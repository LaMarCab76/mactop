package app

import (
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

const (
	LayoutEnergy    = "energy"
	LayoutProcesses = "processes"
)

func btopLayoutName(layout string) string {
	key := map[string]string{LayoutDefault: "Btop_Summary", LayoutGPUFocus: "Btop_GPUView", LayoutEnergy: "Btop_EnergyView", LayoutProcesses: "Btop_ProcessesView", LayoutTop3: "Btop_LegacyTop"}[layout]
	if key != "" {
		return i18n.T(key)
	}
	return layout
}

type btopRoot struct{ *ui.Block }
type btopDashboard struct{ *ui.Block }
type btopProcessArea struct{ *ui.Block }

func btopText(buf *ui.Buffer, rect image.Rectangle, y int, text string, color ui.Color) {
	if rect.Dx() > 0 && y >= rect.Min.Y && y < rect.Max.Y {
		buf.SetString(runewidth.Truncate(text, rect.Dx(), ""), ui.NewStyle(color, htopEffectiveBg()), image.Pt(rect.Min.X, y))
	}
}

func btopBox(buf *ui.Buffer, rect image.Rectangle, title string, color ui.Color) image.Rectangle {
	if rect.Dx() < 2 || rect.Dy() < 2 {
		return image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X, rect.Min.Y)
	}
	b := ui.NewBlock()
	b.BorderRounded = true
	b.BackgroundColor = htopEffectiveBg()
	b.BorderStyle = ui.NewStyle(color, htopEffectiveBg())
	b.TitleStyle = b.BorderStyle
	b.Title = runewidth.Truncate(" "+title+" ", max(0, rect.Dx()-4), "")
	b.SetRect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y)
	b.Draw(buf)
	return b.Inner
}

func btopPowerHeight(height int) int {
	if effectiveLayout() == LayoutEnergy {
		return max(8, min(24, height/2))
	}
	return max(8, min(24, height/4))
}

func btopContentRect(w, h int) image.Rectangle {
	height := btopPowerHeight(max(0, h-2))
	return image.Rect(1, min(max(1, h-1), 1+height), max(1, w-1), max(1, h-1))
}

func (r *btopRoot) Draw(buf *ui.Buffer) {
	p := htopPalette()
	bg := htopEffectiveBg()
	buf.Fill(ui.NewCell(' ', ui.NewStyle(p.text, bg)), r.Rectangle)
	btopPanelsVisible = false
	if mainBlock != nil {
		mainBlock.SetRect(r.Min.X, r.Min.Y, r.Max.X, r.Max.Y)
		mainBlock.Draw(buf)
	}
	w, h := r.Dx(), r.Dy()
	if w < 3 || h < 3 {
		return
	}
	content := btopContentRect(w, h)
	drawBtopPower(buf, image.Rect(1, 1, w-1, content.Min.Y))
	if w < 80 || h < 24 {
		btopText(buf, content, content.Min.Y, fmt.Sprintf(i18n.T("Btop_MinSize"), w, h), p.text)
		return
	}
	if activeSettings != nil {
		activeSettings.SetRect(content.Min.X, content.Min.Y, content.Max.X, content.Max.Y)
		activeSettings.Draw(buf)
		return
	}
	if grid != nil {
		grid.SetRect(content.Min.X, content.Min.Y, content.Max.X, content.Max.Y)
		grid.Draw(buf)
	}
	if killPending && confirmModal != nil {
		confirmModal.SetRect(content.Min.X, content.Min.Y, content.Max.X, content.Max.Y)
		confirmModal.Draw(buf)
	}
}

func btopWatts(r PowerReading) string {
	if !r.Valid {
		return "—"
	}
	return fmt.Sprintf("%.1fW", r.Watts)
}

