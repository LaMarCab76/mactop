package app

import "math"

// CPUTimePercent holds disjoint percentages from one Mach CPU tick interval.
// Valid is false for the initial baseline, a reset, or an unavailable sample.
type CPUTimePercent struct {
	User, System, Nice float64
	Valid              bool
}

func finitePercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return max(0, min(100, value))
}

func cpuPercentagesFromTimes(previous, current []CPUUsage) ([]float64, []CPUTimePercent) {
	totals := make([]float64, len(current))
	parts := make([]CPUTimePercent, len(current))
	if len(previous) != len(current) {
		return totals, parts
	}
	for i, now := range current {
		old := previous[i]
		deltas := []float64{now.User - old.User, now.System - old.System, now.Nice - old.Nice, now.Idle - old.Idle}
		valid := true
		total := 0.0
		for _, delta := range deltas {
			if delta < 0 || math.IsNaN(delta) || math.IsInf(delta, 0) {
				valid = false
			}
			total += delta
		}
		if !valid || total <= 0 || math.IsInf(total, 0) {
			continue
		}
		parts[i] = CPUTimePercent{
			User: deltas[0] / total * 100, System: deltas[1] / total * 100,
			Nice: deltas[2] / total * 100, Valid: true,
		}
		totals[i] = finitePercent((deltas[0] + deltas[1] + deltas[2]) / total * 100)
	}
	return totals, parts
}

// Both the TUI and the compatibility wrapper consume this single sampler.
// The startup seed finishes before the collection goroutine is started.
func getCPUPercentagesAndBreakdown() ([]float64, []CPUTimePercent, error) {
	current, err := GetCPUUsage()
	if err != nil {
		lastCPUTimes, firstRun = nil, true
		return nil, nil, err
	}
	previous := lastCPUTimes
	if firstRun {
		previous = nil
	}
	totals, parts := cpuPercentagesFromTimes(previous, current)
	lastCPUTimes, firstRun = current, false
	return totals, parts, nil
}
