package app

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func preferenceTestFile(t *testing.T, data string) string {
	t.Helper()
	saveConfigFlush()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	before := currentConfig
	t.Cleanup(func() { saveConfigFlush(); currentConfig = before; resetModeLayouts(); activeOriginalAppearance = nil })
	path := mactopConfigPath("config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if data != "" {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	loadConfig()
	return path
}

func readPreferenceObject(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestPreferenceRecoveryAndUnknownLeaves(t *testing.T) {
	path := preferenceTestFile(t, `{"theme":"blue","interval":"bad","future":{"large":9876543210987654321},"menubar":{"status_bar_width":"bad","show_cpu":false,"future":{"keep":true}}}`)
	if currentConfig.Theme != "blue" || currentConfig.Interval != 0 || currentConfig.MenuBar == nil || currentConfig.MenuBar.ShowCPU == nil || *currentConfig.MenuBar.ShowCPU {
		t.Fatalf("lost valid siblings: %+v", currentConfig)
	}
	draft := cloneConfig(currentConfig)
	draft.Interval = 1400
	if err := commitPreferenceDraft(draft); err != nil {
		t.Fatal(err)
	}
	obj := readPreferenceObject(t, path)
	if !strings.Contains(string(obj["future"]), "9876543210987654321") {
		t.Fatal("unknown integer changed precision")
	}
	if !strings.Contains(string(obj["menubar"]), `"bad"`) || !strings.Contains(string(obj["menubar"]), `"keep"`) {
		t.Fatal("unedited invalid or unknown nested leaf lost")
	}
}

func TestEditorExplicitResetRepairsOnlyTouchedInvalidLeaf(t *testing.T) {
	path := preferenceTestFile(t, `{"interval":"incorrecto","sort_reverse":"invalid","future":{"keep":true}}`)
	e := newSettingsEditor(true)
	selectEditorField(t, e, "interval")
	e.key("d")
	closed, quit := e.key("<F10>")
	if !closed || quit {
		t.Fatalf("reset was not saved: %s", e.status)
	}
	obj := readPreferenceObject(t, path)
	if _, exists := obj["interval"]; exists {
		t.Fatal("explicit default left the invalid interval in the document")
	}
	if string(obj["sort_reverse"]) != `"invalid"` || !strings.Contains(string(obj["future"]), "true") {
		t.Fatal("reset changed an unedited invalid/unknown leaf")
	}
	// Explicit false equals the normalized default, but must repair the raw value.
	e = newSettingsEditor(true)
	spec := selectEditorField(t, e, "sort_reverse")
	if err := e.setValue(spec, "false"); err != nil {
		t.Fatal(err)
	}
	if closed, _ := e.key("<F10>"); !closed {
		t.Fatal(e.status)
	}
	obj = readPreferenceObject(t, path)
	if string(obj["sort_reverse"]) != "false" {
		t.Fatal("explicit normalized value did not repair the raw leaf")
	}
}

func TestPreferenceWritesPreserveRelativeSymlink(t *testing.T) {
	path := preferenceTestFile(t, "")
	dotfiles := filepath.Join(filepath.Dir(path), "dotfiles")
	if err := os.Mkdir(dotfiles, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dotfiles, "mactop.json")
	if err := os.WriteFile(target, []byte(`{"interval":1000,"unknown":true}`), 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join("dotfiles", "mactop.json")
	if err := os.Symlink(link, path); err != nil {
		t.Fatal(err)
	}
	loadConfig()
	draft := cloneConfig(currentConfig)
	draft.Interval = 1300
	if err := commitPreferenceDraft(draft); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(path); err != nil || got != link {
		t.Fatalf("symlink was replaced: %q %v", got, err)
	}
	obj := readPreferenceObject(t, target)
	if string(obj["interval"]) != "1300" || string(obj["unknown"]) != "true" {
		t.Fatal("write did not reach the target")
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("target permissions changed")
	}
	// Aliased and direct writers must merge through the same physical lock.
	var group sync.WaitGroup
	errors := make(chan error, 2)
	for i, file := range []string{path, target} {
		group.Add(1)
		go func(i int, file string) {
			defer group.Done()
			errors <- writePreferencePatch(file, preferencePatch{fmt.Sprintf("writer%d", i): json.RawMessage(`true`)})
		}(i, file)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	obj = readPreferenceObject(t, target)
	if string(obj["writer0"]) != "true" || string(obj["writer1"]) != "true" {
		t.Fatal("aliased writers lost a patch")
	}
}

func TestPreferenceDanglingSymlinkCreatesTargetAndLoopIsPreserved(t *testing.T) {
	path := preferenceTestFile(t, "")
	target := filepath.Join(filepath.Dir(path), "new-target.json")
	if err := os.Symlink("new-target.json", path); err != nil {
		t.Fatal(err)
	}
	if err := writePreferencePatch(path, preferencePatch{"theme": json.RawMessage(`"blue"`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(path); err != nil {
		t.Fatal("dangling link was replaced")
	}
	if string(readPreferenceObject(t, target)["theme"]) != `"blue"` {
		t.Fatal("target was not created")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("config.json", path); err != nil {
		t.Fatal(err)
	}
	if err := writePreferencePatch(path, preferencePatch{"theme": json.RawMessage(`"red"`)}); err == nil {
		t.Fatal("link loop was accepted")
	}
	if got, err := os.Readlink(path); err != nil || got != "config.json" {
		t.Fatal("looping link was damaged")
	}
}

func TestPreferenceInvalidJSONIsNeverReplaced(t *testing.T) {
	broken := `{"interval":oops`
	path := preferenceTestFile(t, broken)
	draft := cloneConfig(currentConfig)
	draft.Background = "white"
	if err := commitPreferenceDraft(draft); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if currentConfig.Background != "" {
		t.Fatal("failed editor commit applied its draft")
	}
	saveConfigFlush()
	got, _ := os.ReadFile(path)
	if string(got) != broken {
		t.Fatal("invalid original was overwritten")
	}
}

func TestPreferenceExplicitSaveSupersedesDebounce(t *testing.T) {
	path := preferenceTestFile(t, `{"interval":1000,"future":true}`)
	currentConfig.Interval = 1200
	saveConfig()
	draft := cloneConfig(currentConfig)
	draft.Interval = 1600
	if err := commitPreferenceDraft(draft); err != nil {
		t.Fatal(err)
	}
	saveConfigFlush()
	obj := readPreferenceObject(t, path)
	if string(obj["interval"]) != "1600" || string(obj["future"]) != "true" {
		t.Fatalf("stale debounce won: %s", obj)
	}
}

func TestOverlayPreferencePatchRespectsXDGAndSiblings(t *testing.T) {
	path := preferenceTestFile(t, `{"overlay":{"sections":"cpu,gpu","unknown":{"keep":7}},"theme":"sand"}`)
	if err := saveOverlayPreferences("cpu,gpu", "memory,cpu", .7); err != nil {
		t.Fatal(err)
	}
	obj := readPreferenceObject(t, path)
	var overlay map[string]json.RawMessage
	_ = json.Unmarshal(obj["overlay"], &overlay)
	if string(overlay["sections"]) != `"cpu,gpu"` || !strings.Contains(string(overlay["unknown"]), "7") {
		t.Fatal("overlay callback replaced siblings")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".mactop", "config.json")); !os.IsNotExist(err) {
		t.Fatal("overlay ignored XDG")
	}
	if err := os.WriteFile(path, []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveOverlayPreferences("cpu", "gpu", .8); err == nil {
		t.Fatal("overlay accepted invalid JSON")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "{invalid" {
		t.Fatal("overlay destroyed invalid document")
	}
}

func TestPreferenceConcurrentWritersMergeLeaves(t *testing.T) {
	path := preferenceTestFile(t, `{"overlay":{"future":true},"future":99}`)
	patches := []preferencePatch{{"overlay.opacity": json.RawMessage(`0.7`)}, {"menubar.show_cpu": json.RawMessage(`false`)}, {"htop.layout": json.RawMessage(`"top3"`)}}
	var group sync.WaitGroup
	errs := make(chan error, len(patches))
	for _, patch := range patches {
		group.Add(1)
		go func(p preferencePatch) { defer group.Done(); errs <- writePreferencePatch(path, p) }(patch)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	obj := readPreferenceObject(t, path)
	if len(obj) != 4 || !strings.Contains(string(obj["overlay"]), "future") || !strings.Contains(string(obj["htop"]), "top3") {
		t.Fatalf("lost concurrent patch: %s", obj)
	}
}

func TestNativeSnapshotDoesNotRevertNewerTUIField(t *testing.T) {
	path := preferenceTestFile(t, `{"overlay":{"opacity":0.5,"sections":"cpu,gpu"},"future":true}`)
	var worker nativePreferenceSnapshot
	initial, _ := overlaySnapshotValues([]string{"cpu"}, []string{"cpu", "memory"}, .5)
	worker.record(initial)
	// Another process confirms a new opacity while this worker stays open.
	if err := writePreferencePatch(path, preferencePatch{"overlay.opacity": json.RawMessage(`0.8`)}); err != nil {
		t.Fatal(err)
	}
	changed, _ := overlaySnapshotValues([]string{"gpu"}, []string{"cpu", "memory"}, .5)
	if err := worker.commit(changed); err != nil {
		t.Fatal(err)
	}
	obj := readPreferenceObject(t, path)
	var overlay map[string]json.RawMessage
	_ = json.Unmarshal(obj["overlay"], &overlay)
	var collapsed []string
	_ = json.Unmarshal(overlay["collapsed_sections"], &collapsed)
	if string(overlay["opacity"]) != "0.8" || !reflect.DeepEqual(collapsed, []string{"gpu"}) {
		t.Fatalf("worker reverted unrelated edited field: %s", obj["overlay"])
	}
}

func TestEditorSaveFailureKeepsDraftForRetry(t *testing.T) {
	path := preferenceTestFile(t, "")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	draft := cloneConfig(currentConfig)
	draft.Interval = 1700
	if err := commitPreferenceDraft(draft); err == nil {
		t.Fatal("directory accepted as config file")
	}
	if currentConfig.Interval != 0 || draft.Interval != 1700 {
		t.Fatal("failed save changed preferences or draft")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := commitPreferenceDraft(draft); err != nil {
		t.Fatal(err)
	}
	if currentConfig.Interval != 1700 {
		t.Fatal("retry did not apply draft")
	}
}

func TestSettingsCatalogCoversEveryPreferenceLeaf(t *testing.T) {
	known := map[string]bool{}
	for _, s := range settingCatalog {
		if known[s.Key] {
			t.Fatalf("duplicate %s", s.Key)
		}
		known[s.Key] = true
	}
	var walk func(reflect.Type, string)
	walk = func(typ reflect.Type, prefix string) {
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			if !known[strings.TrimSuffix(prefix, ".")] {
				t.Errorf("missing editor/legacy entry: %s", prefix)
			}
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if f.Anonymous {
				walk(f.Type, prefix)
			} else {
				walk(f.Type, prefix+name+".")
			}
		}
	}
	walk(reflect.TypeOf(AppConfig{}), "")
	if known["processes.pid"] {
		t.Fatal("PID must not be a persisted preference")
	}
}

func TestSettingsCatalogCoversPublicRegisteredFlags(t *testing.T) {
	catalog := map[string]bool{"menubar-worker": true, "overlay-worker": true}
	for _, s := range settingCatalog {
		for _, name := range s.CLI {
			catalog[strings.TrimLeft(name, "-")] = true
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "app.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "flag" {
			return true
		}
		index := 0
		if strings.HasSuffix(selector.Sel.Name, "Var") {
			index = 1
		}
		if len(call.Args) <= index {
			return true
		}
		literal, ok := call.Args[index].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(literal.Value)
		if err == nil && !catalog[name] {
			t.Errorf("public flag is missing from Settings/actions: --%s", name)
		}
		return true
	})
}
