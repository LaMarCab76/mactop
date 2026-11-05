package app

import (
	"fmt"
	"image"
	"math"
	"strings"

	"github.com/mattn/go-runewidth"
	ui "github.com/metaspartan/gotui/v5"
	w "github.com/metaspartan/gotui/v5/widgets"
)

var meterValues = map[*w.Gauge]float64{}

func htopBlockTitleWidth(block *ui.Block) int {
	if grid != nil {
		for _, item := range grid.Items {
			var source *ui.Block
			switch v := item.Entry.(type) {
			case *w.Gauge:
				source = &v.Block
			case *w.StepChart:
				source = &v.Block
			case *w.Paragraph:
				source = &v.Block
			case *w.List:
				source = &v.Block
			case *w.SparklineGroup:
				source = &v.Block
			case *CPUCoreWidget:
				source = v.Block
			}
			if source == block {
				return max(0, int(float64(grid.Dx())*(item.XRatio+item.WidthRatio))-int(float64(grid.Dx())*item.XRatio)-4)
			}
		}
	}
	return max(0, block.Dx()-4)
}

func meterGlyph() rune {
	if htopConfig().BarStyle == "classic" {
		return '|'
	}
	if runewidth.RuneWidth('▪') != 1 {
		return '#'
	}
	if screen := ui.DefaultBackend.Screen; screen != nil {
		charset := strings.ToUpper(strings.ReplaceAll(screen.CharacterSet(), "-", ""))
		if charset != "UTF8" {
			return '#'
		}
	}
	return '▪'
}

func meterColor(color ui.Color, position, capacity int) ui.Color {
	if htopConfig().BarStyle != "gradient_blocks" {
		return color
	}
	r, g, b := color.RGB()
	if r < 0 || g < 0 || b < 0 {
		return color
	}
	intensity := .55 + .45*float64(max(0, position))/float64(max(1, capacity-1))
	r, g, b = int32(float64(r)*intensity), int32(float64(g)*intensity), int32(float64(b)*intensity)
	colors := 1 << 24
	if screen := ui.DefaultBackend.Screen; screen != nil {
		colors = screen.Colors()
	}
	if colors < 256 {
		return color
	}
	if colors < 1<<24 {
		// Quantize to the terminal's 6x6x6 color cube.
		level := func(v int32) int {
			if v < 48 {
				return 0
			}
			if v < 115 {
				return 1
			}
			return min(5, int((v-35)/40))
		}
		return ui.Color(16 + 36*level(r) + 6*level(g) + level(b))
	}
	return ui.NewRGBColor(r, g, b)
}

type htopGauge struct{ *w.Gauge }

func (g *htopGauge) Draw(buf *ui.Buffer) {
	if g.Dx() <= 0 || g.Dy() <= 0 {
		return
	}
	g.Block.Draw(buf)
	area := g.Inner
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return
	}
	p := htopPalette()
	bg := htopEffectiveBg()
	buf.Fill(ui.NewCell(' ', ui.NewStyle(p.text, bg)), area)
	percent, ok := meterValues[g.Gauge]
	if !ok {
		percent = float64(g.Percent)
	}
	percent = finitePercent(percent)
	label := g.Label
	if label == "" {
		label = fmt.Sprintf("%.1f%%", percent)
	}
	label = runewidth.Truncate(label, area.Dx(), "")
	labelWidth := runewidth.StringWidth(label)
	barWidth := max(0, area.Dx()-labelWidth-3)
	y := area.Min.Y + (area.Dy()-1)/2
	if barWidth > 0 {
		buf.SetCell(ui.NewCell('[', ui.NewStyle(p.text, bg)), image.Pt(area.Min.X, y))
		fill := int(math.Round(percent / 100 * float64(barWidth)))
		for x := 0; x < barWidth; x++ {
			ch := ' '
			color := p.text
			if x < fill {
				ch = meterGlyph()
				color = meterColor(g.BarColor, x, barWidth)
			}
			buf.SetCell(ui.NewCell(ch, ui.NewStyle(color, bg)), image.Pt(area.Min.X+1+x, y))
		}
		buf.SetCell(ui.NewCell(']', ui.NewStyle(p.text, bg)), image.Pt(area.Min.X+1+barWidth, y))
	}
	buf.SetString(label, ui.NewStyle(p.text, bg), image.Pt(area.Max.X-labelWidth, y))
}

type htopHistory struct{ *w.StepChart }

type htopSparklines struct{ *w.SparklineGroup }

func (g *htopSparklines) Draw(buf *ui.Buffer) {
	if g.Dx() <= 0 || g.Dy() <= 0 {
		return
	}
	g.Block.Draw(buf)
	area := g.Inner
	if area.Dx() <= 0 || area.Dy() <= 0 || len(g.Sparklines) == 0 {
		return
	}
	bg := htopEffectiveBg()
	buf.Fill(ui.NewCell(' ', ui.NewStyle(htopPalette().text, bg)), area)
	for i, line := range g.Sparklines {
		if line == nil {
			continue
		}
		top, bottom := area.Min.Y+i*area.Dy()/len(g.Sparklines), area.Min.Y+(i+1)*area.Dy()/len(g.Sparklines)
		if bottom <= top {
			continue
		}
		if line.Title != "" {
			buf.SetString(runewidth.Truncate(line.Title, area.Dx(), ""), ui.NewStyle(line.LineColor, bg), image.Pt(area.Min.X, top))
			top++
		}
		height := bottom - top
		if height <= 0 {
			continue
		}
		maxValue := line.MaxVal
		if maxValue <= 0 || math.IsNaN(maxValue) || math.IsInf(maxValue, 0) {
			maxValue = 1
			for _, v := range line.Data {
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					maxValue = math.Max(maxValue, v)
				}
			}
		}
		start := max(0, len(line.Data)-area.Dx())
		for j := start; j < len(line.Data); j++ {
			v := line.Data[j]
			if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
				continue
			}
			filled := min(height, int(math.Round(math.Min(1, v/maxValue)*float64(height))))
			if line.MaxHeight > 0 {
				filled = min(filled, line.MaxHeight)
			}
			for level := 0; level < filled; level++ {
				buf.SetCell(ui.NewCell(meterGlyph(), ui.NewStyle(meterColor(line.LineColor, level, height), bg)), image.Pt(area.Min.X+j-start, bottom-1-level))
			}
		}
	}
}

