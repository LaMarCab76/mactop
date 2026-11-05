package app

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/metaspartan/mactop/v2/internal/i18n"
)

var visibleProcesses []ProcessMetrics
var killBirthTime int64
var lastGPUProcessBirth = map[int]int64{}

// A process can own many AGX clients. Invalid/overflowed clients invalidate
// that PID rather than silently under-counting it as a known zero.
func aggregateGPUClients(clients []GPUProcessStat) map[int]uint64 {
	result := make(map[int]uint64)
	invalid := make(map[int]bool)
	for _, client := range clients {
		if client.PID <= 0 || invalid[client.PID] {
			continue
		}
		previous := result[client.PID]
		if !client.Valid || ^uint64(0)-previous < client.GPUTimeNs {
			invalid[client.PID] = true
			delete(result, client.PID)
			continue
		}
		result[client.PID] = previous + client.GPUTimeNs
	}
	return result
}

func legacyProcessView(processes []ProcessMetrics) []ProcessMetrics {
	view := append([]ProcessMetrics(nil), processes...)
	sort.SliceStable(view, func(i, j int) bool { return view[i].CPU > view[j].CPU })
	if filterPID == 0 && len(view) > 500 {
		view = view[:500]
	}
	return view
}

// Compatibility for headless callers; the TUI collector publishes the full
// untruncated snapshot so memory/GPU leaders cannot disappear at the CPU cut.
func getProcessList(systemGPU float64) ([]ProcessMetrics, error) {
	all, err := getProcessSnapshot(systemGPU)
	return legacyProcessView(all), err
}

func isTop3Layout() bool {
	return isHtopMode() && effectiveLayout() == LayoutTop3 && btopProcessOptions().Mode != "list"
}

func top3Processes(all []ProcessMetrics, metric string, query string) ([]ProcessMetrics, int, int) {
	view := make([]ProcessMetrics, 0, len(all))
	eligible := 0
	for _, p := range all {
		if query != "" && !strings.Contains(strings.ToLower(p.Command), strings.ToLower(query)) {
			continue
		}
		eligible++
		value, valid := processRankValue(p, metric)
		if valid && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 {
			view = append(view, p)
		}
	}
	validCount := len(view)
	sort.Slice(view, func(i, j int) bool {
		a, _ := processRankValue(view[i], metric)
		b, _ := processRankValue(view[j], metric)
		if a == b {
			return view[i].PID < view[j].PID
		}
		return a > b
	})
	if len(view) > 3 {
		view = view[:3]
	}
	return view, validCount, eligible
}

func processRankValue(p ProcessMetrics, metric string) (float64, bool) {
	switch metric {
	case "memory":
		return float64(p.RSS), p.RSSValid
	case "gpu":
		return p.GPU, p.GPUValid
	default:
		return p.CPU, p.CPUValid
	}
}

func currentProcessView() ([]ProcessMetrics, int, int) {
	if isHtopMode() && btopProcessOptions().Mode == "list" {
		c := btopProcessOptions()
		view := make([]ProcessMetrics, 0, len(lastProcesses))
		for _, p := range lastProcesses {
			if (c.Name == "" || strings.Contains(strings.ToLower(p.Command), strings.ToLower(c.Name))) && (searchText == "" || strings.Contains(strings.ToLower(p.Command), strings.ToLower(searchText))) && (c.User == "" || strings.EqualFold(c.User, p.User)) {
				view = append(view, p)
			}
		}
		sortProcesses(view)
		return view, len(view), len(view)
	}
	if isTop3Layout() {
		return top3Processes(lastProcesses, htopConfig().TopMetric, searchText)
	}
	view := legacyProcessView(lastProcesses)
	if searchText != "" {
		filtered := view[:0]
		for _, p := range view {
			if strings.Contains(strings.ToLower(p.Command), strings.ToLower(searchText)) {
				filtered = append(filtered, p)
			}
		}
		view = filtered
	}
	sortProcesses(view)
	return view, len(view), len(view)
}

func top3Title(valid, total int) string {
	metric := map[string]string{"cpu": "CPU", "memory": "RES", "gpu": "GPU"}[htopConfig().TopMetric]
	title := fmt.Sprintf(" Top 3 · %s · ←/→ ", metric)
	if valid < total {
		title += fmt.Sprintf(i18n.T("Top3_Coverage"), valid, total)
	}
	if searchText != "" {
		title += " / " + searchText
	}
	if isFrozen {
		title += " · " + i18n.T("Top3_Frozen")
	}
	return title
}

func cycleTopMetric(step int) {
	metrics := []string{"cpu", "memory", "gpu"}
	index := 0
	for i, s := range metrics {
		if s == htopConfig().TopMetric {
			index = i
			break
		}
	}
	if currentConfig.Htop == nil {
		currentConfig.Htop = &HtopConfig{}
	}
	currentConfig.Htop.TopMetric = metrics[(index+step+len(metrics))%len(metrics)]
	updateProcessList()
	saveConfig()
}
