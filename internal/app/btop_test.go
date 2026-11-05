package app

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ui "github.com/metaspartan/gotui/v5"
	w "github.com/metaspartan/gotui/v5/widgets"
	"github.com/toon-format/toon-go"
	"gopkg.in/yaml.v3"
)

func TestBtopHistoryValidityWindowAndSource(t *testing.T) {
	var s btopSeries
	at := time.Unix(1000, 0)
	s.add(at, 0, true, "PSTR")
	s.add(at.Add(time.Second), 30, true, "PSTR")
	s.add(at.Add(2*time.Second), 0, false, "PSTR")
	if s.Min != 0 || s.Max != 30 || s.ValidCount != 2 || !math.IsNaN(s.tail(1)[0].Value) {
		t.Fatal("unknown became zero or zero was excluded")
	}
	for i := 0; i < 1100; i++ {
		s.add(at.Add(time.Duration(i+3)*time.Second), 5, true, "PSTR")
	}
	if s.count != btopHistoryCapacity || len(s.tail(2)) != 2 || s.Max != 30 || s.Min != 0 {
		t.Fatal("window overwrote session extrema")
	}
	s.add(at.Add(time.Hour), 10, true, "PD0R")
	if s.Min != 10 || s.Max != 10 || s.count != 1 || s.Source != "PD0R" {
		t.Fatal("sources mixed")
	}
	a, b := btopSeries{}, btopSeries{}
	a.add(at, 50, true, "a")
	b.add(at, 50, true, "a")
	for i := 1; i <= 100; i++ {
		a.add(at.Add(time.Duration(i)*100*time.Millisecond), 0, true, "a")
	}
	b.add(at.Add(10*time.Second), 0, true, "a")
	if math.Abs(a.Scale-b.Scale) > 1e-8 {
		t.Fatal("scale depends on sample frequency")
	}
}

func TestBtopPowerPreservesRawSensorAndJSON(t *testing.T) {
	raw := PowerReadings{CPU: powerReading(8, true, "cpu"), GPU: powerReading(4, true, "gpu"), RAM: powerReading(2, true, "ram"), Sensor: powerReading(10, true, "PSTR")}
	m := normalizeSocMetricsPower(SocMetrics{CPUPower: 8, GPUPower: 4, DRAMPower: 2, SystemPower: 10, TotalPower: 14, PowerReadings: raw})
	if m.TotalPower != 14 || m.SystemPower != 0 || m.PowerReadings.Sensor.Watts != 10 {
		t.Fatal("raw provenance lost to legacy normalization")
	}
	if raw.sum(false).Watts != 12 || raw.sum(true).Watts != 14 {
		t.Fatal("GPU/CPU double counted")
	}
	for _, v := range []any{m, cpuMetricsFromSoc(m, nil, 0, false), GPUMetrics{Valid: true}} {
		b, err := json.Marshal(v)
		if err != nil || strings.Contains(string(b), "PowerReadings") || strings.Contains(string(b), "GPUUsageValid") || strings.Contains(string(b), "Valid") {
			t.Fatal("internal metadata leaked to exported JSON", string(b))
		}
	}
	if powerReading(math.NaN(), true, "x").Valid || !powerReading(0, true, "x").Valid || powerReading(0, false, "x").Valid {
		t.Fatal("invalid zero contract")
	}
	for name, encode := range map[string]func(any) ([]byte, error){"json": json.Marshal, "yaml": yaml.Marshal, "xml": xml.Marshal, "toon": func(v any) ([]byte, error) { return toon.Marshal(v) }} {
		data, err := encode(m)
		if err != nil || strings.Contains(string(data), "PSTR") || strings.Contains(strings.ToLower(string(data)), "powerreadings") || strings.Contains(strings.ToLower(string(data)), "gpuusagevalid") {
			t.Fatalf("%s leaked internal power metadata: %s, %v", name, data, err)
		}
	}
}

