package app

import (
	"encoding/json"
	"image"
	"os"
	"reflect"
	"strings"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
	w "github.com/metaspartan/gotui/v5/widgets"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

func TestResolveUIMode(t *testing.T) {
	for _, tc := range []struct{ saved, cli, want string }{
		{"", "", UIModeOriginal}, {"invalid", "", UIModeOriginal},
		{UIModeHtop, "", UIModeHtop}, {UIModeHtop, UIModeOriginal, UIModeOriginal},
		{UIModeOriginal, UIModeHtop, UIModeHtop},
	} {
		got, err := resolveUIMode(tc.saved, tc.cli)
		if err != nil || got != tc.want {
			t.Fatalf("resolve(%q,%q) = %q, %v", tc.saved, tc.cli, got, err)
		}
	}
	if _, err := resolveUIMode("", "rainbow"); err == nil {
		t.Fatal("invalid CLI mode accepted")
	}
}

// Real widgets and a simulated terminal exercise mode changes without native
// metrics or access to the user's terminal/configuration. No parallel tests:
// the application deliberately keeps its UI state in package globals.
func modeTestUI(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	i18n.Init("en")
	oldRuntimeInterval := runtimeInterval.Load()
	runtimeInterval.Store(1000)
	t.Cleanup(func() { runtimeInterval.Store(oldRuntimeInterval) })
	var restore []func()
	t.Cleanup(func() {
		saveConfigFlush()
		for i := len(restore) - 1; i >= 0; i-- {
			restore[i]()
		}
	})
	backend, err := ui.NewBackend(&ui.InitConfig{SimulationMode: true, SimulationSize: image.Pt(80, 24)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.Close)
	replaceUIValue(&restore, &ui.DefaultBackend, backend)
	replaceUIValue(&restore, &currentConfig, AppConfig{DefaultLayout: LayoutDefault, Theme: "green"})
	replaceUIValue(&restore, &runtimeLayout, "")
	replaceUIValue(&restore, &previousStableLayout, "")
	replaceUIValue(&restore, &modeLayouts, map[string]modeLayoutState{})
	replaceUIValue(&restore, &activeOriginalAppearance, (*originalAppearance)(nil))
	replaceUIValue(&restore, &partyTheme, "")
	replaceUIValue(&restore, &activeSettings, (*settingsEditor)(nil))
	replaceUIValue(&restore, &visibleProcesses, ([]ProcessMetrics)(nil))
	replaceUIValue(&restore, &meterValues, map[*w.Gauge]float64{})
	replaceUIValue(&restore, &btopData, btopTelemetry{})
	replaceUIValue(&restore, &btopPanels, map[string]*btopProcessPanel{})
	replaceUIValue(&restore, &btopFocus, "cpu")
	replaceUIValue(&restore, &btopPanelsVisible, false)
	replaceUIValue(&restore, &btopProcessOverride, (*BtopProcessConfig)(nil))
	replaceUIValue(&restore, &btopMemory, MemoryMetrics{})

	replaceUIValue(&restore, &activeUIMode, UIModeOriginal)
	replaceUIValue(&restore, &showHelp, false)
	replaceUIValue(&restore, &isFrozen, false)
	replaceUIValue(&restore, &searchMode, false)
	replaceUIValue(&restore, &searchText, "")
	replaceUIValue(&restore, &killPending, false)
	replaceUIValue(&restore, &lastActiveLayout, LayoutAlternative)
	replaceUIValue(&restore, &temporaryLayout, false)
	replaceUIValue(&restore, &previousLayout, "")
	replaceUIValue(&restore, &previousLayoutTemporary, false)
	replaceUIValue(&restore, &currentLayoutNum, 0)
	replaceUIValue(&restore, &infoScrollOffset, 0)
	replaceUIValue(&restore, &currentColorName, "green")
	replaceUIValue(&restore, &IsLightMode, false)
	replaceUIValue(&restore, &CurrentBgColor, ui.ColorClear)
	replaceUIValue(&restore, &currentUser, "test")
	replaceUIValue(&restore, &lastProcesses, []ProcessMetrics{{PID: 7, CPU: 10, User: "test", Command: "test"}})
	replaceUIValue(&restore, &lastPorts, []PortMetrics{{PID: 7, Port: 8080, Protocol: "TCP", User: "test"}})
	replaceUIValue(&restore, &processList, w.NewList())
	replaceUIValue(&restore, &helpText, w.NewParagraph())
	replaceUIValue(&restore, &mainBlock, ui.NewBlock())
	replaceUIValue(&restore, &grid, ui.NewGrid())
	replaceUIValue(&restore, &cpuGauge, w.NewGauge())
	replaceUIValue(&restore, &gpuGauge, w.NewGauge())
	replaceUIValue(&restore, &memoryGauge, w.NewGauge())
	replaceUIValue(&restore, &aneGauge, w.NewGauge())
	replaceUIValue(&restore, &modelText, w.NewParagraph())
	replaceUIValue(&restore, &NetworkInfo, w.NewParagraph())
	replaceUIValue(&restore, &PowerChart, w.NewParagraph())
	replaceUIValue(&restore, &sparklineGroup, w.NewSparklineGroup(w.NewSparkline()))
	replaceUIValue(&restore, &memoryHistoryChart, w.NewStepChart())
	replaceUIValue(&restore, &infoParagraph, w.NewParagraph())
	replaceUIValue(&restore, &fanStatusPanel, w.NewParagraph())
	replaceUIValue(&restore, &fanTempPanel, w.NewParagraph())
	replaceUIValue(&restore, &fanControlPanel, w.NewParagraph())
	replaceUIValue(&restore, &confirmModal, w.NewModal("Test"))
	cores := &CPUCoreWidget{Block: ui.NewBlock(), cores: make([]float64, 14), labels: make([]string, 14), cpuIndexMap: make([]int, 14)}
	for i := range cores.cpuIndexMap {
		cores.cpuIndexMap[i] = i
	}
	cores.UpdateSample(make([]float64, 14), make([]CPUTimePercent, 14))
	replaceUIValue(&restore, &cpuCoreWidget, cores)
	width, height := GetCachedTerminalDimensions()
	UpdateCachedTerminalDimensions(80, 24)
	t.Cleanup(func() { UpdateCachedTerminalDimensions(width, height) })
}

func readModeConfig(t *testing.T) AppConfig {
	t.Helper()
	saveConfigFlush()
	data, err := os.ReadFile(mactopConfigPath("config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config AppConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestUIModeOverrideDoesNotPersist(t *testing.T) {
	modeTestUI(t)
	currentConfig.UIMode = UIModeOriginal
	activeUIMode, _ = resolveUIMode(currentConfig.UIMode, UIModeHtop)
	currentConfig.Interval = 1200
	saveConfig()
	if saved := readModeConfig(t); saved.UIMode != UIModeOriginal || saved.Interval != 1200 {
		t.Fatalf("CLI override leaked into preferences: %+v", saved)
	}
	setUIMode(UIModeHtop)
	if saved := readModeConfig(t); saved.UIMode != UIModeHtop {
		t.Fatalf("explicit switch not saved: %+v", saved)
	}
}

func TestUIModePreservesHelpAndTemporaryLayouts(t *testing.T) {
	modeTestUI(t)
	showHelp = true
	helpGrid := grid
	for _, mode := range []string{UIModeHtop, UIModeOriginal, UIModeHtop} {
		setUIMode(mode)
		if grid != helpGrid || !showHelp || !strings.Contains(helpText.Text, uiModeName()) {
			t.Fatal("switch replaced the help grid or failed to refresh its text")
		}
		if strings.Contains(mainBlock.TitleBottomLeft, "btop") != (mode == UIModeHtop) {
			t.Fatalf("stale footer after help mode switch to %s: %q", mode, mainBlock.TitleBottomLeft)
		}
	}
	showHelp = false
	setUIMode(UIModeOriginal)
	currentConfig.DefaultLayout = LayoutAlternative
	runtimeLayout = LayoutAlternative
	for _, layout := range []string{LayoutInfo, LayoutFan} {
		toggleTemporaryLayout(layout)
		if effectiveLayout() != layout || !temporaryLayout {
			t.Fatal("temporary layout did not open")
		}
		saveConfig()
		if saved := readModeConfig(t); saved.DefaultLayout != LayoutAlternative {
			t.Fatalf("temporary layout persisted: %+v", saved)
		}
		setUIMode(UIModeHtop)
		if effectiveLayout() != LayoutDefault {
			t.Fatal("Original temporary layout leaked into htop")
		}
		setUIMode(UIModeOriginal)
		if effectiveLayout() != layout || !temporaryLayout {
			t.Fatal("mode lost temporary return state")
		}
		toggleTemporaryLayout(layout)
	}

}

func TestUIModeSavesStableLayoutAfterInfoFanSequence(t *testing.T) {
	for _, keys := range [][]string{{"i", "F"}, {"F", "i"}} {
		t.Run(strings.Join(keys, "-"), func(t *testing.T) {
			modeTestUI(t)
			currentConfig.DefaultLayout = LayoutAlternative
			lastActiveLayout = ""
			for _, key := range keys {
				handleKeyboardEvent(ui.Event{ID: key}, done)
			}
			if lastActiveLayout != LayoutAlternative {
				t.Fatalf("lost stable layout: %q", lastActiveLayout)
			}
			handleKeyboardEvent(ui.Event{ID: "v"}, done)
			if saved := readModeConfig(t); saved.DefaultLayout != LayoutAlternative || saved.UIMode != UIModeHtop {
				t.Fatalf("temporary view leaked into saved config: %+v", saved)
			}
			// Return to the source mode before closing its temporary view.
			setUIMode(UIModeOriginal)
			// Toggling the active temporary view returns directly to the stable layout.
			handleKeyboardEvent(ui.Event{ID: keys[len(keys)-1]}, done)
			if effectiveLayout() != LayoutAlternative {
				t.Fatalf("return layout = %q", effectiveLayout())
			}
		})
	}
}

func TestUIModeKeepsFanSelectedAsPermanentLayout(t *testing.T) {
	modeTestUI(t)
	// Reach Fan with the same l event used for ordinary persistent layouts.
	for i, layout := range layoutOrder {
		if layout == LayoutFan {
			currentConfig.DefaultLayout = layoutOrder[i-1]
			break
		}
	}
	handleKeyboardEvent(ui.Event{ID: "l"}, done)
	if saved := readModeConfig(t); saved.DefaultLayout != LayoutFan || temporaryLayout {
		t.Fatalf("cycled Fan was treated as temporary: %+v", saved)
	}
	// The mode selector and a restart must also preserve an explicitly chosen Fan.
	setUIMode(UIModeHtop)
	loadConfig()
	currentConfig.Interval = 1500
	saveConfig()
	if saved := readModeConfig(t); saved.DefaultLayout != LayoutFan || temporaryLayout {
		t.Fatalf("loaded Fan was treated as temporary: %+v", saved)
	}
	// Info can use a permanently selected Fan as its stable return view.
	toggleInfoLayout()
	setUIMode(UIModeOriginal)
	if saved := readModeConfig(t); saved.DefaultLayout != LayoutFan {
		t.Fatalf("Info lost permanent Fan: %+v", saved)
	}
	if effectiveLayout() != LayoutFan || temporaryLayout {
		t.Fatal("Info did not return to permanent Fan")
	}
}

func TestUIModeRemembersTemporaryViewAcrossDirectLayoutSwitch(t *testing.T) {
	modeTestUI(t)
	currentConfig.DefaultLayout = LayoutAlternative
	toggleInfoLayout()
	switchToLayout(LayoutPorts)
	saveConfig()
	if saved := readModeConfig(t); saved.DefaultLayout != LayoutPorts {
		t.Fatalf("direct layout was treated as temporary: %+v", saved)
	}
	switchToLayout(LayoutPorts)
	saveConfig()
	if effectiveLayout() != LayoutInfo || !temporaryLayout {
		t.Fatal("direct shortcut lost temporary return state")
	}
	if saved := readModeConfig(t); saved.DefaultLayout != LayoutAlternative {
		t.Fatalf("temporary return was persisted: %+v", saved)
	}
}

func TestUIModeRecolorsFrozenProcessesAndPorts(t *testing.T) {
	modeTestUI(t)
	isFrozen = true
	processList.SelectedRow = 1
	searchText = "test"
	filteredProcessesSaved := filteredProcesses
	t.Cleanup(func() { filteredProcesses = filteredProcessesSaved })
	filteredProcesses = lastProcesses
	setUIMode(UIModeHtop)
	if len(processList.Rows) != 2 || !strings.Contains(processList.Rows[0], "bg:green") || processList.SelectedRow != 1 || searchText != "test" || !isFrozen {
		t.Fatalf("frozen process switch lost state: %+v", processList)
	}
	currentConfig.DefaultLayout, searchText = LayoutPorts, ""
	setUIMode(UIModeOriginal)
	setUIMode(UIModeHtop)
	if len(processList.Rows) != 2 || !strings.Contains(processList.Rows[0], "bg:green") {
		t.Fatalf("ports header did not use htop palette: %v", processList.Rows)
	}
}

func TestHtopStylesAreReversibleAndPartyPauses(t *testing.T) {
	modeTestUI(t)
	custom := &CustomThemeConfig{CPU: "#123456", ProcessList: "#abcdef"}
	currentConfig.CustomTheme = custom
	activeUIMode = UIModeHtop
	applyResolvedOriginalAppearance()
	memoryHistoryChart.LineColors = []ui.Color{ui.ColorPink, ui.ColorOrange}
	snapshot := func() []any {
		return []any{cpuGauge.BarColor, cpuGauge.BorderStyle, cpuGauge.TitleStyle, cpuGauge.LabelStyle,
			gpuGauge.BarColor, gpuGauge.BorderStyle, gpuGauge.TitleStyle, gpuGauge.LabelStyle,
			processList.BorderStyle, processList.TextStyle, processList.SelectedStyle,
			memoryHistoryChart.BorderStyle, memoryHistoryChart.TitleStyle, memoryHistoryChart.LineColors}
	}
	before, beforeCPUColor := snapshot(), cpuGauge.BarColor
	for _, light := range []bool{false, true} {
		IsLightMode = light
		renderWithUIMode(func() {
			if cpuGauge.BarColor != htopPalette().user || gpuGauge.BarColor != htopPalette().gpu || processList.SelectedStyle.Bg != ui.ColorCyan {
				t.Fatal("semantic palette was not applied")
			}
		})
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("htop styles leaked into original widgets")
		}
	}
	if advancePartyTheme() || currentConfig.Theme != "green" || currentConfig.CustomTheme != custom {
		t.Fatal("party changed/persisted the original theme under htop")
	}
	setUIMode(UIModeOriginal)
	if currentConfig.CustomTheme != custom || cpuGauge.BarColor != beforeCPUColor {
		t.Fatal("return to Original lost custom styles")
	}
}

func TestUIModeDefaultLayoutAtTerminalSizes(t *testing.T) {
	modeTestUI(t)
	for _, size := range []image.Point{{80, 24}, {120, 40}, {30, 12}, {3, 3}} {
		ui.DefaultBackend.Screen.SetSize(size.X, size.Y)
		UpdateCachedTerminalDimensions(size.X, size.Y)
		for _, mode := range []string{UIModeOriginal, UIModeHtop, UIModeOriginal} {
			setUIMode(mode)
			entries := len(grid.Items)
			updateLayout(size.X, size.Y)
			updateLayout(size.X, size.Y)
			if len(grid.Items) != entries {
				t.Fatal("resize accumulated old grid entries")
			}
			renderContent(size.X, size.Y)
			if mode == UIModeHtop && !strings.Contains(mainBlock.TitleBottomLeft, "btop") {
				t.Fatal("mode indicator lost")
			}
			if mode == UIModeHtop && size.X == 80 && btopContentRect(size.X, size.Y).Dy() < 10 {
				t.Fatal("btop left no usable content area")
			}
		}
	}
}

func TestBtopDefaultLayoutKeepsHistoryAndTopsWithManyCores(t *testing.T) {
	modeTestUI(t)
	activeUIMode = UIModeHtop
	for _, cores := range []int{8, 20, 52, 128} {
		cpuCoreWidget.cores = make([]float64, cores)
		ui.DefaultBackend.Screen.SetSize(120, 40)
		UpdateCachedTerminalDimensions(120, 40)
		applyLayout(LayoutDefault)
		updateProcessList()
		renderContent(120, 40)
		if !btopPanelsVisible || btopContentRect(120, 40).Dy() < 20 {
			t.Fatalf("cores %d displaced charts/processes", cores)
		}
	}
}

func TestUIModeKeyRespectsSearchAndKill(t *testing.T) {
	modeTestUI(t)
	applyLayout(LayoutDefault)
	searchMode = true
	handleKeyboardEvent(ui.Event{ID: "v"}, done)
	if activeUIMode != UIModeOriginal || searchText != "v" {
		t.Fatal("v did not remain search input")
	}
	searchMode, searchText, killPending = false, "", true
	handleKeyboardEvent(ui.Event{ID: "v"}, done)
	if activeUIMode != UIModeOriginal {
		t.Fatal("v switched mode inside kill confirmation")
	}
}

func TestHtopPaletteUsesExplicitBackgroundAndHeaderContrast(t *testing.T) {
	modeTestUI(t)
	activeUIMode = UIModeHtop
	for _, tc := range []struct {
		light  bool
		bg     string
		text   ui.Color
		header string
	}{
		{false, "", ui.ColorWhite, "black"},
		{true, "", ui.ColorWhite, "black"},
		{true, "clear", ui.ColorBlack, "white"},
		{false, "white", ui.ColorBlack, "white"},
		{true, "black", ui.ColorWhite, "black"},
	} {
		IsLightMode = tc.light
		currentConfig.Htop = &HtopConfig{Background: tc.bg}
		_, header := resolveProcessThemeColor()
		if htopPalette().text != tc.text || header != tc.header {
			t.Fatalf("light=%v bg=%v: text=%v header=%s", tc.light, tc.bg, htopPalette().text, header)
		}
	}
}
