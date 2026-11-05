package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
	w "github.com/metaspartan/gotui/v5/widgets"
)

func TestTop3UsesCompleteSampleBeforeLegacyLimit(t *testing.T) {
	all := make([]ProcessMetrics, 800)
	for i := range all {
		all[i] = ProcessMetrics{PID: i + 1, CPU: float64(800 - i), RSS: int64(i), GPU: float64(i), CPUValid: true, RSSValid: true, GPUValid: true, Command: fmt.Sprintf("process-%d", i)}
	}
	all[799].RSS, all[799].GPU = 1<<40, 99999
	legacy := legacyProcessView(all)
	if len(legacy) != 500 || legacy[len(legacy)-1].PID == 800 {
		t.Fatal("legacy projection changed")
	}
	for _, metric := range []string{"memory", "gpu"} {
		view, valid, total := top3Processes(all, metric, "")
		if len(view) != 3 || view[0].PID != 800 || valid != 800 || total != 800 {
			t.Fatalf("%s leader lost: %+v", metric, view)
		}
	}
	if all[0].PID != 1 || all[799].PID != 800 {
		t.Fatal("ranking mutated source")
	}
	view, _, total := top3Processes(all, "cpu", "process-799")
	if len(view) != 1 || view[0].PID != 800 || total != 1 {
		t.Fatal("search ran after ranking")
	}
}

func TestTop3UnknownIsDifferentFromValidZero(t *testing.T) {
	all := []ProcessMetrics{{PID: 4, GPU: 999}, {PID: 3, GPU: math.NaN(), GPUValid: true}, {PID: 2, GPUValid: true}, {PID: 1, GPUValid: true}}
	view, valid, total := top3Processes(all, "gpu", "")
	if len(view) != 2 || view[0].PID != 1 || view[1].PID != 2 || valid != 2 || total != 4 {
		t.Fatalf("unknown or tie ranking wrong: %+v", view)
	}
	view, valid, _ = top3Processes(all, "cpu", "")
	if len(view) != 0 || valid != 0 {
		t.Fatal("missing baseline was ranked as zero CPU")
	}
}

func TestTop3GPUDoesNotRenderUnknownTaskMemoryAsZero(t *testing.T) {
	modeTestUI(t)
	activeUIMode, runtimeLayout = UIModeHtop, LayoutTop3
	currentConfig.Htop = &HtopConfig{TopMetric: "gpu"}
	processList.SelectedRow = 1
	p := ProcessMetrics{PID: 8, User: "test", Command: "gpu-reader", GPU: 100, GPUValid: true}
	row := buildProcessRows([]ProcessMetrics{p}, calculateMaxWidths(100))[0]
	fields := strings.Fields(row)
	for _, index := range []int{2, 3, 4, 6, 7} {
		if fields[index] != "--" {
			t.Fatalf("unknown task field %d looks measured: %q", index, row)
		}
	}
	if fields[5] != "10.0%" {
		t.Fatal("valid GPU value was hidden")
	}
	p.RSSValid, p.CPUValid, p.MemoryPercentValid = true, true, true
	fields = strings.Fields(buildProcessRows([]ProcessMetrics{p}, calculateMaxWidths(100))[0])
	if fields[2] == "--" || fields[3] == "--" {
		t.Fatal("known zero memory was hidden")
	}
}

func TestSettingsClearOverrideKeepsItsName(t *testing.T) {
	modeTestUI(t)
	old := cliBgColor
	defer func() { cliBgColor = old }()
	spec := settingSpec{Key: "htop.background"}
	cliBgColor = "clear"
	if effectiveSettingValue(spec) != "clear" {
		t.Fatal("clear CLI override was presented as a color")
	}
	cliBgColor = ""
	currentConfig.Htop = &HtopConfig{Background: "clear"}
	if effectiveSettingValue(spec) != "clear" {
		t.Fatal("saved clear background lost its name")
	}
}

