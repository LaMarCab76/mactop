package app

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ui "github.com/metaspartan/gotui/v5"
)

func solidBtopCell(r rune) bool { return r >= '▁' && r <= '█' }

func TestBtopHistoryColumnsAreSolidAndGrounded(t *testing.T) {
	modeTestUI(t)
	for _, style := range []string{"", "classic", "blocks", "gradient_blocks"} {
		currentConfig.Htop = &HtopConfig{BarStyle: style}
		mode, resolution := btopGlyphMode()
		if mode != 1 || resolution != 1 {
			t.Fatalf("%q selected dotted history instead of solid columns", style)
		}
	}
	for _, mode := range []int{0, 1} {
		area := image.Rect(2, 2, 7, 6)
		buf := ui.NewBuffer(image.Rect(0, 0, 10, 9))
		plot := btopPlot{mode: mode, resolution: 1, ceiling: 100, points: []btopPoint{{Value: 25}, {Value: 75}, {Value: 0}, {Value: math.NaN()}}}
		drawBtopPlot(buf, area, plot, ui.ColorCyan)
		for x, height := range []int{0, 1, 3, 1, 0} {
			for y := area.Min.Y; y < area.Max.Y; y++ {
				r := buf.GetCell(image.Pt(area.Min.X+x, y)).Rune
				filled := r == '#' || solidBtopCell(r)
				if filled != (y >= area.Max.Y-height) {
					t.Fatalf("mode %d column %d is floating or has a false sample at y=%d: %q", mode, x, y, r)
				}
				if r >= '\u2800' && r <= '\u28ff' {
					t.Fatal("braille dot in a solid history")
				}
			}
		}
	}
}

func TestBtopMemoryHistoryPreservesMissingSamplesAndMeasuredZero(t *testing.T) {
	modeTestUI(t)
	at := time.Unix(1000, 0)
	for i, sample := range []MemoryMetrics{{Total: 100, Used: 50}, {}, {Total: 100}, {Total: 100, Used: 101}} {
		recordBtopMemory(sample, at.Add(time.Duration(i)*time.Second))
	}
	points := btopData.history("memory").tail(4)
	if len(points) != 4 || points[0].Value != 50 || !math.IsNaN(points[1].Value) || points[2].Value != 0 || !math.IsNaN(points[3].Value) {
		t.Fatalf("invalid memory reading became a valid zero: %v", points)
	}
	if btopMemoryLabel() != "—" {
		t.Fatal("impossible memory usage shown as measured")
	}
}

