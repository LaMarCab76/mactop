package app

import (
	"fmt"
	"image"
	"math"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

type btopProcessPanel struct {
	rows         []ProcessMetrics
	valid, total int
	selected     int
	identity     ProcessMetrics
	scroll       int
}

var btopPanels = map[string]*btopProcessPanel{}
var btopFocus = "cpu"
var btopPanelsVisible bool
var btopProcessOverride *BtopProcessConfig

func btopProcessOptions() BtopProcessConfig {
	var saved *BtopProcessConfig
	if currentConfig.Htop != nil {
		saved = currentConfig.Htop.Processes
	}
	if btopProcessOverride != nil {
		saved = btopProcessOverride
	}
	return normalizeBtopProcessOptions(saved)
}

func normalizeBtopProcessOptions(saved *BtopProcessConfig) BtopProcessConfig {
	c := BtopProcessConfig{Mode: "tops", Count: 3, Blocks: "cpu,gpu,memory"}
	if saved != nil {
		if saved.Mode != "" {
			c.Mode = saved.Mode
		}
		if saved.Count != 0 {
			c.Count = saved.Count
		}
		if saved.Blocks != "" {
			c.Blocks = saved.Blocks
		}
		c.Name = saved.Name
		c.User = saved.User
	}
	if !stringIn(c.Mode, []string{"tops", "list", "hidden"}) {
		c.Mode = "tops"
	}
	if c.Count != 3 && c.Count != 5 && c.Count != 10 {
		c.Count = 3
	}
	var blocks []string
	for _, item := range strings.Split(strings.ToLower(c.Blocks), ",") {
		item = strings.TrimSpace(item)
		if stringIn(item, []string{"cpu", "gpu", "memory"}) && !stringIn(item, blocks) {
			blocks = append(blocks, item)
		}
	}
	if len(blocks) == 0 {
		blocks = []string{"cpu", "gpu", "memory"}
	}
	c.Blocks = strings.Join(blocks, ",")
	return c
}

func btopBlocks() []string { return strings.Split(btopProcessOptions().Blocks, ",") }

func rankedBtopProcesses(all []ProcessMetrics, c BtopProcessConfig, metric, query string) ([]ProcessMetrics, int, int) {
	view := make([]ProcessMetrics, 0, len(all))
	total := 0
	for _, p := range all {
		if c.Name != "" && !strings.Contains(strings.ToLower(p.Command), strings.ToLower(c.Name)) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(p.Command), strings.ToLower(query)) {
			continue
		}
		if c.User != "" && !strings.EqualFold(p.User, c.User) {
			continue
		}
		total++
		value, valid := processRankValue(p, metric)
		if valid && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 {
			view = append(view, p)
		}
	}
	valid := len(view)
	sort.Slice(view, func(i, j int) bool {
		a, _ := processRankValue(view[i], metric)
		b, _ := processRankValue(view[j], metric)
		if a == b {
			return view[i].PID < view[j].PID
		}
		return a > b
	})
	if len(view) > c.Count {
		view = view[:c.Count]
	}
	return view, valid, total
}

func refreshBtopProcesses() {
	if !isHtopMode() {
		return
	}
	c := btopProcessOptions()
	if !stringIn(btopFocus, btopBlocks()) {
		btopFocus = btopBlocks()[0]
	}
	for _, metric := range []string{"cpu", "gpu", "memory"} {
		panel := btopPanels[metric]
		if panel == nil {
			panel = &btopProcessPanel{selected: -1}
			btopPanels[metric] = panel
		}
		panel.rows, panel.valid, panel.total = rankedBtopProcesses(lastProcesses, c, metric, searchText)
		panel.selected = -1
		if panel.identity.PID > 0 {
			for i, p := range panel.rows {
				if p.PID == panel.identity.PID && p.BirthTime == panel.identity.BirthTime {
					panel.selected = i
					break
				}
			}
		}
		if panel.selected < 0 {
			panel.identity = ProcessMetrics{}
		}
		panel.scroll = max(0, min(panel.scroll, len(panel.rows)-1))
	}
}