func TestBtopTripleRanksUseCompleteFilteredSnapshot(t *testing.T) {
	all := make([]ProcessMetrics, 800)
	for i := range all {
		all[i] = ProcessMetrics{PID: i + 1, CPU: float64(800 - i), CPUValid: true, RSS: int64(i), RSSValid: true, GPU: float64(i), GPUValid: true, User: "alice", Command: "keep", BirthTime: int64(i + 1)}
	}
	all[799].RSS = 1 << 40
	all[799].GPU = 99999
	c := BtopProcessConfig{Count: 3, Name: "keep", User: "alice"}
	for _, metric := range []string{"memory", "gpu"} {
		rows, valid, total := rankedBtopProcesses(all, c, metric, "")
		if len(rows) != 3 || rows[0].PID != 800 || valid != 800 || total != 800 {
			t.Fatalf("lost %s leader", metric)
		}
	}
	all[799].User = "bob"
	all[798].GPUValid = false
	rows, valid, total := rankedBtopProcesses(all, c, "gpu", "")
	if rows[0].PID != 798 || valid != 798 || total != 799 {
		t.Fatal("filter/validity applied after ranking")
	}
	rows, _, _ = rankedBtopProcesses(all, c, "cpu", "missing")
	if len(rows) != 0 {
		t.Fatal("empty filter ignored")
	}
	if all[0].PID != 1 {
		t.Fatal("ranking mutated shared snapshot")
	}
}

func seedBtopTest(t *testing.T) {
	t.Helper()
	modeTestUI(t)
	activeUIMode = UIModeHtop
	lastGPUMetrics = GPUMetrics{Valid: true, ActivePercent: 47, FreqMHz: 1296, Temp: 52}
	lastCPUMetrics.AvgUsage = 34
	lastCPUMetrics.CoreUsages = []float64{34}
	lastCPUMetrics.CoreBreakdown = []CPUTimePercent{{User: 34, Valid: true}}
	at := time.Unix(1000, 0)
	for i := 0; i < 120; i++ {
		p := PowerReadings{CPU: powerReading(8, true, "PMP"), GPU: powerReading(4, true, "GPU"), RAM: powerReading(2, true, "PZD"), Sensor: powerReading(20+float64(i%10), true, "SMC:PSTR")}
		recordBtopPower(p, at.Add(time.Duration(i)*time.Second))
		btopData.history("cpu").add(at.Add(time.Duration(i)*time.Second), float64(i%80), true, "Mach")
		btopData.history("gpu").add(at.Add(time.Duration(i)*time.Second), float64(i%60), true, "GPU")
	}
	lastProcesses = []ProcessMetrics{{PID: 33, CPU: 30, CPUValid: true, RSS: 1, RSSValid: true, GPU: 10, GPUValid: true, Command: "first", User: "alice", BirthTime: 33}, {PID: 22, CPU: 20, CPUValid: true, RSS: 2, RSSValid: true, GPU: 20, GPUValid: true, Command: "second", User: "bob", BirthTime: 22}, {PID: 11, CPU: 10, CPUValid: true, RSS: 3, RSSValid: true, GPU: 30, GPUValid: true, Command: "third", User: "alice", BirthTime: 11}}
	updateProcessList()
}

func btopTestBuffer(w, h int) *ui.Buffer {
	ui.DefaultBackend.Screen.SetSize(w, h)
	UpdateCachedTerminalDimensions(w, h)
	buf := ui.NewBuffer(image.Rect(0, 0, w, h))
	r := &btopRoot{ui.NewBlock()}
	r.SetRect(0, 0, w, h)
	renderWithUIMode(func() { r.Draw(buf) })
	return buf
}