func TestCLIColorPresetNamesRemainCaseInsensitive(t *testing.T) {
	modeTestUI(t)
	oldFg, oldBg := cliFgColor, cliBgColor
	defer func() { cliFgColor, cliBgColor = oldFg, oldBg }()
	cliFgColor, cliBgColor = "RED", "WHITE"
	normalizeCLIColors()
	a := resolveOriginalAppearance(currentConfig)
	if a.Theme != "red" || a.Background != "white" || htopEffectiveBg() != ui.ColorWhite {
		t.Fatal("legacy named color case handling regressed")
	}
	cliFgColor, cliBgColor = "#ABCDEF", "CLEAR"
	normalizeCLIColors()
	if cliFgColor != "#ABCDEF" || cliBgColor != "clear" {
		t.Fatal("hex/clear normalization changed their meaning")
	}
}

func TestGPUClientsBeyond256AreAggregatedWithoutUnknownZeros(t *testing.T) {
	clients := make([]GPUProcessStat, 700)
	for i := range clients {
		clients[i] = GPUProcessStat{PID: i + 1, GPUTimeNs: uint64(i), Valid: true}
	}
	clients = append(clients, GPUProcessStat{PID: 700, GPUTimeNs: 9999, Valid: true}, GPUProcessStat{PID: 2, Valid: false})
	got := aggregateGPUClients(clients)
	if len(got) != 699 || got[700] != 10698 {
		t.Fatalf("truncated or lost multi-client time: %d %d", len(got), got[700])
	}
	if _, ok := got[2]; ok {
		t.Fatal("partially unknown PID was treated as known")
	}
	if v, ok := got[1]; !ok || v != 0 {
		t.Fatal("valid observed zero was lost")
	}
}

func TestTop3VisibleSnapshotIsKillSourceAndFreezeSource(t *testing.T) {
	modeTestUI(t)
	activeUIMode = UIModeHtop
	runtimeLayout = LayoutTop3
	currentConfig.Htop = &HtopConfig{TopMetric: "memory"}
	lastProcesses = []ProcessMetrics{{PID: 33, RSS: 1, RSSValid: true, Command: "keep"}, {PID: 22, RSS: 20, RSSValid: true, Command: "keep"}, {PID: 11, RSS: 30, RSSValid: true, Command: "keep"}, {PID: 44, RSS: 100, RSSValid: true, Command: "other"}}
	searchText = "keep"
	processList.SelectedRow = 2
	updateProcessList()
	if !strings.Contains(processList.Rows[0], "RES↓") || strings.Contains(processList.Rows[0], "CPU↓") {
		t.Fatal("Top3 header does not describe its rank")
	}
	if visibleProcesses[0].PID != 11 || visibleProcesses[1].PID != 22 {
		t.Fatal("wrong visible rank")
	}
	attemptKillProcess()
	if !killPending || killPID != 22 {
		t.Fatal("F9 did not capture visible PID")
	}
	hideKillModal()
	isFrozen = true
	before := append([]ProcessMetrics(nil), lastProcesses...)
	oldChan := processMetricsChan
	processMetricsChan = make(chan []ProcessMetrics, 1)
	defer func() { processMetricsChan = oldChan }()
	processMetricsChan <- []ProcessMetrics{{PID: 99, RSS: 1000, RSSValid: true}}
	drainProcessMetricsUpdate()
	if !reflect.DeepEqual(before, lastProcesses) {
		t.Fatal("freeze replaced sample")
	}
	currentConfig.Htop.TopMetric = "gpu"
	updateProcessList()
	if len(visibleProcesses) != 0 || processList.SelectedRow != 0 {
		t.Fatal("GPU unavailable displayed fake processes")
	}
}

func selectEditorField(t *testing.T, e *settingsEditor, key string) settingSpec {
	t.Helper()
	for category := range settingsCategories() {
		e.category = category
		for i, s := range e.fields() {
			if s.Key == key {
				e.index = i
				return s
			}
		}
	}
	t.Fatalf("missing editor field %s", key)
	return settingSpec{}
}

