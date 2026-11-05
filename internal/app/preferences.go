package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"
)

const saveDebounceInterval = 500 * time.Millisecond

// Every writer, including Cocoa callbacks in worker processes, applies leaf
// patches to the latest document. Unknown and invalid, unedited leaves survive.
type preferencePatch map[string]json.RawMessage // nil removes a known leaf

var preferences = struct {
	sync.Mutex
	path      string
	baseline  map[string]json.RawMessage
	pending   preferencePatch
	timer     *time.Timer
	lastError string
}{}

func cloneConfig(c AppConfig) AppConfig {
	b, _ := json.Marshal(c)
	var result AppConfig
	_ = json.Unmarshal(b, &result)
	return result
}

func configLeaves(c AppConfig) map[string]json.RawMessage {
	b, _ := json.Marshal(c)
	var root map[string]json.RawMessage
	_ = json.Unmarshal(b, &root)
	result := make(map[string]json.RawMessage)
	var walk func(map[string]json.RawMessage, string)
	walk = func(obj map[string]json.RawMessage, prefix string) {
		for key, raw := range obj {
			path := prefix + key
			var child map[string]json.RawMessage
			if len(raw) > 0 && raw[0] == '{' && json.Unmarshal(raw, &child) == nil {
				walk(child, path+".")
			} else {
				result[path] = raw
			}
		}
	}
	walk(root, "")
	return result
}

func diffPreferences(before, after map[string]json.RawMessage) preferencePatch {
	patch := make(preferencePatch)
	for path, value := range after {
		if !bytes.Equal(value, before[path]) {
			patch[path] = value
		}
	}
	for path := range before {
		if _, exists := after[path]; !exists {
			patch[path] = nil
		}
	}
	return patch
}

func mergePatches(a, b preferencePatch) preferencePatch {
	result := make(preferencePatch, len(a)+len(b))
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

// Decode fields independently so one mistyped value does not discard siblings.
func decodePreferenceFields(raw json.RawMessage, dst reflect.Value) {
	if dst.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return
		}
		value := reflect.New(dst.Type().Elem())
		if dst.Type().Elem().Kind() != reflect.Struct {
			if json.Unmarshal(raw, value.Interface()) == nil {
				dst.Set(value)
			}
		} else {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) == nil && object != nil {
				decodePreferenceFields(raw, value.Elem())
				dst.Set(value)
			}
		}
		return
	}
	if dst.Kind() != reflect.Struct {
		value := reflect.New(dst.Type())
		if json.Unmarshal(raw, value.Interface()) == nil {
			dst.Set(value.Elem())
		}
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return
	}
	for i := 0; i < dst.NumField(); i++ {
		field := dst.Type().Field(i)
		if field.Anonymous {
			decodePreferenceFields(raw, dst.Field(i))
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if value, ok := obj[name]; ok && name != "-" {
			decodePreferenceFields(value, dst.Field(i))
		}
	}
}