func (v *btopProcessArea) Draw(buf *ui.Buffer) {
	c := btopProcessOptions()
	p := htopPalette()
	if c.Mode == "hidden" {
		drawBtopChart(buf, v.Rectangle, btopGPUTitle(), "gpu", 100, p.gpu)
		return
	}
	if c.Mode == "list" {
		btopPanelsVisible = true
		processList.SetRect(v.Min.X, v.Min.Y, v.Max.X, v.Max.Y)
		processList.Draw(buf)
		return
	}
	btopPanelsVisible = true
	blocks := btopBlocks()
	for i, metric := range blocks {
		panel := btopPanels[metric]
		if panel == nil {
			continue
		}
		r := image.Rect(v.Min.X+i*v.Dx()/len(blocks), v.Min.Y, v.Min.X+(i+1)*v.Dx()/len(blocks), v.Max.Y)
		color := map[string]ui.Color{"cpu": p.user, "gpu": p.gpu, "memory": p.memory}[metric]
		name := map[string]string{"cpu": "CPU", "gpu": "GPU ~", "memory": i18n.T("Btop_Memory")}[metric]
		title := fmt.Sprintf("Top %d · %s", c.Count, name)
		filters := strings.TrimSpace(c.Name + " " + searchText)
		if c.User != "" {
			filters += " @" + c.User
		}
		if filters != "" {
			title += " /" + filters
		}
		if metric == btopFocus {
			title = "› " + title
		}
		if isFrozen {
			title += " · ||"
		}
		inside := btopBox(buf, r, title, color)
		if inside.Empty() {
			continue
		}
		rows := max(0, inside.Dy()-1)
		if panel.selected >= 0 {
			if panel.selected < panel.scroll {
				panel.scroll = panel.selected
			}
			if rows > 0 && panel.selected >= panel.scroll+rows {
				panel.scroll = panel.selected - rows + 1
			}
		}
		for row := 0; row < rows && panel.scroll+row < len(panel.rows); row++ {
			idx := panel.scroll + row
			process := panel.rows[idx]
			value := ""
			switch metric {
			case "cpu":
				value = fmt.Sprintf("%.1f%%", process.CPU/float64(max(1, cachedSystemInfo.CoreCount)))
			case "gpu":
				value = fmt.Sprintf("~%.1f%%", process.GPU/10)
			case "memory":
				value = formatBytes(float64(process.RSS)*1024, "auto")
			}
			text := runewidth.Truncate(process.Command, max(0, inside.Dx()-runewidth.StringWidth(value)-2), "")
			text += strings.Repeat(" ", max(1, inside.Dx()-runewidth.StringWidth(value)-runewidth.StringWidth(text))) + value
			if idx == panel.selected && metric == btopFocus {
				style := ui.NewStyle(p.headerFgColor(), p.gpu)
				buf.Fill(ui.NewCell(' ', style), image.Rect(inside.Min.X, inside.Min.Y+row, inside.Max.X, inside.Min.Y+row+1))
				buf.SetString(runewidth.Truncate(text, inside.Dx(), ""), style, image.Pt(inside.Min.X, inside.Min.Y+row))
			} else {
				btopText(buf, inside, inside.Min.Y+row, text, p.text)
			}
		}
		if len(panel.rows) == 0 {
			btopText(buf, inside, inside.Min.Y, i18n.T("Btop_NoProcesses"), p.text)
		}
		status := fmt.Sprintf("%d/%d · %d/%d", min(rows, max(0, len(panel.rows)-panel.scroll)), len(panel.rows), panel.valid, panel.total)
		if panel.selected >= 0 && metric == btopFocus {
			status = fmt.Sprintf("PID %d · %s", panel.identity.PID, status)
		}
		btopText(buf, inside, inside.Max.Y-1, status, p.text)
	}
}

func (p htopColors) headerFgColor() ui.Color {
	// Compute contrast against the actual selection background, including custom themes.
	r, g, b := p.gpu.RGB()
	if r >= 0 && .299*float64(r)+.587*float64(g)+.114*float64(b) < 128 {
		return ui.ColorWhite
	}
	return ui.ColorBlack
}

func handleBtopProcessKey(key string) bool {
	if !isHtopMode() || !btopPanelsVisible || btopProcessOptions().Mode != "tops" {
		return false
	}
	blocks := btopBlocks()
	panel := btopPanels[btopFocus]
	if panel == nil {
		return false
	}
	switch key {
	case "<Tab>", "<Backtab>", "<Left>", "<Right>":
		index := 0
		for i, m := range blocks {
			if m == btopFocus {
				index = i
			}
		}
		step := 1
		if key == "<Backtab>" || key == "<Left>" {
			step = -1
		}
		btopFocus = blocks[(index+step+len(blocks))%len(blocks)]
	case "j", "<Down>", "<MouseWheelDown>", "k", "<Up>", "<MouseWheelUp>", "g", "<Home>", "G", "<End>":
		if len(panel.rows) == 0 {
			return true
		}
		index := panel.selected
		switch key {
		case "g", "<Home>":
			index = 0
		case "G", "<End>":
			index = len(panel.rows) - 1
		case "k", "<Up>", "<MouseWheelUp>":
			index = max(0, index-1)
		default:
			index = min(len(panel.rows)-1, index+1)
		}
		panel.selected = index
		panel.identity = panel.rows[index]
	case "<F9>":
		attemptBtopKill()
	default:
		return false
	}
	return true
}

func attemptBtopKill() {
	if !btopPanelsVisible {
		return
	}
	panel := btopPanels[btopFocus]
	if panel == nil || panel.selected < 0 || panel.selected >= len(panel.rows) {
		return
	}
	p := panel.rows[panel.selected]
	if p.PID <= 0 || p.BirthTime <= 0 || p.PID != panel.identity.PID || p.BirthTime != panel.identity.BirthTime {
		return
	}
	showKillModal(p.PID)
	killBirthTime = p.BirthTime
}