func btopBufferText(buf *ui.Buffer) string {
	var text strings.Builder
	for y := buf.Min.Y; y < buf.Max.Y; y++ {
		for x := buf.Min.X; x < buf.Max.X; x++ {
			r := buf.GetCell(image.Pt(x, y)).Rune
			if r == 0 {
				r = ' '
			}
			text.WriteRune(r)
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func TestBtopPowerPersistsAcrossViewsEditorsAndModal(t *testing.T) {
	seedBtopTest(t)
	for _, size := range []image.Point{{80, 24}, {100, 28}, {120, 32}, {160, 50}} {
		for _, layout := range []string{LayoutDefault, LayoutGPUFocus, LayoutEnergy, LayoutProcesses, LayoutInfo, LayoutFan, LayoutPorts} {
			runtimeLayout = layout
			applyLayout(layout)
			for _, overlay := range []string{"none", "settings", "quick", "help", "kill"} {
				activeSettings = nil
				showHelp = false
				killPending = false
				if overlay == "settings" {
					activeSettings = newSettingsEditor(false)
				}
				if overlay == "quick" {
					activeSettings = newBtopQuickEditor()
				}
				if overlay == "help" {
					showHelp = true
				}
				if overlay == "kill" {
					killPending = true
				}
				buf := btopTestBuffer(size.X, size.Y)
				text := btopBufferText(buf)
				for _, want := range []string{"CPU 8.0W", "GPU 4.0W", "min 20.0W", "max 29.0W"} {
					if !strings.Contains(text, want) {
						t.Fatalf("%v %s %s lost %q\n%s", size, layout, overlay, want, text)
					}
				}
				if activeSettings != nil && activeSettings.Min.Y < btopContentRect(size.X, size.Y).Min.Y {
					t.Fatal("editor covered power")
				}
			}
		}
	}
}

func TestBtopNewestPointAndMeasuredZeroAreVisible(t *testing.T) {
	modeTestUI(t)
	for _, width := range []int{1, 7, 50} {
		var s btopSeries
		at := time.Unix(10, 0)
		for i := 0; i < 200; i++ {
			s.add(at.Add(time.Duration(i)*time.Second), 0, false, "GPU")
		}
		s.add(at.Add(201*time.Second), 100, true, "GPU")
		area := image.Rect(2, 2, width+2, 6)
		buf := ui.NewBuffer(image.Rect(0, 0, width+5, 10))
		drawBtopHistory(buf, area, &s, 100, ui.ColorCyan)
		if buf.GetCell(image.Pt(area.Max.X-1, area.Min.Y)).Rune == 0 {
			t.Fatal("newest point lost on resize", width)
		}
		s.add(at.Add(202*time.Second), 0, true, "GPU")
		buf = ui.NewBuffer(buf.Rectangle)
		drawBtopHistory(buf, area, &s, 100, ui.ColorCyan)
		if buf.GetCell(image.Pt(area.Max.X-1, area.Max.Y-1)).Rune == 0 {
			t.Fatal("valid zero not drawn")
		}
	}
}

func TestBtopPanelFocusAndDisappearingSelection(t *testing.T) {
	seedBtopTest(t)
	runtimeLayout = LayoutDefault
	applyLayout(LayoutDefault)
	btopTestBuffer(80, 24)
	handleBtopProcessKey("<Down>")
	if btopPanels["cpu"].identity.PID != 33 {
		t.Fatal("wrong CPU selection")
	}
	handleBtopProcessKey("<Tab>")
	handleBtopProcessKey("<Down>")
	attemptBtopKill()
	if !killPending || killPID != 11 || killBirthTime != 11 {
		t.Fatal("F9 did not use GPU panel identity")
	}
	hideKillModal()
	lastProcesses = lastProcesses[:2]
	updateProcessList()
	if btopPanels["gpu"].selected != -1 {
		t.Fatal("selection silently moved to replacement")
	}
	attemptBtopKill()
	if killPending {
		t.Fatal("F9 accepted stale selection")
	}
	runtimeLayout = LayoutInfo
	applyLayout(runtimeLayout)
	btopTestBuffer(80, 24)
	attemptKillProcess()
	if killPending {
		t.Fatal("F9 targeted hidden panel")
	}
}

func TestBtopQuickSettingsSessionSaveAndCancel(t *testing.T) {
	seedBtopTest(t)
	path := preferenceTestFile(t, `{"htop":{"processes":{"count":3}},"future":{"keep":42}}`)
	e := newBtopQuickEditor()
	spec := selectEditorField(t, e, "htop.processes.count")
	if err := e.setValue(spec, "10"); err != nil {
		t.Fatal(err)
	}
	close, _ := e.key("<Enter>")
	if !close || btopProcessOptions().Count != 10 || currentConfig.Htop.Processes.Count != 3 {
		t.Fatal("session applied to saved preferences")
	}
	e = newBtopQuickEditor()
	spec = selectEditorField(t, e, "htop.processes.count")
	_ = e.setValue(spec, "5")
	e.key("<Escape>")
	if btopProcessOptions().Count != 10 {
		t.Fatal("cancel changed session")
	}
	e = newBtopQuickEditor()
	close, _ = e.key("<F10>")
	if !close || btopProcessOverride != nil || currentConfig.Htop.Processes.Count != 10 {
		t.Fatal("explicit save lost session choices", e.status)
	}
	obj := readPreferenceObject(t, path)
	var future map[string]int
	_ = json.Unmarshal(obj["future"], &future)
	if future["keep"] != 42 {
		t.Fatal("unknown preferences lost")
	}
}

func TestBtopAliasAndEvidence(t *testing.T) {
	seedBtopTest(t)
	for _, override := range []string{"btop", "htop"} {
		mode, err := resolveUIMode("original", override)
		if err != nil || mode != UIModeHtop {
			t.Fatal("alias failed")
		}
	}
	for _, name := range []string{LayoutDefault, LayoutGPUFocus, LayoutEnergy, LayoutProcesses} {
		runtimeLayout = name
		applyLayout(name)
		text := btopBufferText(btopTestBuffer(120, 32))
		if !strings.Contains(text, "Top 3") || !strings.Contains(text, "min 20.0W") {
			t.Fatal("missing default content", name)
		}
		if dir := os.Getenv("MACTOP_CAPTURE_DIR"); dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.txt", name)), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestBtopEveryLegacyLayoutKeepsPower(t *testing.T) {
	seedBtopTest(t)
	// Fixtures for optional legacy panels; no native sampler is needed.
	var restore []func()
	t.Cleanup(func() {
		for i := len(restore) - 1; i >= 0; i-- {
			restore[i]()
		}
	})
	for _, ptr := range []**w.StepChart{&cpuHistoryChart, &gpuHistoryChart, &powerHistoryChart, &memBWHistoryChart, &memoryPressureHistoryChart, &aneHistoryChart, &bandwidthHistoryChart, &socPowerHistoryChart, &ssdReadHistoryChart} {
		replaceUIValue(&restore, ptr, w.NewStepChart())
	}
	for _, ptr := range []**w.Paragraph{&memoryPressurePanel, &tbInfoParagraph} {
		replaceUIValue(&restore, ptr, w.NewParagraph())
	}
	replaceUIValue(&restore, &memoryPressureGauge, w.NewGauge())
	for _, ptr := range []**w.SparklineGroup{&sparklineGroup, &gpuSparklineGroup, &tbNetSparklineGroup} {
		replaceUIValue(&restore, ptr, w.NewSparklineGroup(w.NewSparkline()))
	}
	for _, layout := range activeLayoutOrder() {
		runtimeLayout = layout
		applyLayout(layout)
		text := btopBufferText(btopTestBuffer(80, 24))
		if !strings.Contains(text, "CPU 8.0W") || !strings.Contains(text, "max 29.0W") {
			t.Fatal("legacy layout hid power", layout)
		}
	}
}

func TestBtopListFiltersAndCountSurviveResize(t *testing.T) {
	seedBtopTest(t)
	currentConfig.Htop = &HtopConfig{Processes: &BtopProcessConfig{Mode: "list", Name: "i", User: "alice", Count: 10}}
	runtimeLayout = LayoutProcesses
	applyLayout(runtimeLayout)
	updateProcessList()
	btopTestBuffer(80, 24)
	if len(visibleProcesses) != 2 {
		t.Fatal("list ignored configured filters")
	}
	processList.SelectedRow = 1
	old := visibleProcesses[0]
	var next []ProcessMetrics
	for _, p := range lastProcesses {
		if p.PID != old.PID {
			next = append(next, p)
		}
	}
	lastProcesses = next
	updateProcessList()
	if processList.SelectedRow != 0 {
		t.Fatal("list selection moved to replacement")
	}
	currentConfig.Htop.Processes.Mode = "tops"
	updateProcessList()
	for _, size := range []image.Point{{80, 24}, {160, 50}, {100, 28}} {
		btopTestBuffer(size.X, size.Y)
		if btopProcessOptions().Count != 10 {
			t.Fatal("resize changed N")
		}
	}
}

func TestBtopPowerTextRestoresOriginalPresentation(t *testing.T) {
	seedBtopTest(t)
	PowerChart.Text = "Original battery and supply details"
	renderWithUIMode(func() {
		if PowerChart.Text == "Original battery and supply details" {
			t.Fatal("new UI did not identify its residual")
		}
	})
	if PowerChart.Text != "Original battery and supply details" {
		t.Fatal("new power labels leaked into Original")
	}
}

func TestBtopFullEditorCanReplaceAnEqualSavedValue(t *testing.T) {
	seedBtopTest(t)
	preferenceTestFile(t, `{"htop":{"processes":{"count":3}}}`)
	c := btopProcessOptions()
	c.Count = 10
	btopProcessOverride = &c
	e := newSettingsEditor(false)
	spec := selectEditorField(t, e, "htop.processes.count")
	if settingOverride(spec) == "" || effectiveSettingValue(spec) != "10" {
		t.Fatal("session override was not identified")
	}
	if err := e.setValue(spec, "3"); err != nil {
		t.Fatal(err)
	}
	closed, _ := e.key("<F10>")
	if !closed || btopProcessOptions().Count != 3 || btopProcessOverride != nil {
		t.Fatal("explicit full-editor save did not replace session override", e.status)
	}
}

func TestBtopQuickResetAllFieldsAppliesDefaults(t *testing.T) {
	seedBtopTest(t)
	e := newBtopQuickEditor()
	for _, key := range []string{"htop.processes.mode", "htop.processes.count", "htop.processes.blocks", "htop.processes.name", "htop.processes.user"} {
		selectEditorField(t, e, key)
		e.key("d")
	}
	closed, _ := e.key("<Enter>")
	c := btopProcessOptions()
	if !closed || c.Mode != "tops" || c.Count != 3 || c.Blocks != "cpu,gpu,memory" {
		t.Fatal("reset did not safely apply defaults", c)
	}
}

func TestBtopNativeCounterKnownZeroAndGating(t *testing.T) {
	validated := false
	for _, watts := range []float64{2, 0, 3, 0} {
		valid := nativePowerCounterAvailable(watts, true, validated, false)
		if !valid {
			t.Fatal("native producer rejected working counter", watts)
		}
		validated = valid
	}
	for _, tc := range []struct {
		watts                      float64
		observed, validated, gated bool
	}{{0, true, false, false}, {0, true, true, true}, {5, false, true, false}, {-1, true, true, false}, {math.NaN(), true, true, false}} {
		if nativePowerCounterAvailable(tc.watts, tc.observed, tc.validated, tc.gated) {
			t.Fatal("missing or gated native counter became valid")
		}
	}
}

func TestBtopReturningSourceRestoresSessionExtrema(t *testing.T) {
	var s btopSeries
	at := time.Unix(1000, 0)
	s.add(at, 30, true, "PSTR")
	s.add(at.Add(time.Second), 10, true, "PD0R")
	s.add(at.Add(2*time.Second), 12, true, "PSTR")
	if s.Min != 12 || s.Max != 30 || s.ValidCount != 2 || !s.Started.Equal(at) || s.count != 1 {
		t.Fatal("returning source lost statistics or mixed graph segments")
	}
	s.add(at.Add(3*time.Second), 20, true, "PD0R")
	if s.Min != 10 || s.Max != 20 || s.ValidCount != 2 {
		t.Fatal("source banks mixed")
	}
	modeTestUI(t)
	for i := 0; i < 10; i++ {
		recordBtopPower(PowerReadings{CPU: powerReading(10, true, "CPU"), RAM: powerReading(7, true, "RAM"), ANE: powerReading(float64(i%2)*.5, true, "ANE")}, at.Add(time.Duration(i)*time.Second))
	}
	sum := btopData.history("sum")
	if sum.count != 10 || sum.Min != 17 || sum.Max != 17.5 {
		t.Fatal("valid idle ANE reset the sum history")
	}
}

func TestBtopMixedIntervalsDoNotCreateFalseGaps(t *testing.T) {
	modeTestUI(t)
	var s btopSeries
	at := time.Unix(1000, 0)
	for i := 0; i < 30; i++ {
		offset := time.Duration(i) * time.Second
		if i >= 20 {
			offset = 20*time.Second + time.Duration(i-20)*10*time.Second
		}
		s.add(at.Add(offset), 30, true, "GPU")
	}
	area := image.Rect(1, 1, 16, 5)
	buf := ui.NewBuffer(image.Rect(0, 0, 20, 8))
	drawBtopHistory(buf, area, &s, 100, ui.ColorCyan)
	for x := area.Min.X; x < area.Max.X; x++ {
		occupied := false
		for y := area.Min.Y; y < area.Max.Y; y++ {
			r := buf.GetCell(image.Pt(x, y)).Rune
			if r != 0 && r != ' ' {
				occupied = true
			}
		}
		if !occupied {
			t.Fatal("valid mixed-interval samples made a false gap", x)
		}
	}
}

func TestBtopScaleCoversVisibleSpikeAndDecaysAfterItLeaves(t *testing.T) {
	modeTestUI(t)
	var s btopSeries
	at := time.Unix(1000, 0)
	s.add(at, 50, true, "W")
	for i := 1; i <= 60; i++ {
		s.add(at.Add(time.Duration(i)*time.Second), 5, true, "W")
	}
	plot := btopWindow(&s, 100, 0)
	if plot.ceiling < 55 {
		t.Fatal("visible 50W spike clipped under a lower scale", plot.ceiling)
	}
	var short btopSeries
	short.add(at, 50, true, "W")
	for i := 1; i < 20; i++ {
		short.add(at.Add(time.Duration(i)*time.Second), 5, true, "W")
	}
	first := btopWindow(&short, 10, 0)
	short.add(at.Add(20*time.Second), 5, true, "W")
	second := btopWindow(&short, 10, 0)
	if first.ceiling < 55 || second.ceiling >= first.ceiling || second.ceiling <= 5.5 {
		t.Fatal("axis dropped abruptly when the spike left", first.ceiling, second.ceiling)
	}
}

func TestBtopBlockWindowLabelAndFilledBars(t *testing.T) {
	modeTestUI(t)
	currentConfig.Htop = &HtopConfig{BarStyle: "blocks"}
	s := btopData.history("gpu")
	at := time.Unix(1000, 0)
	for i := 0; i < 200; i++ {
		s.add(at.Add(time.Duration(i)*time.Second), 100, true, "GPU")
	}
	buf := ui.NewBuffer(image.Rect(0, 0, 40, 8))
	drawBtopChart(buf, buf.Rectangle, "GPU", "gpu", 100, ui.ColorCyan)
	if !strings.Contains(btopBufferText(buf), "38 samples · 37s") {
		t.Fatal("block label did not use the rendered window", btopBufferText(buf))
	}
	area := image.Rect(1, 1, 2, 5)
	buf = ui.NewBuffer(image.Rect(0, 0, 4, 7))
	drawBtopHistory(buf, area, s, 100, ui.ColorCyan)
	for y := area.Min.Y; y < area.Max.Y; y++ {
		if buf.GetCell(image.Pt(1, y)).Rune != '█' {
			t.Fatal("block bar floats above unfilled rows", y)
		}
	}
}

func TestBtopEnergyAvailabilityAtMinimumSize(t *testing.T) {
	seedBtopTest(t)
	runtimeLayout = LayoutEnergy
	applyLayout(LayoutEnergy)
	text := btopBufferText(btopTestBuffer(80, 24))
	if !strings.Contains(text, "Display / external devices: unavailable") {
		t.Fatal("minimum energy view hid availability", text)
	}
	// The two component boxes remain labelled when only two interior rows fit.
	for _, key := range []string{"cpu_w", "gpu_w"} {
		plot := btopWindow(btopData.history(key), 37, 0)
		if !strings.Contains(text, fmt.Sprintf("0–%.1f W", plot.ceiling)) {
			t.Fatal("minimum energy view hid component axis", key, text)
		}
	}
}

func TestBtopNativeDomainMaskIncludesGPUAndSRAM(t *testing.T) {
	zero := [5]float64{}
	if got := nativePowerDomainsAvailable(zero, 31, 0, 0); got != 0 {
		t.Fatal("never-working domain accepted zero", got)
	}
	working := [5]float64{2, 3, 1, 4, 5}
	validated := nativePowerDomainsAvailable(working, 31, 0, 0)
	if validated != 31 {
		t.Fatal("producer failed to validate all domains", validated)
	}
	if got := nativePowerDomainsAvailable(zero, 31, validated, 0); got != 31 {
		t.Fatal("known valid zero was discarded", got)
	}
	if got := nativePowerDomainsAvailable(zero, 31, validated, 17); got&17 != 0 {
		t.Fatal("CPU gating left zero Energy Model DRAM valid", got)
	}
	for _, bit := range []uint32{1, 2, 4, 8, 16} {
		if got := nativePowerDomainsAvailable(zero, 31&^bit, validated, 0); got&bit != 0 {
			t.Fatal("missing domain stayed valid", bit)
		}
	}
}

func TestBtopMissingPowerHasNoInventedStartTime(t *testing.T) {
	seedBtopTest(t)
	btopData = btopTelemetry{}
	currentConfig.Htop = &HtopConfig{Power: &BtopPowerConfig{Source: "ram"}}
	runtimeLayout = LayoutDefault
	applyLayout(runtimeLayout)
	text := btopBufferText(btopTestBuffer(120, 32))
	if strings.Contains(text, "00:00:00") || !strings.Contains(text, "min —") {
		t.Fatal("missing power had invented epoch", text)
	}
}

func TestBtopCPUWarmupDoesNotAppearAsMeasuredZero(t *testing.T) {
	modeTestUI(t)
	if btopCPUUsageValid(CPUMetrics{CoreUsages: []float64{0}}) {
		t.Fatal("missing baseline accepted")
	}
	m := CPUMetrics{CoreUsages: []float64{0}, CoreBreakdown: []CPUTimePercent{{Valid: true}}}
	if !btopCPUUsageValid(m) {
		t.Fatal("measured idle zero discarded")
	}
	m.AvgUsage = math.NaN()
	if btopCPUUsageValid(m) {
		t.Fatal("invalid usage accepted")
	}
}

func TestBtopNetDiskProducerValidityIsIndependent(t *testing.T) {
	netDiskMutex.Lock()
	oldNet, oldDisk, oldTime, oldState := lastNetStats, lastDiskStats, lastNetDiskTime, btopRateState
	lastNetStats, lastDiskStats, lastNetDiskTime, btopRateState = NativeNetMetric{}, NativeDiskMetric{}, time.Time{}, btopRateBaselines{}
	netDiskMutex.Unlock()
	t.Cleanup(func() {
		netDiskMutex.Lock()
		lastNetStats, lastDiskStats, lastNetDiskTime, btopRateState = oldNet, oldDisk, oldTime, oldState
		netDiskMutex.Unlock()
	})
	at := time.Unix(1000, 0)
	network := func(n uint64) map[string]NativeNetMetric {
		return map[string]NativeNetMetric{"en0": {BytesRecv: n, BytesSent: n}}
	}
	disk := func(n uint64) map[string]NativeDiskMetric {
		return map[string]NativeDiskMetric{"disk0": {ReadBytes: n, WriteBytes: n}}
	}
	sample := func(sec int, net map[string]NativeNetMetric, ne error, drives map[string]NativeDiskMetric, de error) NetDiskMetrics {
		return getNetDiskMetricsFrom(at.Add(time.Duration(sec)*time.Second), func() (map[string]NativeNetMetric, error) { return net, ne }, func() (map[string]NativeDiskMetric, error) { return drives, de })
	}
	m := sample(0, network(100), nil, disk(1024), nil)
	if m.NetworkValid || m.DiskValid {
		t.Fatal("first sample had invented baseline")
	}
	m = sample(1, network(100), nil, disk(1024), nil)
	if !m.NetworkValid || !m.DiskValid || m.InBytesPerSec != 0 || m.ReadKBytesPerSec != 0 {
		t.Fatal("measured zero deltas were discarded")
	}
	m = sample(2, nil, errors.New("network failed"), disk(2048), nil)
	if m.NetworkValid || !m.DiskValid {
		t.Fatal("network failure affected disk validity")
	}
	m = sample(3, network(300), nil, nil, errors.New("disk failed"))
	if m.NetworkValid || m.DiskValid {
		t.Fatal("recovery or failed disk had invented baseline")
	}
	m = sample(4, network(300), nil, disk(4096), nil)
	if !m.NetworkValid || m.DiskValid {
		t.Fatal("disk recovery affected network")
	}
	m = sample(5, network(300), nil, disk(4096), nil)
	if !m.NetworkValid || !m.DiskValid {
		t.Fatal("fresh baselines did not recover")
	}
	m = sample(6, network(0), nil, disk(0), nil)
	if m.NetworkValid || m.DiskValid {
		t.Fatal("counter resets became huge measured rates")
	}
	m = sample(7, map[string]NativeNetMetric{"en1": {BytesRecv: 500}}, nil, map[string]NativeDiskMetric{}, nil)
	if m.NetworkValid || m.DiskValid {
		t.Fatal("device changes or empty provider became measured traffic")
	}
}

func TestBtopRateGapsAndMetadataFormats(t *testing.T) {
	modeTestUI(t)
	at := time.Unix(1000, 0)
	recordBtopRates(NetDiskMetrics{DiskValid: true}, at)
	if !math.IsNaN(btopData.history("rx").tail(1)[0].Value) || btopData.history("disk_r").tail(1)[0].Value != 0 {
		t.Fatal("provider validity not propagated to history")
	}
	recordBtopRates(NetDiskMetrics{NetworkValid: true}, at.Add(time.Second))
	if btopData.history("rx").tail(1)[0].Value != 0 || !math.IsNaN(btopData.history("disk_r").tail(1)[0].Value) {
		t.Fatal("network and disk not independent")
	}
	if btopNetworkRates(NetDiskMetrics{}) != "—" || btopDiskRates(NetDiskMetrics{}) != "—" {
		t.Fatal("missing rates presented as zero")
	}
	metrics := NetDiskMetrics{NetworkValid: true, DiskValid: true}
	for name, encode := range map[string]func(any) ([]byte, error){"json": json.Marshal, "yaml": yaml.Marshal, "xml": xml.Marshal, "toon": func(v any) ([]byte, error) { return toon.Marshal(v) }} {
		data, err := encode(metrics)
		text := strings.ToLower(string(data))
		if err != nil || strings.Contains(text, "networkvalid") || strings.Contains(text, "diskvalid") {
			t.Fatalf("%s leaked private validity: %s %v", name, data, err)
		}
	}
}

func TestBtopNativeGatingUsesActivityAndAllDomains(t *testing.T) {
	mask := nativePowerGatedDomains(true, false, 0, 0, 0, 60, true)
	if mask != 31 || nativePowerDomainsAvailable([5]float64{}, 31, 31, mask) != 0 {
		t.Fatal("degraded active GPU/SRAM accepted zero", mask)
	}
	idle := nativePowerGatedDomains(true, false, 0, 0, 0, 0, false)
	if idle != 17 || nativePowerDomainsAvailable([5]float64{}, 31, 31, idle) != 14 {
		t.Fatal("known idle GPU/ANE zeros were discarded", idle)
	}
	positive := nativePowerGatedDomains(true, false, 3, 1, 2, 60, true)
	if positive != 17 {
		t.Fatal("positive GPU/ANE readings incorrectly gated", positive)
	}
}
