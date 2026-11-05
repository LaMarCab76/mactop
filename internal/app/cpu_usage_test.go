package app

import (
	"image"
	"math"
	"strings"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
)

func TestCPUPercentagesShareOneInterval(t *testing.T) {
	old := []CPUUsage{{User: 100, System: 20, Nice: 5, Idle: 200}}
	now := []CPUUsage{{User: 130, System: 40, Nice: 15, Idle: 240}}
	totals, parts := cpuPercentagesFromTimes(old, now)
	if totals[0] != 60 || parts[0] != (CPUTimePercent{User: 30, System: 20, Nice: 10, Valid: true}) {
		t.Fatalf("total=%v parts=%+v", totals, parts)
	}
	for _, previous := range [][]CPUUsage{nil, {{}, {}}} {
		totals, parts = cpuPercentagesFromTimes(previous, now)
		if len(totals) != 1 || totals[0] != 0 || parts[0].Valid {
			t.Fatalf("baseline/topology change must return zeros without a breakdown: %v %+v", totals, parts)
		}
	}
}

func TestCPUPercentagesInvalidIntervals(t *testing.T) {
	for _, now := range []CPUUsage{
		{}, {User: -1, System: 100}, {User: math.NaN()}, {Idle: math.Inf(1)},
	} {
		totals, parts := cpuPercentagesFromTimes([]CPUUsage{{}}, []CPUUsage{now})
		if totals[0] != 0 || parts[0].Valid {
			t.Fatalf("invalid interval %+v produced %v %+v", now, totals, parts)
		}
	}
	// A reset invalidates only the affected CPU. The next sample uses the
	// newly recorded baseline and can recover without fabricating a delta.
	previous := []CPUUsage{{User: 20}, {User: 10}}
	current := []CPUUsage{{User: 5, Idle: 100}, {User: 20, Idle: 10}}
	totals, parts := cpuPercentagesFromTimes(previous, current)
	if parts[0].Valid || totals[0] != 0 || !parts[1].Valid || totals[1] != 50 {
		t.Fatalf("per-core reset: %v %+v", totals, parts)
	}
	totals, parts = cpuPercentagesFromTimes(current, []CPUUsage{{User: 15, Idle: 110}, {User: 30, Idle: 20}})
	if !parts[0].Valid || totals[0] != 50 {
		t.Fatalf("reset did not recover: %v %+v", totals, parts)
	}
}

func TestCPUSegmentRoundingAndBounds(t *testing.T) {
	for width := 0; width < 30; width++ {
		for _, parts := range []CPUTimePercent{
			{Nice: 10, User: 30, System: 20, Valid: true},
			{Nice: 33.4, User: 33.4, System: 33.4, Valid: true},
			{User: 100, Valid: true}, {User: math.NaN(), System: math.Inf(1), Nice: -20, Valid: true}, {},
		} {
			ends := cpuSegmentEnds(parts, width)
			if ends[0] < 0 || ends[0] > ends[1] || ends[1] > ends[2] || ends[2] > width {
				t.Fatalf("overflow: width=%d parts=%+v ends=%v", width, parts, ends)
			}
			if parts.User == 100 && ends[2] != width {
				t.Fatalf("full CPU must fill bar: width=%d ends=%v", width, ends)
			}
		}
	}
	if got := cpuSegmentEnds(CPUTimePercent{Nice: 10, User: 30, System: 20, Valid: true}, 10); got != [3]int{1, 4, 6} {
		t.Fatalf("segment order/length = %v, want blue 1, green 3, red 2", got)
	}
}

func TestCPUCoreSampleRemapsAndClears(t *testing.T) {
	w := &CPUCoreWidget{Block: ui.NewBlock(), cores: make([]float64, 3), cpuIndexMap: []int{2, -1, 0}}
	parts := []CPUTimePercent{{User: 10, Valid: true}, {System: 20, Valid: true}, {Nice: 30, Valid: true}}
	w.UpdateSample([]float64{10, 20, 30}, parts)
	if w.cores[0] != 30 || w.breakdown[0] != parts[2] || w.cores[1] != 0 || w.breakdown[1].Valid || w.breakdown[2] != parts[0] {
		t.Fatalf("incorrect hardware mapping: cores=%v parts=%+v", w.cores, w.breakdown)
	}
	w.UpdateSample([]float64{10, 20, 30}, parts[:1])
	if w.breakdown != nil {
		t.Fatal("mismatched breakdown retained old data")
	}
	w.UpdateSample(nil, nil)
	if w.cores[0] != 30 || w.breakdown != nil {
		t.Fatal("failed read changed Original totals or retained an invalid breakdown")
	}
}