func TestSettingsDraftCancelAndDefaultState(t *testing.T) {
	modeTestUI(t)
	currentConfig.MenuBar = &MenuBarConfig{ShowCPU: new(bool), BarOrder: "memory,cpu,gpu,ane"}
	e := newSettingsEditor(false)
	s := selectEditorField(t, e, "menubar.show_cpu")
	if err := e.setValue(s, "true"); err != nil {
		t.Fatal(err)
	}
	if *currentConfig.MenuBar.ShowCPU {
		t.Fatal("draft shares pointer with saved config")
	}
	if err := e.setValue(s, ""); err != nil {
		t.Fatal(err)
	}
	if e.draft.MenuBar.ShowCPU != nil {
		t.Fatal("Default is not nil")
	}
	close, quit := e.key("<Escape>")
	if !close || quit || *currentConfig.MenuBar.ShowCPU {
		t.Fatal("cancel changed saved state")
	}
}

func TestSettingsScalarBooleansCycleInBothDirections(t *testing.T) {
	modeTestUI(t)
	for _, key := range []string{"sort_reverse", "output.pretty"} {
		for _, direction := range []string{"<Right>", "<Left>", "<Space>", " "} {
			t.Run(key+direction, func(t *testing.T) {
				e := newSettingsEditor(true)
				selectEditorField(t, e, key)
				value := func() bool {
					if key == "sort_reverse" {
						return e.draft.SortReverse
					}
					return e.draft.Output != nil && e.draft.Output.Pretty
				}
				if value() {
					t.Fatal("expected false initial value")
				}
				e.key(direction)
				if !value() {
					t.Fatal("first press did not toggle false to true")
				}
				e.key(direction)
				if value() {
					t.Fatal("second press did not toggle true to false")
				}
				e.key(direction)
				e.key("d")
				if value() {
					t.Fatal("default reset did not restore false")
				}
				if raw, touched := e.edits[key]; !touched || raw != nil {
					t.Fatal("default reset lost its explicit removal")
				}
			})
		}
	}
}

func TestSettingsOwnsKeysAndPreservesHelp(t *testing.T) {
	modeTestUI(t)
	activeUIMode = UIModeHtop
	applyLayout(LayoutDefault)
	searchMode = true
	handleKeyboardEvent(ui.Event{ID: "<F2>"}, done)
	if activeSettings != nil {
		t.Fatal("settings intercepted search")
	}
	searchMode = false
	killPending = true
	handleKeyboardEvent(ui.Event{ID: "<F2>"}, done)
	if activeSettings != nil {
		t.Fatal("settings intercepted confirmation")
	}
	killPending = false
	toggleHelpMenu()
	helpGrid := grid
	handleKeyboardEvent(ui.Event{ID: "<F2>"}, done)
	if activeSettings == nil {
		t.Fatal("F2 did not open editor")
	}
	for _, key := range []string{"v", "+", "-", "a", "0", "9", "R"} {
		handleKeyboardEvent(ui.Event{ID: key}, done)
	}
	if activeUIMode != UIModeHtop || grid != helpGrid || !showHelp {
		t.Fatal("editor leaked keys or replaced help")
	}
	handleKeyboardEvent(ui.Event{ID: "<Escape>"}, done)
	if activeSettings != nil || grid != helpGrid || !showHelp {
		t.Fatal("cancel lost help")
	}
}

func TestHtopKillModalContrastUsesEffectiveBackground(t *testing.T) {
	modeTestUI(t)
	activeUIMode = UIModeHtop
	IsLightMode = true
	currentConfig.Background = "white"
	updateKillModal()
	if confirmModal.TextStyle.Fg != ui.ColorWhite || confirmModal.TextStyle.Bg != ui.NewRGBColor(0, 0, 0) {
		t.Fatal("light terminal made black modal text on the new black background")
	}
	currentConfig.Htop = &HtopConfig{Background: "white"}
	updateKillModal()
	if confirmModal.TextStyle.Fg != ui.ColorBlack {
		t.Fatal("white configured background did not select dark modal text")
	}
}

