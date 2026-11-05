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
	choices  []string
	title    string
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
	textStyle := ui.NewStyle(p.text, htopEffectiveBg())
	buf.Fill(ui.NewCell(' ', textStyle), m.Rectangle)
	width, height := min(72, m.Dx()), min(10, m.Dy())
	if width <= 0 || height <= 0 {
		return
	}
	x := m.Min.X + (m.Dx()-width)/2
	y := m.Min.Y + (m.Dy()-height)/2
	card := ui.NewBlock()
	card.BorderRounded = true
	card.BackgroundColor = htopEffectiveBg()
	card.BorderStyle = ui.NewStyle(p.user, htopEffectiveBg())
	card.TitleStyle = textStyle
	title := m.title
	if title == "" {
		title = i18n.T("TUI_ModeMenuTitle")
	}
	card.Title = runewidth.Truncate(title, max(0, width-4), "")
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
	labels := m.choices
	if labels == nil {
		labels = []string{i18n.T("TUI_ModeMenuHtop"), i18n.T("TUI_ModeMenuOriginal")}
	}
	for i, label := range labels {
		style, prefix := textStyle, "   "
		if i == m.selected {
			style, prefix = ui.NewStyle(ui.ColorBlack, ui.ColorCyan, ui.ModifierBold), " > "
		}
		write(start+i*step, prefix+label, style)
	}
	if card.Inner.Dy() >= 6 {
		write(card.Inner.Max.Y-2, i18n.T("TUI_ModeMenuNavigate"), textStyle)
		keys := "Settings_MenuKeys"
		if m.choices == nil {
			keys = "Settings_RootMenuKeys"
		}
		write(card.Inner.Max.Y-1, i18n.T(keys), textStyle)
	}
}

func shouldShowStartupMenu() bool {
	return cliUIMode == "" && !headless && !menubar && !overlay &&
		!menubarWorker && !overlayWorker && !dumpTemps && !dumpDebug && !dumpFPS && !fanCLIRequested()
}

// This runs before metrics, native workers and the background render loop.
// Reuse its event channel for the monitor so two pollers cannot steal keys.
func chooseStartupUIMode(events <-chan ui.Event) (string, bool) {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(interrupts)
	return chooseStartupWithSignals(events, interrupts)
}

func chooseStartupWithSignals(events <-chan ui.Event, interrupts <-chan os.Signal) (string, bool) {
	root := newStartupModeMenu(currentConfig.UIMode)
	sub := &startupModeMenu{Block: ui.NewBlock(), title: i18n.T("Settings_NewTitle"), choices: []string{i18n.T("Settings_Start"), i18n.T("Settings_Open"), i18n.T("Settings_Back")}}
	stage := "root"
	defer func() { activeSettings = nil }()
	render := func() {
		width, height := ui.TerminalDimensions()
		UpdateCachedTerminalDimensions(width, height)
		ui.Clear()
		if activeSettings != nil {
			activeSettings.SetRect(0, 0, width, height)
			ui.Render(activeSettings)
		} else {
			menu := root
			if stage == "new" {
				menu = sub
			}
			menu.SetRect(0, 0, width, height)
			ui.Render(menu)
		}
	}
	render()
	for {
		select {
		case <-interrupts:
			return "", false
		case event, ok := <-events:
			if !ok {
				return "", false
			}
			if event.Type == ui.ResizeEvent {
				render()
				continue
			}
			if event.Type != ui.KeyboardEvent {
				continue
			}
			key := event.ID
			if activeSettings != nil {
				close, quit := activeSettings.key(key)
				if quit {
					return "", false
				}
				if close {
					activeSettings = nil
				}
				render()
				continue
			}
			if stage == "root" {
				next, accept, cancel := startupMenuKey(key, root.selected)
				if cancel {
					return "", false
				}
				root.selected = next
				if accept {
					if root.mode() == UIModeOriginal {
						return UIModeOriginal, true
					}
					stage = "new"
				}
			} else {
				switch key {
				case "<C-c>", "q":
					return "", false
				case "<Escape>", "3":
					stage = "root"
				case "<Down>", "j", "<Tab>":
					sub.selected = (sub.selected + 1) % 3
				case "<Up>", "k":
					sub.selected = (sub.selected + 2) % 3
				case "1":
					return UIModeHtop, true
				case "2":
					activeSettings = newSettingsEditor(true)
				case "<Enter>":
					switch sub.selected {
					case 0:
						return UIModeHtop, true
					case 1:
						activeSettings = newSettingsEditor(true)
					case 2:
						stage = "root"
					}
				}
			}
			render()
		}
	}
}
