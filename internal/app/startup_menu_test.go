package app

import (
	"image"
	"strings"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

func TestStartupMenuKeyControls(t *testing.T) {
	for _, tc := range []struct {
		key            string
		selected, next int
		accept, cancel bool
	}{
		{"<Down>", 0, 1, false, false}, {"<Up>", 1, 0, false, false},
		{"j", 0, 1, false, false}, {"k", 1, 0, false, false}, {"<Tab>", 1, 0, false, false},
		{"<Enter>", 0, 0, true, false}, {"<Enter>", 1, 1, true, false},
		{"1", 1, 0, true, false}, {"2", 0, 1, true, false},
		{"q", 0, 0, false, true}, {"<Escape>", 1, 1, false, true}, {"<C-c>", 0, 0, false, true},
		{"x", 1, 1, false, false},
	} {
		next, accept, cancel := startupMenuKey(tc.key, tc.selected)
		if next != tc.next || accept != tc.accept || cancel != tc.cancel {
			t.Fatalf("key %q selected=%d -> %d %v %v", tc.key, tc.selected, next, accept, cancel)
		}
	}
}

func TestStartupMenuRemembersChoice(t *testing.T) {
	for _, tc := range []struct{ saved, want string }{{"", UIModeHtop}, {"invalid", UIModeHtop}, {UIModeHtop, UIModeHtop}, {UIModeOriginal, UIModeOriginal}} {
		if got := newStartupModeMenu(tc.saved).mode(); got != tc.want {
			t.Fatalf("saved=%q mode=%q", tc.saved, got)
		}
	}
}

func TestStartupMenuSelectionAndCancellation(t *testing.T) {
	modeTestUI(t)
	for _, tc := range []struct {
		keys     []string
		want     string
		accepted bool
	}{
		{[]string{"1", "1"}, UIModeHtop, true}, {[]string{"2"}, UIModeOriginal, true},
		{[]string{"<Down>", "<Enter>"}, UIModeOriginal, true},
		{[]string{"q"}, "", false}, {[]string{"<Escape>"}, "", false}, {[]string{"<C-c>"}, "", false},
		{nil, "", false},
	} {
		before := currentConfig
		events := make(chan ui.Event, len(tc.keys)+1)
		events <- ui.Event{Type: ui.ResizeEvent, Payload: ui.Resize{Width: 80, Height: 24}}
		for _, key := range tc.keys {
			events <- ui.Event{Type: ui.KeyboardEvent, ID: key}
		}
		close(events)
		mode, accepted := chooseStartupUIMode(events)
		if mode != tc.want || accepted != tc.accepted {
			t.Fatalf("keys=%v -> %q %v", tc.keys, mode, accepted)
		}
		if currentConfig.UIMode != before.UIMode || currentConfig.DefaultLayout != before.DefaultLayout {
			t.Fatal("menu mutated preferences before confirmation was applied")
		}
	}
}

func TestStartupMenuDoesNotInterceptExplicitOrNonTUIModes(t *testing.T) {
	var restore []func()
	t.Cleanup(func() {
		for i := len(restore) - 1; i >= 0; i-- {
			restore[i]()
		}
	})
	replaceUIValue(&restore, &cliUIMode, "")
	for _, flag := range []*bool{&headless, &menubar, &overlay, &menubarWorker, &overlayWorker, &dumpTemps, &dumpDebug, &dumpFPS, &fanStatusFlag, &fanAutoFlag} {
		replaceUIValue(&restore, flag, false)
	}
	replaceUIValue(&restore, &fanSetSpec, "")
	if !shouldShowStartupMenu() {
		t.Fatal("normal launch did not request a menu")
	}
	for _, mode := range []string{UIModeHtop, UIModeOriginal} {
		cliUIMode = mode
		if shouldShowStartupMenu() {
			t.Fatal("explicit UI mode was intercepted")
		}
	}
	cliUIMode = ""
	for _, flag := range []*bool{&headless, &menubar, &overlay, &menubarWorker, &overlayWorker, &dumpTemps, &dumpDebug, &dumpFPS, &fanStatusFlag, &fanAutoFlag} {
		*flag = true
		if shouldShowStartupMenu() {
			t.Fatal("non-TUI flag was intercepted")
		}
		*flag = false
	}
	fanSetSpec = "auto"
	if shouldShowStartupMenu() {
		t.Fatal("one-shot fan command was intercepted")
	}
}

func TestStartupMenuRenderingIsClippedAndOrdered(t *testing.T) {
	i18n.Init("en")
	for _, size := range []image.Point{{0, 0}, {1, 1}, {3, 3}, {20, 6}, {80, 24}, {120, 40}} {
		menu := newStartupModeMenu("")
		menu.SetRect(2, 2, 2+size.X, 2+size.Y)
		buf := ui.NewBuffer(image.Rect(0, 0, 126, 46))
		menu.Draw(buf)
		var text strings.Builder
		for y := range 46 {
			for x := range 126 {
				pt := image.Pt(x, y)
				cell := buf.GetCell(pt)
				if !pt.In(menu.Rectangle) && cell != ui.CellClear {
					t.Fatalf("size=%v wrote outside menu at %v", size, pt)
				}
				text.WriteRune(cell.Rune)
			}
		}
		if size.X >= 80 {
			s := text.String()
			first, second := strings.Index(s, "1. New TUI (btop)"), strings.Index(s, "2. Original")
			if first < 0 || second <= first {
				t.Fatal("startup choices were missing or out of order")
			}
		}
	}
}

func TestStartupMenuKeyHintsMatchRootAndSubmenu(t *testing.T) {
	i18n.Init("en")
	for _, root := range []bool{true, false} {
		menu := newStartupModeMenu("")
		if !root {
			menu.choices = []string{"Start", "Settings", "Back"}
		}
		menu.SetRect(0, 0, 80, 24)
		buf := ui.NewBuffer(image.Rect(0, 0, 80, 24))
		menu.Draw(buf)
		var text strings.Builder
		for _, cell := range buf.Cells {
			text.WriteRune(cell.Rune)
		}
		if root && (!strings.Contains(text.String(), "q/Esc: exit") || strings.Contains(text.String(), "1/2/3")) {
			t.Fatal("root hints do not match root handlers")
		}
		if !root && (!strings.Contains(text.String(), "Esc: back") || !strings.Contains(text.String(), "1/2/3")) {
			t.Fatal("submenu hints do not match submenu handlers")
		}
	}
}