func TestStartupSettingsSaveThenStart(t *testing.T) {
	modeTestUI(t)
	events := make(chan ui.Event, 8)
	// New TUI -> Settings -> UI mode enum -> Right (Original) -> Save -> Start.
	for _, key := range []string{"1", "2", "<Right>", "<F10>", "1"} {
		events <- ui.Event{Type: ui.KeyboardEvent, ID: key}
	}
	close(events)
	mode, ok := chooseStartupUIMode(events)
	if !ok || mode != UIModeHtop || currentConfig.UIMode != UIModeOriginal {
		t.Fatalf("startup settings flow failed: %s %v %+v", mode, ok, currentConfig)
	}
	if activeSettings != nil {
		t.Fatal("startup editor leaked into monitor")
	}
}

func TestSettingsAndMetersAreClippedAndBlack(t *testing.T) {
	modeTestUI(t)
	activeUIMode = UIModeHtop
	currentConfig.Background = "white"
	IsLightMode = true
	black := ui.NewRGBColor(0, 0, 0)
	if htopEffectiveBg() != black || htopLightBackground() {
		t.Fatal("Original/system background leaked into default htop")
	}
	for _, size := range []image.Point{{0, 0}, {1, 1}, {3, 3}, {20, 6}, {80, 24}} {
		for _, drawable := range []ui.Drawable{newSettingsEditor(false), &htopGauge{w.NewGauge()}, &htopHistory{w.NewStepChart()}, &htopSparklines{w.NewSparklineGroup(w.NewSparkline())}} {
			drawable.SetRect(2, 2, 2+size.X, 2+size.Y)
			buf := ui.NewBuffer(image.Rect(0, 0, 100, 40))
			drawable.Draw(buf)
			for index, cell := range buf.Cells {
				point := image.Pt(index%buf.Dx(), index/buf.Dx())
				if !point.In(drawable.GetRect()) && cell != ui.CellClear {
					t.Fatalf("%T %v drew outside at %v", drawable, size, point)
				}
			}
			if size.X >= 20 && buf.GetCell(image.Pt(3, 3)).Style.Bg != black {
				t.Fatalf("%T background is not explicit black", drawable)
			}
		}
	}
}

func TestMeterUsesFloatAndCleansPreviousFill(t *testing.T) {
	modeTestUI(t)
	activeUIMode = UIModeHtop
	currentConfig.Htop = &HtopConfig{BarStyle: "gradient_blocks"}
	g := w.NewGauge()
	g.SetRect(0, 0, 30, 4)
	g.BarColor = ui.NewRGBColor(0, 255, 0)
	meter := &htopGauge{g}
	for _, percent := range []float64{100, 9.9, math.NaN(), 0} {
		meterValues[g] = percent
		buf := ui.NewBuffer(image.Rect(0, 0, 30, 4))
		meter.Draw(buf)
		count := 0
		var colors = map[ui.Color]bool{}
		for _, cell := range buf.Cells {
			if cell.Rune == meterGlyph() {
				count++
				colors[cell.Style.Fg] = true
			}
		}
		if math.IsNaN(percent) || percent == 0 {
			if count != 0 {
				t.Fatal("invalid/zero painted blocks")
			}
		}
		if percent == 100 && (count < 15 || len(colors) < 2) {
			t.Fatal("missing blocks or gradient")
		}
		if percent == 9.9 && (count < 1 || count > 3) {
			t.Fatalf("float/trailing fill wrong: %d", count)
		}
	}
}