func loadConfig() {
	saveConfigFlush()
	path := mactopConfigPath("config.json")
	cfg := AppConfig{DefaultLayout: LayoutDefault}
	var loadErr error
	data, err := os.ReadFile(path)
	if err == nil {
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil || object == nil {
			loadErr = fmt.Errorf("invalid configuration JSON: %s (file preserved)", path)
		} else {
			decodePreferenceFields(data, reflect.ValueOf(&cfg).Elem())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		loadErr = err
	}
	if cfg.UIMode == "btop" {
		cfg.UIMode = UIModeHtop
	}
	cfg.Theme = migrateThemeName(cfg.Theme)
	validateLoadedPreferences(&cfg)
	currentConfig = cfg
	temporaryLayout = false
	resetModeLayouts()
	preferences.Lock()
	defer preferences.Unlock()
	preferences.path, preferences.baseline = path, configLeaves(cfg)
	preferences.pending = nil
	preferences.lastError = ""
	if loadErr != nil {
		preferences.lastError = loadErr.Error()
	}
}

func preferenceStorePathLocked() string {
	path := mactopConfigPath("config.json")
	if preferences.path != path {
		if preferences.timer != nil {
			preferences.timer.Stop()
			preferences.timer = nil
		}
		preferences.path, preferences.baseline = path, make(map[string]json.RawMessage)
		preferences.pending, preferences.lastError = nil, ""
	}
	return path
}

// Existing explicit keyboard actions still coalesce. The timer never reads UI
// state and never takes renderMutex; serialization includes the disk write.
func saveConfig() {
	preferences.Lock()
	defer preferences.Unlock()
	preferenceStorePathLocked()
	leaves := configLeaves(currentConfig)
	preferences.pending = mergePatches(preferences.pending, diffPreferences(preferences.baseline, leaves))
	preferences.baseline = leaves
	if preferences.timer != nil {
		preferences.timer.Stop()
	}
	preferences.timer = time.AfterFunc(saveDebounceInterval, flushPendingSave)
}

func flushPendingSave() {
	preferences.Lock()
	defer preferences.Unlock()
	flushPreferencesLocked()
}

func flushPreferencesLocked() {
	if len(preferences.pending) == 0 {
		return
	}
	if err := writePreferencePatch(preferences.path, preferences.pending); err != nil {
		preferences.lastError = err.Error()
		stderrLogger.Printf("Configuration was not saved: %v", err)
		return // retain patch for a later retry
	}
	preferences.pending = nil
	preferences.lastError = ""
}

func saveConfigFlush() {
	preferences.Lock()
	defer preferences.Unlock()
	if preferences.timer != nil {
		preferences.timer.Stop()
		preferences.timer = nil
	}
	flushPreferencesLocked()
}

// An editor commit is synchronous. Failure leaves both saved preferences and
// the editable draft untouched, and cannot queue the draft for a later write.
func commitPreferenceDraft(draft AppConfig, explicit ...preferencePatch) error {
	preferences.Lock()
	defer preferences.Unlock()
	path := preferenceStorePathLocked()
	patch := mergePatches(preferences.pending, diffPreferences(configLeaves(currentConfig), configLeaves(draft)))
	// A normalized invalid leaf may already equal the draft's default. An
	// explicit reset/edit still has to replace or remove that on-disk leaf.
	for _, edits := range explicit {
		patch = mergePatches(patch, edits)
	}
	if len(patch) > 0 {
		if err := writePreferencePatch(path, patch); err != nil {
			preferences.lastError = err.Error()
			return err
		}
	}
	if preferences.timer != nil {
		preferences.timer.Stop()
		preferences.timer = nil
	}
	preferences.pending = nil
	preferences.baseline = configLeaves(draft)
	preferences.lastError = ""
	currentConfig = cloneConfig(draft)
	return nil
}

// Native workers only patch the fields the user changed in that worker.
func commitNativePreferences(patch preferencePatch) error {
	preferences.Lock()
	defer preferences.Unlock()
	path := preferenceStorePathLocked()
	if err := writePreferencePatch(path, mergePatches(preferences.pending, patch)); err != nil {
		preferences.lastError = err.Error()
		return err
	}
	preferences.pending = nil
	preferences.lastError = ""
	return nil
}

func preferenceError() string {
	preferences.Lock()
	defer preferences.Unlock()
	return preferences.lastError
}

func setRawPreference(obj map[string]json.RawMessage, keys []string, value json.RawMessage) error {
	key := keys[0]
	if len(keys) == 1 {
		if value == nil {
			delete(obj, key)
		} else {
			obj[key] = value
		}
		return nil
	}
	child := make(map[string]json.RawMessage)
	if raw, ok := obj[key]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		// A malformed known parent is recoverable when explicitly editing one
		// of its children. Unrelated parents are left byte-for-byte intact.
		_ = json.Unmarshal(raw, &child)
		if child == nil {
			child = make(map[string]json.RawMessage)
		}
	}
	if err := setRawPreference(child, keys[1:], value); err != nil {
		return err
	}
	if len(child) == 0 {
		delete(obj, key)
		return nil
	}
	encoded, err := json.Marshal(child)
	if err != nil {
		return err
	}
	obj[key] = encoded
	return nil
}

func writePreferencePatch(path string, patch preferencePatch) error {
	if path == "" {
		return fmt.Errorf("configuration path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Atomic replacement must target the real file, not replace a user's
	// dotfile symlink. All aliases also share the same destination lock.
	destination, err := resolvePreferenceDestination(path)
	if err != nil {
		return err
	}
	path, dir = destination, filepath.Dir(destination)
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			return fmt.Errorf("configuration is busy: %w", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	obj := make(map[string]json.RawMessage)
	mode := os.FileMode(0600)
	if data, readErr := os.ReadFile(path); readErr == nil {
		if json.Unmarshal(data, &obj) != nil || obj == nil {
			return fmt.Errorf("invalid configuration JSON; original file preserved: %s", path)
		}
		if info, e := os.Stat(path); e == nil {
			mode = info.Mode().Perm()
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	for key, value := range patch {
		if err := setRawPreference(obj, strings.Split(key, "."), value); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	readback, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, readback) {
		return fmt.Errorf("configuration verification failed")
	}
	return nil
}

// Resolve existing links, including relative/dangling final links whose parent
// exists. Never replace an unresolved link or silently create its target dirs.
func resolvePreferenceDestination(path string) (string, error) {
	for hops := 0; hops < 40; hops++ {
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return "", err
		}
		path = filepath.Join(parent, filepath.Base(path))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(parent, target)
		}
		path = target
	}
	return "", fmt.Errorf("configuration symlink chain is too long")
}