func TestBtopResourceHistoriesVisibleWithProcessBlocks(t *testing.T) {
	seedBtopTest(t)
	for _, size := range []image.Point{{80, 24}, {100, 28}, {120, 40}, {160, 50}, {240, 90}} {
		for _, layout := range []string{LayoutDefault, LayoutGPUFocus, LayoutTop3} {
			for _, count := range []int{3, 5, 10} {
				currentConfig.Htop = &HtopConfig{Processes: &BtopProcessConfig{Count: count}}
				runtimeLayout = layout
				applyLayout(layout)
				buf := btopTestBuffer(size.X, size.Y)
				text := btopBufferText(buf)
				lines := strings.Split(text, "\n")
				top := -1
				for y, line := range lines {
					if strings.Contains(line, "Memory") && strings.Contains(line, "Network") && strings.Contains(line, "Disk I/O") {
						top = y
						break
					}
				}
				if top < 0 || !strings.Contains(text, fmt.Sprintf("Top %d", count)) {
					t.Fatalf("%v %s Top %d lost resource/process blocks:\n%s", size, layout, count, text)
				}
				bottom := top + 1
				for bottom < size.Y && buf.GetCell(image.Pt(1, bottom)).Rune != '╰' {
					bottom++
				}
				for i := 0; i < 3; i++ {
					x1, x2 := 2+i*(size.X-2)/3, (i+1)*(size.X-2)/3
					rows := 0
					for y := top + 2; y < bottom; y++ {
						filled := false
						for x := x1; x < x2; x++ {
							r := buf.GetCell(image.Pt(x, y)).Rune
							filled = filled || solidBtopCell(r)
						}
						if filled {
							rows++
						}
					}
					if rows < 2 {
						t.Fatalf("%v %s Top %d panel %d has no visible history for each series:\n%s", size, layout, count, i, text)
					}
				}
				if dir := os.Getenv("MACTOP_CAPTURE_DIR"); dir != "" && count == 3 && layout == LayoutDefault {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("solid-%dx%d.txt", size.X, size.Y)), []byte(text), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}

func TestBtopPowerPlotGrowsWithTerminal(t *testing.T) {
	seedBtopTest(t)
	runtimeLayout = LayoutDefault
	applyLayout(LayoutDefault)
	previous := 0
	for _, size := range []image.Point{{80, 24}, {120, 40}, {160, 50}, {240, 90}} {
		buf := btopTestBuffer(size.X, size.Y)
		rows := 0
		for y := 1; y < btopContentRect(size.X, size.Y).Min.Y; y++ {
			if solidBtopCell(buf.GetCell(image.Pt(size.X-3, y)).Rune) {
				rows++
			}
		}
		if rows < 3 || rows <= previous {
			t.Fatalf("power plot did not get more space at %v: %d rows after %d", size, rows, previous)
		}
		previous = rows
	}
}

func TestBtopNetworkIOKeepsFourRatePlotsAtMinimumSize(t *testing.T) {
	seedBtopTest(t)
	runtimeLayout = LayoutNetworkIO
	applyLayout(LayoutNetworkIO)
	buf := btopTestBuffer(80, 24)
	text := btopBufferText(buf)
	for _, label := range []string{"RX", "TX", "Disk I/O total R", "Disk I/O total W", "Top 3"} {
		if !strings.Contains(text, label) {
			t.Fatal("minimum Network I/O layout lost a graph", label, text)
		}
	}
	// Each dedicated plot must have a solid column, not merely a title.
	var graphRows []int
	for y, line := range strings.Split(text, "\n") {
		if (strings.Contains(line, "RX") && strings.Contains(line, "TX")) || strings.Contains(line, "Disk I/O total R") {
			graphRows = append(graphRows, y+1)
		}
	}
	if len(graphRows) != 2 {
		t.Fatalf("expected two rows of rate plots: %v\n%s", graphRows, text)
	}
	for _, row := range graphRows {
		for _, x := range []int{38, 77} {
			if !solidBtopCell(buf.GetCell(image.Pt(x, row)).Rune) {
				t.Fatalf("rate plot missing at (%d,%d):\n%s", x, row, text)
			}
		}
	}
}

func TestBtopMinimumChartsKeepAxesAndGPUDetails(t *testing.T) {
	seedBtopTest(t)
	btopData.power.ANE = powerReading(.4, true, "ANE")
	for _, mode := range []string{"tops", "list", "hidden"} {
		for _, count := range []int{3, 5, 10} {
			currentConfig.Htop = &HtopConfig{Processes: &BtopProcessConfig{Mode: mode, Count: count}}
			for _, layout := range []string{LayoutDefault, LayoutGPUFocus} {
				runtimeLayout = layout
				applyLayout(layout)
				text := btopBufferText(btopTestBuffer(80, 24))
				for _, want := range []string{"RX 0–", "TX 0–", "R 0–", "W 0–"} {
					if !strings.Contains(text, want) {
						t.Fatalf("%s Top %d %s lost compact rate axis %q:\n%s", mode, count, layout, want, text)
					}
				}
				main := strings.Split(text, "Memory")[0]
				if layout == LayoutDefault && strings.Count(main, "0–100%") < 2 {
					t.Fatalf("minimum CPU/GPU plots lost their axes:\n%s", text)
				}
				if layout == LayoutGPUFocus {
					for _, want := range []string{"DRAM R", "GB/s", "ANE 0.4W", "0–100%"} {
						if !strings.Contains(main, want) {
							t.Fatalf("minimum GPU/AI view lost %q:\n%s", want, text)
						}
					}
				}
			}
		}
	}
}

func TestBtopPowerHeightDoesNotCollapseUnderOverlays(t *testing.T) {
	seedBtopTest(t)
	for _, size := range []image.Point{{80, 24}, {120, 40}, {240, 90}} {
		for _, layout := range []string{LayoutDefault, LayoutEnergy} {
			runtimeLayout = layout
			applyLayout(layout)
			want := btopContentRect(size.X, size.Y).Min.Y
			for _, overlay := range []string{"settings", "help", "kill"} {
				activeSettings, showHelp, killPending = nil, false, false
				switch overlay {
				case "settings":
					activeSettings = newSettingsEditor(false)
				case "help":
					showHelp = true
				case "kill":
					killPending = true
				}
				btopTestBuffer(size.X, size.Y)
				if got := btopContentRect(size.X, size.Y).Min.Y; got != want {
					t.Fatalf("%v %s %s collapsed power from %d to %d", size, layout, overlay, want, got)
				}
			}
			activeSettings, showHelp, killPending = nil, false, false
		}
	}
}

func TestBtopCompactRateAxesStayVisibleForBothDirections(t *testing.T) {
	seedBtopTest(t)
	for _, height := range []int{5, 6, 7, 8} {
		buf := ui.NewBuffer(image.Rect(0, 0, 80, height))
		drawBtopResourceHistories(buf, buf.Rectangle)
		text := btopBufferText(buf)
		for _, want := range []string{"RX 0–70.4KB", "TX 0–8.8KB", "R 0–1.1MB", "W 0–281.6KB"} {
			if !strings.Contains(text, want) {
				t.Fatalf("height %d lost rate scale %q:\n%s", height, want, text)
			}
		}
	}
}

func TestBtopNetworkAxesSurviveIntermediateResizes(t *testing.T) {
	seedBtopTest(t)
	runtimeLayout = LayoutNetworkIO
	applyLayout(LayoutNetworkIO)
	for height := 24; height <= 50; height++ {
		for _, mode := range []string{"tops", "list", "hidden"} {
			for _, count := range []int{3, 5, 10} {
				currentConfig.Htop = &HtopConfig{Processes: &BtopProcessConfig{Mode: mode, Count: count}}
				buf := btopTestBuffer(80, height)
				text := btopBufferText(buf)
				headers := 0
				for y, line := range strings.Split(text, "\n") {
					if !(strings.Contains(line, "RX") && strings.Contains(line, "TX")) && !strings.Contains(line, "Disk I/O total R") {
						continue
					}
					headers++
					bottom := y + 1
					for bottom < height && buf.GetCell(image.Pt(1, bottom)).Rune != '╰' {
						bottom++
					}
					for _, x1 := range []int{2, 41} {
						var panel strings.Builder
						filled := false
						for row := y + 1; row < bottom; row++ {
							for x := x1; x < x1+37; x++ {
								r := buf.GetCell(image.Pt(x, row)).Rune
								panel.WriteRune(r)
								filled = filled || solidBtopCell(r)
							}
							panel.WriteByte('\n')
						}
						if !filled || !strings.Contains(panel.String(), "0–") || !strings.Contains(panel.String(), "samples ·") {
							t.Fatalf("80x%d %s Top%d rate plot at x=%d lost graph/axis/span:\n%s", height, mode, count, x1, text)
						}
					}
				}
				if headers != 2 {
					t.Fatalf("expected four dedicated graphs at height %d: %d rows", height, headers)
				}
				if dir := os.Getenv("MACTOP_CAPTURE_DIR"); dir != "" && height == 28 && mode == "tops" && count == 3 {
					if err := os.WriteFile(filepath.Join(dir, "network-80x28.txt"), []byte(text), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}
