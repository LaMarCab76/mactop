package app

import (
	"fmt"
	"image"
	"math"

	"github.com/mattn/go-runewidth"
	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

const maxTopologyRetries = 3

func (w *CPUCoreWidget) shouldRefreshTopology(cpuCount int) bool {
	if cpuCount <= 0 {
		return false
	}
	if cpuCount != len(w.cores) {
		w.topologyRetries = maxTopologyRetries
	}
	if w.topologyRetries <= 0 {
		return false
	}
	w.topologyRetries--
	return true
}

// UpdateSample remaps totals and components together. A missing breakdown
// clears the previous one rather than displaying stale user/system time.
func (w *CPUCoreWidget) UpdateSample(usage []float64, parts []CPUTimePercent) {
	if w.shouldRefreshTopology(len(usage)) {
		labels, e, p, s, indices := BuildCoreLabels()
		if len(labels) == len(usage) && len(indices) == len(usage) {
			w.labels, w.cpuIndexMap = labels, indices
			w.eCoreCount, w.pCoreCount, w.sCoreCount = e, p, s
			w.topologyRetries = 0
		} else {
			// A topology snapshot may lag a changed Mach sample. Use explicit
			// CPU indices during bounded retries; do not guess E/P/S labels.
			w.labels, w.cpuIndexMap = make([]string, len(usage)), make([]int, len(usage))
			w.eCoreCount, w.pCoreCount, w.sCoreCount = 0, 0, 0
			for i := range usage {
				w.labels[i], w.cpuIndexMap[i] = fmt.Sprint(i), i
			}
		}
	}
	w.breakdown = nil
	if len(usage) == 0 {
		// Preserve Original's last known totals. htop ignores those totals
		// without a valid breakdown and draws --, never stale segments.
		return
	}
	w.UpdateUsage(usage)
	if len(parts) != len(usage) {
		return
	}
	w.breakdown = make([]CPUTimePercent, len(w.cores))
	for i := range w.cores {
		idx := i
		if len(w.cpuIndexMap) > i {
			idx = w.cpuIndexMap[i]
		}
		if idx >= 0 && idx < len(parts) {
			w.breakdown[i] = parts[idx]
		}
	}
}

// Boundaries use cumulative rounding so tiny bars never overflow and all
// segments together occupy exactly the rounded active fraction.
func cpuSegmentEnds(parts CPUTimePercent, width int) [3]int {
	var ends [3]int
	if width <= 0 || !parts.Valid {
		return ends
	}
	values := []float64{finitePercent(parts.Nice), finitePercent(parts.User), finitePercent(parts.System)}
	total := values[0] + values[1] + values[2]
	scale := 1.0
	if total > 100 {
		scale = 100 / total
	}
	accumulated := 0.0
	for i, value := range values {
		accumulated += value * scale
		ends[i] = min(width, int(math.Round(accumulated/100*float64(width))))
	}
	return ends
}

func htopCoreColumns(width int) int { return max(1, min(4, width/22)) }

func (w *CPUCoreWidget) drawHtop(buf *ui.Buffer) {
	width, height := w.Inner.Dx(), w.Inner.Dy()
	if width <= 0 || height <= 0 {
		return
	}
	buf.Fill(ui.NewCell(' ', ui.NewStyle(htopPalette().text, CurrentBgColor)), w.Inner)
	cols := htopCoreColumns(width)
	rows := height
	omitted := len(w.cores) > cols*rows
	if omitted {
		rows--
	}
	visible := min(len(w.cores), cols*rows)
	for i := 0; i < visible; i++ {
		col, row := i/rows, i%rows
		x := w.Inner.Min.X + col*width/cols
		colWidth := (col+1)*width/cols - col*width/cols
		w.drawHtopCore(buf, x, w.Inner.Min.Y+row, colWidth, i)
	}
	if omitted {
		text := fmt.Sprintf(i18n.T("TUI_CoresOmitted"), len(w.cores)-visible)
		buf.SetString(runewidth.Truncate(text, width, ""), ui.NewStyle(htopPalette().text, CurrentBgColor), image.Pt(w.Inner.Min.X, w.Inner.Max.Y-1))
	}
}

func (w *CPUCoreWidget) drawHtopCore(buf *ui.Buffer, x, y, width, index int) {
	p := htopPalette()
	label := fmt.Sprint(index)
	if index < len(w.labels) {
		label = w.labels[index]
	}
	part := CPUTimePercent{}
	if index < len(w.breakdown) {
		part = w.breakdown[index]
	}
	percent := "   --"
	if part.Valid {
		percent = fmt.Sprintf("%4.0f%%", finitePercent(w.cores[index]))
	}
	labelWidth := max(3, runewidth.StringWidth(label))
	if width < labelWidth+10 {
		// A narrow terminal still gets a label and a percentage, without
		// writing through a neighbour's column or into the border.
		text := label + " " + percent
		buf.SetString(runewidth.Truncate(text, width, ""), ui.NewStyle(p.text, CurrentBgColor), image.Pt(x, y))
		return
	}
	buf.SetString(fmt.Sprintf("%-*s[", labelWidth, label), ui.NewStyle(p.text, CurrentBgColor), image.Pt(x, y))
	barWidth := width - labelWidth - 9 // brackets + space + percentage + column gap
	ends := cpuSegmentEnds(part, barWidth)
	colors := []ui.Color{p.nice, p.user, p.system}
	for i := 0; i < barWidth; i++ {
		color, char := p.text, ' '
		for s, end := range ends {
			if i < end {
				color, char = meterColor(colors[s], i, barWidth), meterGlyph()
				break
			}
		}
		buf.SetCell(ui.NewCell(char, ui.NewStyle(color, CurrentBgColor)), image.Pt(x+labelWidth+1+i, y))
	}
	buf.SetString("] "+percent, ui.NewStyle(p.text, CurrentBgColor), image.Pt(x+labelWidth+1+barWidth, y))
}
