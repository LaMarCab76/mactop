package app

import (
	"fmt"

	"github.com/mattn/go-runewidth"
	ui "github.com/metaspartan/gotui/v5"
	w "github.com/metaspartan/gotui/v5/widgets"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

const (
	UIModeOriginal = "original"
	UIModeHtop     = "htop"
)

// Runtime mode is separate from the saved preference: a CLI override must
// not leak into config.json when another setting is saved.
var activeUIMode, cliUIMode string

func resolveUIMode(saved, override string) (string, error) {
	mode := UIModeOriginal
	if saved == UIModeHtop {
		mode = saved
	}
	if override != "" {
		if override != UIModeOriginal && override != UIModeHtop {
			return "", fmt.Errorf(i18n.T("CLI_InvalidUIMode"), override)
		}
		mode = override
	}
	return mode, nil
}

func isHtopMode() bool { return activeUIMode == UIModeHtop }

func uiModeName() string {
	if isHtopMode() {
		return "htop"
	}
	return i18n.T("TUI_ModeOriginal")
}

// Caller holds renderMutex, as with the other presentation updates.
func setUIMode(mode string) {
	changeModeLayout(mode)
	currentConfig.UIMode = mode
	if !isHtopMode() {
		applyResolvedOriginalAppearance()
	}
	if !showHelp {
		applyLayout(effectiveLayout())
	}
	width, _ := GetCachedTerminalDimensions()
	updateLayoutFooter(width)
	updateHelpText()
	updateProcessList() // Rebuild embedded colors, including while frozen.
	saveConfig()
}

func toggleUIMode() {
	renderMutex.Lock()
	defer renderMutex.Unlock()
	mode := UIModeHtop
	if isHtopMode() {
		mode = UIModeOriginal
	}
	setUIMode(mode)
	w, h := GetCachedTerminalDimensions()
	drawScreen(w, h)
}

func updateLayoutFooter(width int) {
	if mainBlock == nil {
		return
	}
	mainBlock.TitleBottomLeft = fmt.Sprintf(i18n.T("TUI_LayoutInfo"), currentLayoutNum+1, totalLayouts, currentColorName)
	if isHtopMode() {
		mainBlock.TitleBottomLeft = " htop | v: " + i18n.T("TUI_ModeOriginal") + " "
	}
	mainBlock.TitleBottom = ""
	if width >= 93 {
		mainBlock.TitleBottom = i18n.T("TUI_InfoLayoutColorExit")
	}
}

type htopColors struct {
	nice, user, system, gpu, ane, power, text                             ui.Color
	memory, bandwidth, network, thunderbolt, systemInfo, selected, border ui.Color
	greenHex, headerFg, textHex, dimHex                                   string
}

func htopLightBackground() bool {
	bg := htopEffectiveBg()
	if bg != ui.ColorClear {
		r, g, b := bg.RGB()
		if r >= 0 && g >= 0 && b >= 0 {
			return 0.299*float64(r)+0.587*float64(g)+0.114*float64(b) > 127.5
		}
	}
	return IsLightMode
}

func defaultHtopPalette() htopColors {
	if htopLightBackground() {
		return htopColors{
			nice: ui.NewRGBColor(0, 70, 190), user: ui.NewRGBColor(0, 110, 0), system: ui.NewRGBColor(180, 30, 30),
			gpu: ui.NewRGBColor(0, 110, 140), ane: ui.NewRGBColor(140, 0, 140), power: ui.NewRGBColor(140, 100, 0),
			text: ui.ColorBlack, greenHex: "#006e00", headerFg: "white", textHex: "#000000", dimHex: "#555555",
		}
	}
	return htopColors{
		nice: ui.ColorBlue, user: ui.ColorGreen, system: ui.ColorRed,
		gpu: ui.ColorCyan, ane: ui.ColorMagenta, power: ui.ColorYellow,
		text: ui.ColorWhite, greenHex: "green", headerFg: "black", textHex: "white", dimHex: "#aaaaaa",
	}
}

func htopPalette() htopColors {
	p := defaultHtopPalette()
	p.border = p.user
	p.memory, p.bandwidth, p.network, p.thunderbolt, p.systemInfo, p.selected = p.power, p.gpu, p.gpu, p.gpu, p.text, ui.NewRGBColor(0, 0, 0)
	if custom := htopConfig().Colors; custom != nil {
		fields := []struct {
			value string
			dst   *ui.Color
		}{
			{custom.Foreground, &p.text}, {custom.Foreground, &p.border}, {custom.CPU, &p.user}, {custom.GPU, &p.gpu}, {custom.Memory, &p.memory}, {custom.ANE, &p.ane}, {custom.Bandwidth, &p.bandwidth}, {custom.Network, &p.network}, {custom.Power, &p.power}, {custom.Thunderbolt, &p.thunderbolt}, {custom.SystemInfo, &p.systemInfo}, {custom.CPUNice, &p.nice}, {custom.CPUSystem, &p.system}, {custom.ProcessListSelected, &p.selected},
		}
		for _, field := range fields {
			if color, err := ParseHexColor(field.value); err == nil {
				*field.dst = color
			}
		}
		if IsHexColor(custom.ProcessList) {
			p.greenHex = custom.ProcessList
		}
		if IsHexColor(custom.ProcessListDim) {
			p.dimHex = custom.ProcessListDim
		}
	}
	if cliFgColor != "" {
		p.text = GetThemeColor(cliFgColor)
		p.border = p.text
	}
	p.textHex = colorMarkup(p.text)
	p.headerFg = "black"
	if color, e := ParseHexColor(p.greenHex); e == nil {
		r, g, b := color.RGB()
		if .299*float64(r)+.587*float64(g)+.114*float64(b) < 128 {
			p.headerFg = "white"
		}
	}
	return p
}

func colorMarkup(c ui.Color) string {
	r, g, b := c.RGB()
	if r < 0 {
		return "white"
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// Temporarily override only presentation fields. Restoring them after drawing
// preserves the exact original preset/custom/per-tick styles, including chart
// series colors. No theme configuration or sampling state is modified.
func replaceUIValue[T any](restore *[]func(), field *T, value T) {
	old := *field
	*restore = append(*restore, func() { *field = old })
	*field = value
}

// gotui skips unset buffer cells. A space layer keeps a changing htop layout
// from retaining text from an earlier layout, even with a transparent backdrop.
type uiModeBackground struct{ *ui.Block }

func (b *uiModeBackground) Draw(buf *ui.Buffer) {
	buf.Fill(ui.NewCell(' ', ui.NewStyle(ui.ColorClear, effectiveBackground())), b.Rectangle)
}

func htopTitleWidth(widget ui.Drawable) int {
	// Grid updates child rectangles when it draws. Use the current grid ratios
	// so the first frame after a resize clips titles to the new dimensions.
	if grid != nil {
		for _, item := range grid.Items {
			if item.Entry == widget {
				left := int(float64(grid.Dx()) * item.XRatio)
				right := int(float64(grid.Dx()) * (item.XRatio + item.WidthRatio))
				return max(0, right-left-4)
			}
		}
	}
	return max(0, widget.GetRect().Dx()-4)
}

// All content rendering goes through this helper with renderMutex held.
// The loading screen precedes widget setup and intentionally has its own path.
func renderWithUIMode(draw func()) {
	if !isHtopMode() {
		draw()
		return
	}
	var restore []func()
	defer func() {
		for i := len(restore) - 1; i >= 0; i-- {
			restore[i]()
		}
	}()
	replaceUIValue(&restore, &CurrentBgColor, htopEffectiveBg())
	replaceUIValue(&restore, &ui.Theme.Default.Bg, CurrentBgColor)
	p := htopPalette()
	block := func(b *ui.Block, color ui.Color) {
		if b == nil {
			return
		}
		replaceUIValue(&restore, &b.BackgroundColor, CurrentBgColor)
		width := htopBlockTitleWidth(b)
		replaceUIValue(&restore, &b.Title, runewidth.Truncate(b.Title, width, ""))
		replaceUIValue(&restore, &b.TitleRight, runewidth.Truncate(b.TitleRight, width, ""))
		replaceUIValue(&restore, &b.TitleBottomLeft, runewidth.Truncate(b.TitleBottomLeft, width, ""))
		replaceUIValue(&restore, &b.TitleBottomRight, runewidth.Truncate(b.TitleBottomRight, width, ""))
		replaceUIValue(&restore, &b.TitleBottom, runewidth.Truncate(b.TitleBottom, width, ""))
		replaceUIValue(&restore, &b.BorderStyle, ui.NewStyle(color, CurrentBgColor))
		replaceUIValue(&restore, &b.TitleStyle, ui.NewStyle(color, CurrentBgColor))
		replaceUIValue(&restore, &b.TitleBottomStyle, ui.NewStyle(color, CurrentBgColor))
	}
	gauge := func(g *w.Gauge, color ui.Color) {
		if g != nil {
			block(&g.Block, color)
			replaceUIValue(&restore, &g.Title, runewidth.Truncate(g.Title, htopTitleWidth(g), "…"))
			replaceUIValue(&restore, &g.BarColor, color)
			replaceUIValue(&restore, &g.LabelStyle, ui.NewStyle(p.text, CurrentBgColor))
		}
	}
	paragraph := func(v *w.Paragraph, color ui.Color) {
		if v != nil {
			block(&v.Block, color)
			replaceUIValue(&restore, &v.TextStyle, ui.NewStyle(color, CurrentBgColor))
		}
	}
	chart := func(c *w.StepChart, colors ...ui.Color) {
		if c != nil {
			block(&c.Block, colors[0])
			series := make([]ui.Color, max(len(c.Data), len(c.LineColors), 1))
			for i := range series {
				series[i] = colors[i%len(colors)]
			}
			replaceUIValue(&restore, &c.LineColors, series)
		}
	}
	gauge(cpuGauge, p.user)
	gauge(gpuGauge, p.gpu)
	gauge(memoryGauge, p.memory)
	gauge(aneGauge, p.ane)
	// Pressure warning/critical colors are meaningful and keep their own style.
	if cpuCoreWidget != nil {
		block(cpuCoreWidget.Block, p.user)
		replaceUIValue(&restore, &cpuCoreWidget.Title, runewidth.Truncate(cpuCoreWidget.Title, htopTitleWidth(cpuCoreWidget), "…"))
		replaceUIValue(&restore, &cpuCoreWidget.TitleBottom, runewidth.Truncate(i18n.T("TUI_CPULegend"), htopTitleWidth(cpuCoreWidget), "…"))
	}
	paragraph(NetworkInfo, p.network)
	paragraph(PowerChart, p.power)
	paragraph(modelText, p.systemInfo)
	paragraph(helpText, p.text)
	paragraph(infoParagraph, p.text)
	paragraph(fanStatusPanel, p.text)
	paragraph(fanTempPanel, p.text)
	paragraph(fanControlPanel, p.text)
	block(mainBlock, p.border)
	if confirmModal != nil {
		block(&confirmModal.Block, p.user)
		modalBg := CurrentBgColor
		if modalBg == ui.ColorClear {
			modalBg = ui.NewRGBColor(0, 0, 0)
		}
		replaceUIValue(&restore, &confirmModal.BackgroundColor, modalBg)
	}
	paragraph(tbInfoParagraph, p.thunderbolt)
	chart(cpuHistoryChart, p.user)
	chart(gpuHistoryChart, p.gpu)
	chart(memoryHistoryChart, p.memory, p.ane)
	chart(powerHistoryChart, p.power)
	chart(aneHistoryChart, p.ane)
	chart(memBWHistoryChart, p.bandwidth, p.power)
	if effectiveLayout() == LayoutHistorySoC {
		chart(bandwidthHistoryChart, p.bandwidth, p.nice, p.system, p.ane, p.power)
	} else {
		chart(bandwidthHistoryChart, p.bandwidth, p.power, p.ane, p.nice)
	}
	chart(socPowerHistoryChart, p.user, p.gpu, p.power, p.ane)
	chart(ssdReadHistoryChart, p.gpu)
	for _, item := range []struct {
		spark *w.Sparkline
		color ui.Color
	}{
		{sparkline, p.power}, {gpuSparkline, p.gpu}, {tbNetSparklineIn, p.gpu}, {tbNetSparklineOut, p.ane},
	} {
		if item.spark != nil {
			replaceUIValue(&restore, &item.spark.LineColor, item.color)
			replaceUIValue(&restore, &item.spark.TitleStyle, ui.NewStyle(item.color, CurrentBgColor))
		}
	}
	for _, item := range []struct {
		group *w.SparklineGroup
		color ui.Color
	}{
		{sparklineGroup, p.power}, {gpuSparklineGroup, p.gpu}, {tbNetSparklineGroup, p.gpu},
	} {
		if item.group != nil {
			block(&item.group.Block, item.color)
		}
	}
	if processList != nil {
		// Keep kill/search/frozen title styles supplied by their own resolvers.
		replaceUIValue(&restore, &processList.BorderStyle, ui.NewStyle(p.user, CurrentBgColor))
		replaceUIValue(&restore, &processList.TextStyle, ui.NewStyle(p.text, CurrentBgColor))
		replaceUIValue(&restore, &processList.SelectedStyle, ui.NewStyle(p.selected, ui.ColorCyan))
	}
	decorateHtopMeters(&restore)
	draw()
}