func btopLegacyPowerText(p PowerReadings) string {
	remainder := "—"
	sum := p.sum(true)
	if p.Sensor.Valid && sum.Valid && p.Sensor.Watts >= sum.Watts {
		remainder = fmt.Sprintf("%.1fW", p.Sensor.Watts-sum.Watts)
	}
	return fmt.Sprintf("CPU %s | GPU %s\nRAM %s | ANE %s\n%s %s\n%s %s", btopWatts(p.CPU), btopWatts(p.gpu()), btopWatts(p.RAM), btopWatts(p.ANE), i18n.T("Btop_Equipment"), btopWatts(p.Sensor), i18n.T("Btop_Unattributed"), remainder)
}

func btopNetworkRates(m NetDiskMetrics) string {
	if !m.NetworkValid {
		return "—"
	}
	return "↓" + formatBytes(m.InBytesPerSec, networkUnit) + " ↑" + formatBytes(m.OutBytesPerSec, networkUnit)
}

func btopDiskRates(m NetDiskMetrics) string {
	if !m.DiskValid {
		return "—"
	}
	return "R " + formatBytes(m.ReadKBytesPerSec*1024, diskUnit) + " W " + formatBytes(m.WriteKBytesPerSec*1024, diskUnit)
}

func btopLegacyNetworkText() string {
	lines := strings.SplitN(NetworkInfo.Text, "\n", 3)
	text := i18n.T("Btop_Network") + " " + btopNetworkRates(lastNetDiskMetrics) + "\n" + i18n.T("Btop_Disk") + " " + btopDiskRates(lastNetDiskMetrics)
	if len(lines) == 3 {
		text += "\n" + lines[2]
	}
	return text
}

func drawBtopPower(buf *ui.Buffer, rect image.Rectangle) {
	p := htopPalette()
	key, reading := btopMainPower()
	s := btopData.history(key)
	name := i18n.T(map[string]string{"sensor": "Btop_Equipment", "sum": "Btop_Sum", "soc": "Btop_SoC", "ram": "Btop_RAM"}[key])
	if key == "sensor" && s.Source != "" {
		name += " " + s.Source
	}
	plot := btopWindow(s, max(0, rect.Dx()-2), 0)
	inside := btopBox(buf, rect, fmt.Sprintf("%s · %s · 0–%.1fW", i18n.T("Btop_Power"), name, plot.ceiling), p.power)
	if inside.Empty() {
		return
	}
	minimum, maximum := "—", "—"
	if s.ValidCount > 0 {
		minimum = fmt.Sprintf("%.1fW", s.Min)
		maximum = fmt.Sprintf("%.1fW", s.Max)
	}
	stats := fmt.Sprintf("%s %s  min %s  max %s", i18n.T("Btop_Now"), btopWatts(reading), minimum, maximum)
	components := fmt.Sprintf("CPU %s  GPU %s", btopWatts(btopData.power.CPU), btopWatts(btopData.power.gpu()))
	chart := inside
	chart.Min.Y++
	if runewidth.StringWidth(stats+"  "+components) <= inside.Dx() {
		stats += "  " + components
	} else {
		btopText(buf, inside, chart.Min.Y, components, p.text)
		chart.Min.Y++
	}
	btopText(buf, inside, inside.Min.Y, stats, p.text)
	options := btopPowerOptions()
	if chart.Dy() >= 2 {
		var extra []string
		if btopOptional(options.ShowSoC, true) {
			extra = append(extra, i18n.T("Btop_SoC")+" "+btopWatts(btopData.power.sum(false)))
		}
		if btopOptional(options.ShowRAM, true) {
			extra = append(extra, "RAM "+btopWatts(btopData.power.RAM))
		}
		if btopOptional(options.ShowANE, false) {
			extra = append(extra, "ANE "+btopWatts(btopData.power.ANE))
		}
		if len(extra) > 0 {
			btopText(buf, inside, chart.Min.Y, strings.Join(extra, "  ·  "), p.text)
			chart.Min.Y++
		}
		if chart.Dy() >= 2 {
			started := "—"
			if s.ValidCount > 0 && !s.Started.IsZero() {
				started = s.Started.Format("15:04:05")
			}
			info := btopSpan(plot) + " · " + i18n.T("Btop_SessionExtrema") + " " + started
			if key == "sensor" {
				btopText(buf, inside, inside.Max.Y-1, info, p.text)
			} else {
				btopText(buf, inside, inside.Max.Y-1, info+" · "+i18n.T("Btop_Partial"), p.text)
			}
			chart.Max.Y--
		}
	}
	drawBtopPlot(buf, chart, plot, p.power)
}

