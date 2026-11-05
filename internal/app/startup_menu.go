package app

import (
	"image"
	"os"
	"os/signal"
	"syscall"

	"github.com/mattn/go-runewidth"
	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

type startupModeMenu struct {
	*ui.Block
	selected int
}

func newStartupModeMenu(saved string) *startupModeMenu {
	selected := 0 // The new TUI is the first choice on a fresh installation.
	if saved == UIModeOriginal {
		selected = 1
	}
	return &startupModeMenu{Block: ui.NewBlock(), selected: selected}
}

func (m *startupModeMenu) mode() string {
	if m.selected == 1 {
		return UIModeOriginal
	}
	return UIModeHtop
}

func startupMenuKey(key string, selected int) (next int, accept, cancel bool) {
	next = selected
	switch key {
	case "<Up>", "k", "<Down>", "j", "<Tab>":
		next = 1 - selected
	case "1":
		next, accept = 0, true
	case "2":
		next, accept = 1, true
	case "<Enter>":
		accept = true
	case "q", "<Escape>", "<C-c>":
		cancel = true
	}
	return
}

func (m *startupModeMenu) Draw(buf *ui.Buffer) {
	p := htopPalette()
	textStyle := ui.NewStyle(p.text)
	buf.Fill(ui.NewCell(' ', textStyle), m.Rectangle)
	width, height := min(72, m.Dx()), min(10, m.Dy())
	if width <= 0 || height <= 0 {
		return
	}
	x := m.Min.X + (m.Dx()-width)/2
	y := m.Min.Y + (m.Dy()-height)/2
	card := ui.NewBlock()
	card.BorderRounded = true
	card.BorderStyle = ui.NewStyle(p.user)
	card.TitleStyle = textStyle
	card.Title = runewidth.Truncate(i18n.T("TUI_ModeMenuTitle"), max(0, width-4), "")
	card.SetRect(x, y, x+width, y+height)
	card.Draw(buf)
	write := func(row int, text string, style ui.Style) {
		if row < card.Inner.Min.Y || row >= card.Inner.Max.Y || card.Inner.Dx() <= 0 {
			return
		}
		buf.SetString(runewidth.Truncate(text, card.Inner.Dx(), ""), style, image.Pt(card.Inner.Min.X, row))
	}
	start, step := card.Inner.Min.Y, 1
	if card.Inner.Dy() >= 6 {
		start++
		step = 2
	}
	for i, label := range []string{i18n.T("TUI_ModeMenuHtop"), i18n.T("TUI_ModeMenuOriginal")} {
		style, prefix := textStyle, "   "
		if i == m.selected {
			style, prefix = ui.NewStyle(ui.ColorBlack, ui.ColorCyan, ui.ModifierBold), " > "
		}
		write(start+i*step, prefix+label, style)
	}
	if card.Inner.Dy() >= 6 {
		write(card.Inner.Max.Y-2, i18n.T("TUI_ModeMenuNavigate"), textStyle)
		write(card.Inner.Max.Y-1, i18n.T("TUI_ModeMenuShortcuts"), textStyle)
	}
}

func shouldShowStartupMenu() bool {
	return cliUIMode == "" && !headless && !menubar && !overlay &&
		!menubarWorker && !overlayWorker && !dumpTemps && !dumpDebug && !dumpFPS && !fanCLIRequested()
}

// This runs before metrics, native workers and the background render loop.
// Reuse its event channel for the monitor so two pollers cannot steal keys.
func chooseStartupUIMode(events <-chan ui.Event) (string, bool) {
	menu := newStartupModeMenu(currentConfig.UIMode)
	render := func() {
		width, height := ui.TerminalDimensions()
		menu.SetRect(0, 0, width, height)
		ui.Clear()
		ui.Render(menu)
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(interrupts)
	render()
	for {
		select {
		case <-interrupts:
			return "", false
		case event, ok := <-events:
			if !ok {
				return "", false
			}
			switch event.Type {
			case ui.ResizeEvent:
				render()
			case ui.KeyboardEvent:
				next, accept, cancel := startupMenuKey(event.ID, menu.selected)
				if cancel {
					return "", false
				}
				menu.selected = next
				if accept {
					return menu.mode(), true
				}
				render()
			}
		}
	}
}