func TestOriginalComponentPreferencesHaveConsumers(t *testing.T) {
	modeTestUI(t)
	var restore []func()
	defer func() {
		for i := len(restore) - 1; i >= 0; i-- {
			restore[i]()
		}
	}()
	replaceUIValue(&restore, &tbInfoParagraph, w.NewParagraph())
	replaceUIValue(&restore, &bandwidthHistoryChart, w.NewStepChart())
	color := "#bada55"
	want, _ := ParseHexColor(color)
	cases := map[string]func() ui.Color{
		"cpu": func() ui.Color { return cpuGauge.BarColor }, "gpu": func() ui.Color { return gpuGauge.BarColor }, "memory": func() ui.Color { return memoryGauge.BarColor }, "ane": func() ui.Color { return aneGauge.BarColor },
		"bandwidth": func() ui.Color { return bandwidthHistoryChart.LineColors[0] }, "network": func() ui.Color { return NetworkInfo.BorderStyle.Fg }, "power": func() ui.Color { return PowerChart.BorderStyle.Fg }, "thunderbolt": func() ui.Color { return tbInfoParagraph.BorderStyle.Fg }, "systemInfo": func() ui.Color { return modelText.BorderStyle.Fg }, "processList": func() ui.Color { return processList.BorderStyle.Fg }, "processListSelected": func() ui.Color { return processList.SelectedStyle.Fg },
	}
	for key, get := range cases {
		currentConfig = AppConfig{DefaultLayout: LayoutDefault, Theme: "green", ThemeSource: "config"}
		currentConfig = withConfigRawValue(currentConfig, "custom_theme."+key, json.RawMessage(`"`+color+`"`))
		applyResolvedOriginalAppearance()
		if got := get(); got != want {
			t.Errorf("%s control has no effect: %v", key, got)
		}
	}
	currentConfig.CustomTheme = &CustomThemeConfig{ProcessListDim: color}
	applyResolvedOriginalAppearance()
	if GetProcessTextColor(false) != color {
		t.Fatal("processListDim has no consumer")
	}
}

func TestOriginalThemeFilePrecedenceAndEditorOverride(t *testing.T) {
	modeTestUI(t)
	if err := os.MkdirAll(mactopConfigDir(), 0700); err != nil {
		t.Fatal(err)
	}
	file := []byte(`{"foreground":"#123456","background":"#ffffff","cpu":"#bada55"}`)
	if err := os.WriteFile(mactopConfigPath("theme.json"), file, 0600); err != nil {
		t.Fatal(err)
	}
	currentConfig.Theme = "green"
	currentConfig.ThemeSource = "auto"
	applyResolvedOriginalAppearance()
	fileCPU, _ := ParseHexColor("#bada55")
	if cpuGauge.BarColor != fileCPU || currentConfig.CustomTheme != nil || currentConfig.Theme != "green" {
		t.Fatal("file did not win without mutating saved preferences")
	}
	e := newSettingsEditor(true)
	spec := selectEditorField(t, e, "custom_theme.cpu")
	if err := e.setValue(spec, "#ff0000"); err != nil {
		t.Fatal(err)
	}
	if e.draft.ThemeSource != "config" {
		t.Fatal("color editor did not make its source explicit")
	}
	if err := commitPreferenceDraft(e.draft); err != nil {
		t.Fatal(err)
	}
	applyResolvedOriginalAppearance()
	red, _ := ParseHexColor("#ff0000")
	if cpuGauge.BarColor != red {
		t.Fatal("saved component override has no effect")
	}
	loadConfig()
	applyResolvedOriginalAppearance()
	if cpuGauge.BarColor != red {
		t.Fatal("theme.json overwrote confirmed config source at restart")
	}
	got, _ := os.ReadFile(mactopConfigPath("theme.json"))
	if string(got) != string(file) {
		t.Fatal("editor wrote theme.json")
	}
}