// A shared window drives rendering, scale and labels in every glyph mode.
// Horizontal positions are sample indices; timestamps describe the real span.
type btopPlot struct {
	points           []btopPoint
	mode, resolution int // ASCII=0, solid blocks=1; one sample per column.
	ceiling          float64
}

func btopGlyphMode() (int, int) {
	utf8 := true
	if screen := ui.DefaultBackend.Screen; screen != nil {
		utf8 = strings.ToUpper(strings.ReplaceAll(screen.CharacterSet(), "-", "")) == "UTF8"
	}
	if utf8 && runewidth.RuneWidth('▁') == 1 {
		return 1, 1
	}
	return 0, 1
}

func btopWindow(s *btopSeries, width int, fixedMax float64) btopPlot {
	mode, resolution := btopGlyphMode()
	plot := btopPlot{points: s.tail(max(0, width) * resolution), mode: mode, resolution: resolution, ceiling: fixedMax}
	if fixedMax > 0 {
		return plot
	}
	target := .1
	for _, p := range plot.points {
		if !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) {
			target = math.Max(target, p.Value*1.1)
		}
	}
	at := time.Time{}
	if len(plot.points) > 0 {
		at = plot.points[len(plot.points)-1].At
	}
	if s.axes == nil {
		s.axes = make(map[int]btopAxis)
	}
	key := width * resolution
	axis, ok := s.axes[key]
	if !ok {
		axis = btopAxis{math.Max(target, s.Scale), at}
	} else {
		elapsed := math.Max(0, at.Sub(axis.At).Seconds())
		axis.Ceiling = math.Max(target, axis.Ceiling*math.Exp(-elapsed/15))
		axis.At = at
	}
	s.axes[key] = axis
	plot.ceiling = axis.Ceiling
	return plot
}

func btopSpan(plot btopPlot) string {
	duration := time.Duration(0)
	if len(plot.points) > 1 {
		duration = plot.points[len(plot.points)-1].At.Sub(plot.points[0].At)
	}
	return fmt.Sprintf("%d %s · %s", len(plot.points), i18n.T("Btop_Samples"), duration.Round(time.Second))
}

func drawBtopHistory(buf *ui.Buffer, area image.Rectangle, s *btopSeries, fixedMax float64, color ui.Color) {
	if area.Empty() {
		return
	}
	drawBtopPlot(buf, area, btopWindow(s, area.Dx(), fixedMax), color)
}

func drawBtopPlot(buf *ui.Buffer, area image.Rectangle, plot btopPlot, color ui.Color) {
	if area.Empty() {
		return
	}
	start := area.Dx()*plot.resolution - len(plot.points)
	style := ui.NewStyle(color, htopEffectiveBg())
	if plot.ceiling <= 0 || math.IsNaN(plot.ceiling) || math.IsInf(plot.ceiling, 0) {
		return
	}
	for i, point := range plot.points {
		if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) || point.Value < 0 {
			continue
		}
		x := start + i
		levels, fill := 1, '#'
		if plot.mode == 1 {
			levels, fill = 8, '█'
		}
		// A measured zero has a thin baseline; missing samples remain gaps.
		units := max(1, int(math.Ceil(math.Min(1, point.Value/plot.ceiling)*float64(area.Dy()*levels))))
		top := area.Max.Y - 1 - (units-1)/levels
		glyph := fill
		if plot.mode == 1 {
			glyph = '▁' + rune((units-1)%8)
		}
		buf.SetCell(ui.NewCell(glyph, style), image.Pt(area.Min.X+x, top))
		for row := top + 1; row < area.Max.Y; row++ {
			buf.SetCell(ui.NewCell(fill, style), image.Pt(area.Min.X+x, row))
		}
	}
}