func TestCPUCoreTopologyRefreshIsBounded(t *testing.T) {
	w := &CPUCoreWidget{cores: make([]float64, 4)}
	for _, count := range []int{6, 8} {
		for attempt := 0; attempt < maxTopologyRetries+3; attempt++ {
			refresh := w.shouldRefreshTopology(count)
			if refresh != (attempt < maxTopologyRetries) {
				t.Fatalf("count %d attempt %d refresh=%v", count, attempt, refresh)
			}
			w.cores = make([]float64, count)
		}
	}
	if w.shouldRefreshTopology(0) {
		t.Fatal("missing sample triggered topology detection")
	}
}

func TestCPUCoreRenderingStaysInsideInner(t *testing.T) {
	oldMode := activeUIMode
	t.Cleanup(func() { activeUIMode = oldMode })
	for _, mode := range []string{UIModeOriginal, UIModeHtop} {
		activeUIMode = mode
		for _, size := range []image.Point{{0, 0}, {1, 1}, {3, 2}, {7, 4}, {36, 3}, {76, 8}, {116, 16}} {
			for _, count := range []int{1, 14, 32, 128} {
				w := &CPUCoreWidget{Block: ui.NewBlock(), cores: make([]float64, count), breakdown: make([]CPUTimePercent, count)}
				w.Border = false
				w.SetRect(2, 2, 2+size.X, 2+size.Y)
				for i := range w.cores {
					w.cores[i], w.breakdown[i] = 60, CPUTimePercent{User: 30, System: 20, Nice: 10, Valid: true}
				}
				buf := ui.NewBuffer(image.Rect(0, 0, 124, 44))
				w.Draw(buf)
				for y := range 44 {
					for x := range 124 {
						pt := image.Pt(x, y)
						if !pt.In(w.Inner) && buf.GetCell(pt) != ui.CellClear {
							t.Fatalf("%s size=%v cores=%d wrote outside Inner at %v", mode, size, count, pt)
						}
					}
				}
			}
		}
	}
}

func TestHtopCoreSegmentsAndRedraw(t *testing.T) {
	oldMode, oldConfig := activeUIMode, currentConfig
	t.Cleanup(func() { activeUIMode, currentConfig = oldMode, oldConfig })
	currentConfig.Htop = &HtopConfig{BarStyle: "classic"}
	activeUIMode = UIModeHtop
	w := &CPUCoreWidget{Block: ui.NewBlock(), cores: []float64{60}, breakdown: []CPUTimePercent{{Nice: 10, User: 30, System: 20, Valid: true}}}
	w.Border = false
	w.SetRect(0, 0, 34, 3)
	buf := ui.NewBuffer(image.Rect(0, 0, 34, 3))
	w.Draw(buf)
	seen := map[ui.Color]bool{}
	for _, cell := range buf.Cells {
		if cell.Rune == '|' {
			seen[cell.Style.Fg] = true
		}
	}
	p := htopPalette()
	if !seen[p.nice] || !seen[p.user] || !seen[p.system] {
		t.Fatalf("missing segment color: %v", seen)
	}
	w.cores[0] = 100
	w.breakdown[0] = CPUTimePercent{User: 100, Valid: true}
	w.Draw(buf)
	w.cores[0] = 9
	w.breakdown[0] = CPUTimePercent{User: 9, Valid: true}
	w.Draw(buf)
	row := func() string {
		var b strings.Builder
		for _, c := range buf.Cells {
			b.WriteRune(c.Rune)
		}
		return b.String()
	}
	if !strings.Contains(row(), "9%") || strings.Contains(row(), "100%") || strings.Contains(row(), "9%0") {
		t.Fatalf("stale percentage: %q", row())
	}
	w.breakdown = nil
	w.Draw(buf)
	if strings.Contains(row(), "|") || !strings.Contains(row(), "--") {
		t.Fatalf("stale breakdown: %q", row())
	}
}