func TestThemeImportUsesCanonicalPrimaryKeysAndLeavesLegacyAliases(t *testing.T) {
	modeTestUI(t)
	currentConfig.CustomTheme = &CustomThemeConfig{Foreground: "#010203", Background: "#040506"}
	e := newSettingsEditor(true)
	if err := e.importTheme(&CustomThemeConfig{Foreground: "#abcdef", Background: "#ffffff", CPU: "#bada55"}); err != nil {
		t.Fatal(err)
	}
	if e.draft.Theme != "#abcdef" || e.draft.Background != "#ffffff" || e.draft.CustomTheme.CPU != "#bada55" {
		t.Fatal("import did not set canonical colors")
	}
	if e.draft.CustomTheme.Foreground != "#010203" || e.draft.CustomTheme.Background != "#040506" {
		t.Fatal("import changed read-only legacy aliases")
	}
	if _, exists := e.edits["custom_theme.foreground"]; exists {
		t.Fatal("import writes legacy alias")
	}
	before := cloneConfig(e.draft)
	if err := e.importTheme(&CustomThemeConfig{CPU: "invalid"}); err == nil {
		t.Fatal("invalid import was accepted")
	}
	if !reflect.DeepEqual(before, e.draft) {
		t.Fatal("invalid import partially modified the draft")
	}
}

func TestSettingsRefreshesInactiveModeLayoutPreference(t *testing.T) {
	modeTestUI(t)
	currentConfig.DefaultLayout = LayoutAlternative
	runtimeLayout = LayoutAlternative
	setUIMode(UIModeHtop)
	before := cloneConfig(currentConfig)
	currentConfig.DefaultLayout = LayoutMemory
	applySavedSettings(before)
	setUIMode(UIModeOriginal)
	if effectiveLayout() != LayoutMemory {
		t.Fatal("inactive mode restored a stale cached layout")
	}
}

func TestRuntimeOverridesNeverBecomePreferences(t *testing.T) {
	modeTestUI(t)
	oldFlags := flag.CommandLine
	flag.CommandLine = flag.NewFlagSet("mactop", flag.ContinueOnError)
	defer func() { flag.CommandLine = oldFlags }()
	oldInterval, oldBg, oldFg, oldLang := updateInterval, cliBgColor, cliFgColor, cliLanguage
	defer func() { updateInterval, cliBgColor, cliFgColor, cliLanguage = oldInterval, oldBg, oldFg, oldLang }()
	flag.IntVar(&updateInterval, "interval", 1000, "")
	flag.StringVar(&cliBgColor, "bg", "", "")
	flag.StringVar(&cliFgColor, "foreground", "", "")
	flag.StringVar(&cliLanguage, "lang", "", "")
	if err := flag.CommandLine.Parse([]string{"--interval=250", "--bg=#ff0000", "--lang=ja"}); err != nil {
		t.Fatal(err)
	}
	currentConfig.Language = "es"
	currentConfig.Interval = 1400
	currentConfig.Background = "white"
	if err := resolveRuntimePreferences(); err != nil {
		t.Fatal(err)
	}
	applyResolvedOriginalAppearance()
	if getUpdateInterval() != 250 || resolvedLanguagePreference() != "ja" {
		t.Fatal("overrides not effective")
	}
	before := cloneConfig(currentConfig)
	currentConfig.SortReverse = true
	saveConfig()
	saved := readModeConfig(t)
	if saved.Interval != 1400 || saved.Language != "es" || saved.Background != "white" {
		t.Fatalf("CLI persisted: %+v", saved)
	}
	setUIMode(UIModeHtop)
	red, _ := ParseHexColor("#ff0000")
	if htopEffectiveBg() != red {
		t.Fatal("CLI bg did not follow mode")
	}
	setUIMode(UIModeOriginal)
	if currentConfig.Background != before.Background {
		t.Fatal("mode switch saved CLI bg")
	}
}

func TestIntervalReadersConcurrentWithSettingsChanges(t *testing.T) {
	modeTestUI(t)
	var group sync.WaitGroup
	for i := 0; i < 4; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 2000; j++ {
				value := getUpdateInterval()
				if value < 100 || value > 5000 {
					t.Errorf("invalid sampled interval: %d", value)
					return
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		renderMutex.Lock()
		err := setUpdateInterval(100 + i*10)
		renderMutex.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	group.Wait()
}