func drawBtopChart(buf *ui.Buffer, rect image.Rectangle, title, key string, scale float64, color ui.Color, details ...string) {
	inside := btopBox(buf, rect, title, color)
	if inside.Empty() {
		return
	}
	var compactDetails []string
	for _, detail := range details {
		if inside.Dy() < 3 {
			compactDetails = append(compactDetails, detail)
			continue
		}
		btopText(buf, inside, inside.Min.Y, detail, htopPalette().text)
		inside.Min.Y++
	}
	plot := btopWindow(btopData.history(key), inside.Dx(), scale)
	if inside.Dy() >= 2 {
		label := fmt.Sprintf("0–%.0f%%", scale)
		if scale <= 0 {
			label = fmt.Sprintf("0–%.1f W", plot.ceiling)
			if key == "rx" || key == "tx" || strings.HasPrefix(key, "disk_") {
				unit := networkUnit
				if strings.HasPrefix(key, "disk_") {
					unit = diskUnit
				}
				label = "0–" + formatBytes(plot.ceiling, unit)
			}
		}
		info := label + " · " + btopSpan(plot)
		if len(compactDetails) > 0 {
			info = label + " · " + strings.Join(compactDetails, " · ")
			if runewidth.StringWidth(info+" · "+btopSpan(plot)) <= inside.Dx() {
				info += " · " + btopSpan(plot)
			}
		}
		btopText(buf, inside, inside.Max.Y-1, info, htopPalette().text)
		inside.Max.Y--
	}
	drawBtopPlot(buf, inside, plot, color)
}

var btopMemory MemoryMetrics

func recordBtopMemory(metrics MemoryMetrics, at time.Time) {
	btopMemory = metrics
	valid := metrics.Total > 0 && metrics.Used <= metrics.Total
	percent := 0.0
	if valid {
		percent = 100 * float64(metrics.Used) / float64(metrics.Total)
	}
	btopData.history("memory").add(at, percent, valid, fmt.Sprintf("physical:%d", metrics.Total))
}

func btopMemoryLabel() string {
	if btopMemory.Total == 0 || btopMemory.Used > btopMemory.Total {
		return "—"
	}
	return fmt.Sprintf("%.1f/%.1f GB · swap %.1f", float64(btopMemory.Used)/1e9, float64(btopMemory.Total)/1e9, float64(btopMemory.SwapUsed)/1e9)
}

func drawBtopRateHistory(buf *ui.Buffer, area image.Rectangle, key, label, unit string, color ui.Color) {
	if area.Empty() {
		return
	}
	chart := area
	legendRect := area
	if area.Dy() >= 2 {
		chart.Min.Y++
	} else {
		// Keep the auto-scale readable even when only one graph row fits.
		legendWidth := min(max(0, area.Dx()-2), max(14, area.Dx()/2))
		legendRect.Max.X = legendRect.Min.X + legendWidth
		chart.Min.X = min(chart.Max.X, legendRect.Max.X+1)
	}
	plot := btopWindow(btopData.history(key), chart.Dx(), 0)
	legend := label + " 0–" + formatBytes(plot.ceiling, unit)
	if area.Dy() == 1 && runewidth.StringWidth(legend) > legendRect.Dx() {
		legend = label + " 0–" + formatBytes(plot.ceiling, "auto")
	}
	if area.Dy() >= 2 {
		span := " · " + btopSpan(plot)
		if runewidth.StringWidth(legend+span) <= area.Dx() {
			legend += span
		}
	}
	btopText(buf, legendRect, area.Min.Y, legend, color)
	drawBtopPlot(buf, chart, plot, color)
}