// Blocks are an alternate projection of the same samples, axes and scales.
// Series overlap, they are never added into an invented aggregate.
func (c *htopHistory) Draw(buf *ui.Buffer) {
	if c.Dx() <= 0 || c.Dy() <= 0 {
		return
	}
	c.Block.Draw(buf)
	area := c.Inner
	if area.Dx() <= 0 || area.Dy() <= 0 {
		return
	}
	p := htopPalette()
	bg := htopEffectiveBg()
	buf.Fill(ui.NewCell(' ', ui.NewStyle(p.text, bg)), area)
	maxValue := c.MaxVal
	if maxValue <= 0 || math.IsNaN(maxValue) || math.IsInf(maxValue, 0) {
		maxValue = 1
		for _, series := range c.Data {
			for _, v := range series {
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					maxValue = math.Max(maxValue, v)
				}
			}
		}
	}
	plot := area
	axisStyle := ui.NewStyle(p.text, bg)
	if c.ShowAxes && area.Dx() > 6 && area.Dy() > 3 {
		plot = image.Rect(area.Min.X+5, area.Min.Y, area.Max.X, area.Max.Y-2)
		for y := plot.Min.Y; y <= plot.Max.Y; y++ {
			buf.SetCell(ui.NewCell('│', axisStyle), image.Pt(plot.Min.X-1, y))
		}
		for x := plot.Min.X - 1; x < plot.Max.X; x++ {
			buf.SetCell(ui.NewCell('─', axisStyle), image.Pt(x, plot.Max.Y))
		}
		for y := plot.Max.Y - 1; y >= plot.Min.Y; y -= 2 {
			value := maxValue * float64(plot.Max.Y-1-y) / float64(max(1, plot.Dy()-1))
			label := runewidth.Truncate(fmt.Sprintf("%.2f", value), 4, "")
			buf.SetString(label, axisStyle, image.Pt(area.Min.X, y))
		}
		for x := 0; x < plot.Dx(); x += 5 {
			label := runewidth.Truncate(fmt.Sprint(x/max(1, c.HorizontalScale)), plot.Dx()-x, "")
			buf.SetString(label, axisStyle, image.Pt(plot.Min.X+x, area.Max.Y-1))
		}
	}
	if plot.Dx() <= 0 || plot.Dy() <= 0 {
		return
	}
	scale := max(1, c.HorizontalScale)
	for i := len(c.Data) - 1; i >= 0; i-- {
		series := c.Data[i]
		color := p.user
		if len(c.LineColors) > 0 {
			color = c.LineColors[i%len(c.LineColors)]
		}
		for j, v := range series {
			if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
				continue
			}
			left := j * scale
			if left >= plot.Dx() {
				break
			}
			height := min(plot.Dy(), int(math.Round(math.Min(1, v/maxValue)*float64(plot.Dy()))))
			for x := left; x < min(plot.Dx(), left+scale); x++ {
				for level := 0; level < height; level++ {
					buf.SetCell(ui.NewCell(meterGlyph(), ui.NewStyle(meterColor(color, level, plot.Dy()), bg)), image.Pt(plot.Min.X+x, plot.Max.Y-1-level))
				}
			}
		}
	}
	// Keep every series label legible in a reserved row at the top of the plot.
	labelX := plot.Min.X
	for i, label := range c.DataLabels {
		if labelX >= plot.Max.X {
			break
		}
		label = runewidth.Truncate(label, plot.Max.X-labelX, "")
		color := p.text
		if len(c.LineColors) > 0 {
			color = c.LineColors[i%len(c.LineColors)]
		}
		buf.SetString(label, ui.NewStyle(color, bg), image.Pt(labelX, plot.Min.Y))
		labelX += runewidth.StringWidth(label) + 2
	}
}

func decorateHtopMeters(restore *[]func()) {
	if grid == nil {
		return
	}
	for _, item := range grid.Items {
		switch source := item.Entry.(type) {
		case *w.List:
			if source == processList && !isPortsLayoutActive() {
				replaceUIValue(restore, &item.Entry, any(&btopProcessArea{&source.Block}))
			}
		case *w.Gauge:
			replaceUIValue(restore, &item.Entry, any(&htopGauge{source}))
		case *w.StepChart:
			if htopConfig().BarStyle != "classic" {
				replaceUIValue(restore, &item.Entry, any(&htopHistory{source}))
			}
		case *w.SparklineGroup:
			if htopConfig().BarStyle != "classic" {
				replaceUIValue(restore, &item.Entry, any(&htopSparklines{source}))
			}
		}
	}
}