// The secondary row contains real histories, even at the 80x24 minimum.
// Network and disk directions have independent, explicitly labelled axes.
func drawBtopResourceHistories(buf *ui.Buffer, rect image.Rectangle) {
	p := htopPalette()
	for i, title := range []string{i18n.T("Btop_Memory"), i18n.T("Btop_Network"), i18n.T("Btop_Disk")} {
		r := image.Rect(rect.Min.X+i*rect.Dx()/3, rect.Min.Y, rect.Min.X+(i+1)*rect.Dx()/3, rect.Max.Y)
		color := p.memory
		label := btopMemoryLabel()
		if i == 0 && rect.Dy() < 6 {
			title += " · 0–100%"
		}
		if i == 1 {
			color, label = p.network, btopNetworkRates(lastNetDiskMetrics)
		} else if i == 2 {
			color, label = p.network, btopDiskRates(lastNetDiskMetrics)
		}
		inside := btopBox(buf, r, title, color)
		btopText(buf, inside, inside.Min.Y, label, p.text)
		inside.Min.Y++
		if inside.Empty() {
			continue
		}
		if i == 0 {
			plot := btopWindow(btopData.history("memory"), inside.Dx(), 100)
			if inside.Dy() >= 3 {
				btopText(buf, inside, inside.Max.Y-1, "0–100% · "+btopSpan(plot), p.text)
				inside.Max.Y--
			}
			drawBtopPlot(buf, inside, plot, color)
			continue
		}
		keys, labels, unit := []string{"rx", "tx"}, []string{"RX", "TX"}, networkUnit
		if i == 2 {
			keys, labels, unit = []string{"disk_r", "disk_w"}, []string{"R", "W"}, diskUnit
		}
		mid := inside.Min.Y + inside.Dy()/2
		drawBtopRateHistory(buf, image.Rect(inside.Min.X, inside.Min.Y, inside.Max.X, mid), keys[0], labels[0], unit, color)
		drawBtopRateHistory(buf, image.Rect(inside.Min.X, mid, inside.Max.X, inside.Max.Y), keys[1], labels[1], unit, color)
	}
}

func btopCPUUsageValid(m CPUMetrics) bool {
	if len(m.CoreUsages) == 0 || len(m.CoreBreakdown) != len(m.CoreUsages) || math.IsNaN(m.AvgUsage) || math.IsInf(m.AvgUsage, 0) || m.AvgUsage < 0 || m.AvgUsage > 100 {
		return false
	}
	for i, value := range m.CoreUsages {
		if !m.CoreBreakdown[i].Valid || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return false
		}
	}
	return true
}

func btopCPUTitle() string {
	usage := "—"
	if btopCPUUsageValid(lastCPUMetrics) {
		usage = fmt.Sprintf("%.1f%%", lastCPUMetrics.AvgUsage)
	}
	return "CPU " + usage + " · " + formatCPUFreq(lastCPUMetrics)
}

func (d *btopDashboard) Draw(buf *ui.Buffer) {
	area := d.Rectangle
	p := htopPalette()
	layout := effectiveLayout()
	options := btopProcessOptions()
	if layout == LayoutProcesses {
		v := &btopProcessArea{ui.NewBlock()}
		v.SetRect(area.Min.X, area.Min.Y, area.Max.X, area.Max.Y)
		v.Draw(buf)
		return
	}
	topHeight := 0
	if options.Mode != "hidden" {
		topHeight = min(options.Count+3, max(6, area.Dy()/3))
	}
	minimumGraphs := 9 // CPU/GPU: graph and axis; resources: both rate directions.
	if layout == LayoutEnergy {
		minimumGraphs = 4
	} else if layout == LayoutNetworkIO {
		minimumGraphs = 8
	}
	topHeight = min(topHeight, max(0, area.Dy()-minimumGraphs))
	graphs := area
	graphs.Max.Y -= topHeight
	summaryHeight := max(5, graphs.Dy()*2/5)
	if layout == LayoutEnergy {
		summaryHeight = 3
		if graphs.Dy() < 6 {
			summaryHeight = 0
			if graphs.Dy() >= 4 {
				summaryHeight = 1
			}
		}
	} else if layout == LayoutNetworkIO {
		// At minimum size the four dedicated rate plots need all eight rows.
		// Each of the two chart rows needs borders, a plot and its axis.
		// Add the five-row resource section only after reserving all eight.
		if graphs.Dy() < 13 {
			summaryHeight = 0
		} else {
			summaryHeight = min(summaryHeight, graphs.Dy()-8)
		}
	}
	charts := graphs
	charts.Max.Y -= summaryHeight
	if layout == LayoutNetworkIO {
		for i, key := range []string{"rx", "tx", "disk_r", "disk_w"} {
			r := image.Rect(charts.Min.X+(i%2)*charts.Dx()/2, charts.Min.Y+(i/2)*charts.Dy()/2, charts.Min.X+(i%2+1)*charts.Dx()/2, charts.Min.Y+(i/2+1)*charts.Dy()/2)
			title := map[string]string{"rx": "RX", "tx": "TX", "disk_r": i18n.T("Btop_Disk") + " R", "disk_w": i18n.T("Btop_Disk") + " W"}[key]
			drawBtopChart(buf, r, title, key, 0, p.network)
		}
	} else if layout == LayoutEnergy {
		mid := charts.Min.X + charts.Dx()/2
		drawBtopChart(buf, image.Rect(charts.Min.X, charts.Min.Y, mid, charts.Max.Y), "CPU · "+btopWatts(btopData.power.CPU), "cpu_w", 0, p.user)
		drawBtopChart(buf, image.Rect(mid, charts.Min.Y, charts.Max.X, charts.Max.Y), "GPU · "+btopWatts(btopData.power.gpu()), "gpu_w", 0, p.gpu)
	} else if layout == LayoutGPUFocus {
		ane := aneGaugeInnerLabel(lastCPUMetrics, aneUtilizationPercent(lastCPUMetrics), aneBWLabelMode(lastCPUMetrics))
		if ane == "" {
			ane = btopWatts(btopData.power.ANE)
		}
		detail := fmt.Sprintf("DRAM R %.1f W %.1f GB/s · ANE %s", lastCPUMetrics.DRAMReadBW, lastCPUMetrics.DRAMWriteBW, ane)
		drawBtopChart(buf, charts, btopGPUTitle(), "gpu", 100, p.gpu, detail)
	} else {
		mid := charts.Min.X + charts.Dx()/2
		drawBtopChart(buf, image.Rect(charts.Min.X, charts.Min.Y, mid, charts.Max.Y), btopCPUTitle(), "cpu", 100, p.user)
		drawBtopChart(buf, image.Rect(mid, charts.Min.Y, charts.Max.X, charts.Max.Y), btopGPUTitle(), "gpu", 100, p.gpu)
	}
	if summaryHeight > 0 {
		rect := image.Rect(area.Min.X, charts.Max.Y, area.Max.X, graphs.Max.Y)
		if layout == LayoutEnergy {
			inside := rect
			if summaryHeight >= 3 {
				inside = btopBox(buf, rect, i18n.T("Btop_Sources"), p.power)
			}
			btopText(buf, inside, inside.Min.Y, i18n.T("Btop_ExternalUnavailable"), p.text)
		} else {
			drawBtopResourceHistories(buf, rect)
		}
	}
	if topHeight > 0 {
		v := &btopProcessArea{ui.NewBlock()}
		v.SetRect(area.Min.X, graphs.Max.Y, area.Max.X, area.Max.Y)
		v.Draw(buf)
	}
}

func btopGPUTitle() string {
	usage := "—"
	temperature := "—"
	frequency := "—"
	if lastGPUMetrics.FreqMHz > 0 {
		frequency = fmt.Sprintf("%dMHz", lastGPUMetrics.FreqMHz)
	}
	if lastGPUMetrics.Temp > 0 {
		temperature = formatTemp(float64(lastGPUMetrics.Temp))
	}
	if lastGPUMetrics.Valid {
		usage = fmt.Sprintf("%.1f%%", lastGPUMetrics.ActivePercent)
	}
	return fmt.Sprintf("GPU %s · %s · %s · %s", usage, frequency, temperature, btopWatts(btopData.power.gpu()))
}
