package app

import ui "github.com/metaspartan/gotui/v5"

const (
	LayoutDefault         = "default"
	LayoutTop3            = "top3"
	LayoutAlternative     = "alternative"
	LayoutAlternativeFull = "alternative_full"
	LayoutVertical        = "vertical"
	LayoutCompact         = "compact"
	LayoutDashboard       = "dashboard"
	LayoutGaugesOnly      = "gauges_only"
	LayoutGPUFocus        = "gpu_focus"
	LayoutCPUFocus        = "cpu_focus"
	LayoutSmall           = "small"
	LayoutNetworkIO       = "network_io"
	LayoutInfo            = "info"
	LayoutTiny            = "tiny"         // Compact layout with abbreviated stats + mini process list
	LayoutMicro           = "micro"        // Ultra-compact gauges + sparklines, no process list
	LayoutNano            = "nano"         // Dense info panel + small gauges + mini process list
	LayoutPico            = "pico"         // Maximum density with 2x2 gauges + sparklines
	LayoutHistory         = "history"      // StepChart history for GPU, Power, and Memory
	LayoutHistoryFull     = "history_full" // StepChart history including CPU
	LayoutHistorySoC      = "history_soc"  // StepChart history: CPU, GPU, ANE, DRAM Bandwidth
	LayoutFan             = "fan"          // Fan control and temperature sensors
	LayoutGPUMemory       = "gpu_memory"   // GPU + Memory focused with memory bandwidth chart
	LayoutPorts           = "ports"        // Listening TCP/UDP ports per process
	LayoutMemory          = "memory"       // Memory pressure, swap, DRAM bandwidth focused
)

var layoutOrder = []string{LayoutDefault, LayoutAlternative, LayoutAlternativeFull, LayoutVertical, LayoutCompact, LayoutDashboard, LayoutGaugesOnly, LayoutGPUFocus, LayoutCPUFocus, LayoutGPUMemory, LayoutMemory, LayoutNetworkIO, LayoutSmall, LayoutTiny, LayoutMicro, LayoutNano, LayoutPico, LayoutHistory, LayoutHistoryFull, LayoutHistorySoC, LayoutFan, LayoutPorts}

func setupGrid() {
	runtimeLayout = preferredLayout()
	lastActiveLayout = runtimeLayout
	updateLayoutNumber()
	applyLayout(effectiveLayout())
}

func cycleLayout(step int) {
	order := activeLayoutOrder()
	index := 0
	for i, l := range order {
		if l == effectiveLayout() {
			index = i
			break
		}
	}
	runtimeLayout = order[((index+step)%len(order)+len(order))%len(order)]
	temporaryLayout = false
	lastActiveLayout = runtimeLayout
	persistLayoutPreference(runtimeLayout)
	updateLayoutNumber()
	if !showHelp {
		applyLayout(runtimeLayout)
	}
	updateHelpText()
	updateProcessList()
}

func switchToLayout(layoutName string) {
	target, targetTemporary := layoutName, false
	targetStable := lastActiveLayout
	if effectiveLayout() == layoutName {
		target, targetTemporary = previousLayout, previousLayoutTemporary
		targetStable = previousStableLayout
		if target == "" || target == layoutName {
			target, targetTemporary = LayoutDefault, false
		}
	}
	previousLayout, previousLayoutTemporary, previousStableLayout = effectiveLayout(), temporaryLayout, lastActiveLayout
	runtimeLayout, temporaryLayout = target, targetTemporary
	if !temporaryLayout {
		lastActiveLayout = target
		persistLayoutPreference(target)
	} else {
		lastActiveLayout = targetStable
		if lastActiveLayout == "" {
			lastActiveLayout = LayoutDefault
		}
		persistLayoutPreference(lastActiveLayout)
	}
	updateLayoutNumber()
	if !showHelp {
		applyLayout(target)
	}
	updateHelpText()
	updateProcessList()
}

func applyLayout(layoutName string) {
	termWidth, termHeight := ui.TerminalDimensions()
	if mainBlock != nil {
		mainBlock.SetRect(0, 0, termWidth, termHeight)
		updateLayoutFooter(termWidth)
	}
	grid = ui.NewGrid()

	setLayoutGrid(layoutName)

	if termWidth > 2 && termHeight > 2 {
		grid.SetRect(1, 1, termWidth-1, termHeight-1)
	}
}

func setLayoutGrid(layoutName string) {
	if isHtopMode() && stringIn(layoutName, []string{LayoutDefault, LayoutTop3, LayoutGPUFocus, LayoutEnergy, LayoutProcesses, LayoutNetworkIO}) {
		grid.Set(ui.NewRow(1.0, &btopDashboard{ui.NewBlock()}))
		return
	}
	if layoutName == LayoutTop3 && isHtopMode() {
		setHtopDefaultLayoutGrid()
		return
	}
	if setCompactLikeLayout(layoutName) {
		return
	}
	if setter, ok := namedLayoutSetters[layoutName]; ok {
		setter()
		return
	}
	setDefaultLayoutGrid()
}

func setCompactLikeLayout(layoutName string) bool {
	switch layoutName {
	case LayoutTiny, LayoutMicro, LayoutNano, LayoutPico:
		setCompactLayoutGrid(layoutName)
		return true
	case LayoutInfo, LayoutFan:
		setInfoFanLayoutGrid(layoutName)
		return true
	case LayoutHistory, LayoutHistoryFull, LayoutGPUMemory:
		setHistoryLikeLayoutGrid(layoutName)
		return true
	default:
		return false
	}
}

var namedLayoutSetters = map[string]func(){
	LayoutPorts:           setPortsLayoutGrid,
	LayoutMemory:          setMemoryLayoutGrid,
	LayoutHistorySoC:      setHistorySoCLayoutGrid,
	LayoutAlternative:     setAlternativeLayoutGrid,
	LayoutAlternativeFull: setAlternativeFullLayoutGrid,
	LayoutVertical:        setVerticalLayoutGrid,
	LayoutCompact:         setCompactMainLayoutGrid,
	LayoutDashboard:       setDashboardLayoutGrid,
	LayoutGaugesOnly:      setGaugesOnlyLayoutGrid,
	LayoutGPUFocus:        setGPUFocusLayoutGrid,
	LayoutCPUFocus:        setCPUFocusLayoutGrid,
	LayoutNetworkIO:       setNetworkIOLayoutGrid,
	LayoutSmall:           setSmallLayoutGrid,
}

func setMemoryLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/5,
			ui.NewCol(1.0/2, memoryGauge),
			ui.NewCol(1.0/2, memoryPressurePanel),
		),
		ui.NewRow(1.0/5,
			ui.NewCol(1.0/2, memoryHistoryChart),
			ui.NewCol(1.0/2, memoryPressureHistoryChart),
		),
		ui.NewRow(1.0/5,
			ui.NewCol(1.0, memBWHistoryChart),
		),
		ui.NewRow(2.0/5,
			ui.NewCol(1.0, processList),
		),
	)
}

func setAlternativeLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/2,
			ui.NewCol(1.0/2, cpuCoreWidget),
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, gpuGauge),
				ui.NewCol(1.0, ui.NewRow(1.0, memoryGauge)),
			),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/6, modelText),
			ui.NewCol(1.0/3, NetworkInfo),
			ui.NewCol(1.0/4, PowerChart),
			ui.NewCol(1.0/4, sparklineGroup),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setAlternativeFullLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, cpuCoreWidget),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/2, gpuGauge),
			ui.NewCol(1.0/2, memoryGauge),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/6, modelText),
			ui.NewCol(1.0/3, NetworkInfo),
			ui.NewCol(1.0/4, PowerChart),
			ui.NewCol(1.0/4, sparklineGroup),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setVerticalLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0,
			ui.NewCol(0.4,
				ui.NewRow(1.0/8, cpuGauge),
				ui.NewRow(1.0/8, gpuGauge),
				ui.NewRow(1.0/8, aneGauge),
				ui.NewRow(1.5/8, memoryGauge),
				ui.NewRow(1.5/8, NetworkInfo),
				ui.NewRow(2.0/8, modelText),
			),
			ui.NewCol(0.6,
				ui.NewRow(3.0/4, processList),
				ui.NewRow(1.0/4,
					ui.NewCol(1.0/2, PowerChart),
					ui.NewCol(1.0/2, sparklineGroup),
				),
			),
		),
	)
}

func setCompactMainLayoutGrid() {
	grid.Set(
		ui.NewRow(2.0/8,
			ui.NewCol(1.0/4, cpuGauge),
			ui.NewCol(1.0/4, gpuGauge),
			ui.NewCol(1.0/4, memoryGauge),
			ui.NewCol(1.0/4, aneGauge),
		),
		ui.NewRow(2.0/8,
			ui.NewCol(1.0/3, modelText),
			ui.NewCol(1.0/3, NetworkInfo),
			ui.NewCol(1.0/3, PowerChart),
		),
		ui.NewRow(2.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setDashboardLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/4, cpuGauge),
			ui.NewCol(1.0/4, gpuGauge),
			ui.NewCol(1.0/4, memoryGauge),
			ui.NewCol(1.0/4, aneGauge),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/2, sparklineGroup),
			ui.NewCol(1.0/2, gpuSparklineGroup),
		),
		ui.NewRow(2.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setGaugesOnlyLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/3,
			ui.NewCol(1.0/2, cpuGauge),
			ui.NewCol(1.0/2, memoryGauge),
		),
		ui.NewRow(1.0/3,
			ui.NewCol(1.0/2, gpuGauge),
			ui.NewCol(1.0/2, aneGauge),
		),
		ui.NewRow(1.0/3,
			ui.NewCol(1.0/2, gpuSparklineGroup),
			ui.NewCol(1.0/2, sparklineGroup),
		),
	)
}

func setGPUFocusLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/2, gpuGauge),
			ui.NewCol(1.0/2, gpuSparklineGroup),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/4, cpuGauge),
			ui.NewCol(1.0/4, memoryGauge),
			ui.NewCol(1.0/4, NetworkInfo),
			ui.NewCol(1.0/4, modelText),
		),
		ui.NewRow(2.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setCPUFocusLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/3,
			ui.NewCol(1.0/3, cpuGauge),
			ui.NewCol(2.0/3, cpuCoreWidget),
		),
		ui.NewRow(1.0/6,
			ui.NewCol(1.0/4, gpuGauge),
			ui.NewCol(1.0/4, memoryGauge),
			ui.NewCol(1.0/4, sparklineGroup),
			ui.NewCol(1.0/4, PowerChart),
		),
		ui.NewRow(3.0/6,
			ui.NewCol(1.0, processList),
		),
	)
}

func setNetworkIOLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/3, gpuSparklineGroup),
			ui.NewCol(1.0/3, sparklineGroup),
			ui.NewCol(1.0/3, NetworkInfo),
		),
		ui.NewRow(2.0/4,
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, gpuGauge),
				ui.NewRow(1.0/2, memoryGauge),
			),
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, tbInfoParagraph),
				ui.NewRow(1.0/2, tbNetSparklineGroup),
			),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setSmallLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0,
			ui.NewCol(1.0,
				ui.NewRow(1.0/4, cpuGauge),
				ui.NewRow(1.0/4, gpuGauge),
				ui.NewRow(1.0/4, memoryGauge),
				ui.NewRow(1.0/4, aneGauge),
			),
		),
	)
}

func setDefaultLayoutGrid() {
	if isHtopMode() {
		setHtopDefaultLayoutGrid()
		return
	}
	grid.Set(
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/2, cpuGauge),
			ui.NewCol(1.0/2, gpuGauge),
		),
		ui.NewRow(2.0/4,
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, aneGauge),
				ui.NewRow(1.0/2,
					ui.NewCol(1.0/2, PowerChart),
					ui.NewCol(1.0/2, sparklineGroup),
				),
			),
			ui.NewCol(1.0/2,
				ui.NewRow(1.0/2, memoryGauge),
				ui.NewRow(1.0/2,
					ui.NewCol(1.0/3, modelText),
					ui.NewCol(2.0/3, NetworkInfo),
				),
			),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setHtopDefaultLayoutGrid() {
	width, height := GetCachedTerminalDimensions()
	innerHeight := max(1, height-2)
	if height < 18 {
		grid.Set(ui.NewRow(0.55, cpuCoreWidget), ui.NewRow(0.45, processList))
		return
	}
	cols := htopCoreColumns(max(1, width-4))
	coreHeight := min(max(5, (len(cpuCoreWidget.cores)+cols-1)/cols+2), max(5, innerHeight-14))
	// gotui truncates fractional grid edges. Bias the first edge by half a
	// cell so rounding error cannot turn an exact N-row panel into N-1 rows.
	// The later fixed-height rows inherit the same half-cell bias.
	coreFraction := (float64(coreHeight) + 0.5) / float64(innerHeight)
	gaugeFraction, infoFraction := 4.0/float64(innerHeight), 6.0/float64(innerHeight)
	grid.Set(
		ui.NewRow(coreFraction, cpuCoreWidget),
		ui.NewRow(gaugeFraction, ui.NewCol(1.0/3, gpuGauge), ui.NewCol(1.0/3, memoryGauge), ui.NewCol(1.0/3, aneGauge)),
		ui.NewRow(infoFraction, ui.NewCol(0.4, PowerChart), ui.NewCol(0.6, NetworkInfo)),
		ui.NewRow(1-coreFraction-gaugeFraction-infoFraction, processList),
	)
}

func setCompactLayoutGrid(layoutName string) {
	switch layoutName {
	case LayoutTiny:
		// Compact vertical with all key metrics + mini process list
		grid.Set(
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, cpuGauge),
				ui.NewCol(1.0/2, gpuGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, memoryGauge),
				ui.NewCol(1.0/2, aneGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, PowerChart),
				ui.NewCol(1.0/2, NetworkInfo),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, processList),
			),
		)
	case LayoutMicro:
		// Ultra-compact gauges + sparklines, no process list
		grid.Set(
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, cpuGauge),
				ui.NewCol(1.0/2, gpuGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, memoryGauge),
				ui.NewCol(1.0/2, aneGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, sparklineGroup),
				ui.NewCol(1.0/2, gpuSparklineGroup),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, PowerChart),
				ui.NewCol(1.0/2, NetworkInfo),
			),
		)
	case LayoutNano:
		// Dense info panel + gauges + mini process list
		grid.Set(
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, cpuGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/2, gpuGauge),
				ui.NewCol(1.0/2, memoryGauge),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0/3, aneGauge),
				ui.NewCol(1.0/3, PowerChart),
				ui.NewCol(1.0/3, NetworkInfo),
			),
			ui.NewRow(1.0/4,
				ui.NewCol(1.0, processList),
			),
		)
	case LayoutPico:
		// Maximum density with 2x2 gauges + sparklines
		grid.Set(
			ui.NewRow(1.0/3,
				ui.NewCol(1.0/4, cpuGauge),
				ui.NewCol(1.0/4, gpuGauge),
				ui.NewCol(1.0/4, memoryGauge),
				ui.NewCol(1.0/4, aneGauge),
			),
			ui.NewRow(1.0/3,
				ui.NewCol(1.0/2, sparklineGroup),
				ui.NewCol(1.0/2, gpuSparklineGroup),
			),
			ui.NewRow(1.0/3,
				ui.NewCol(1.0/3, PowerChart),
				ui.NewCol(1.0/3, NetworkInfo),
				ui.NewCol(1.0/3, modelText),
			),
		)
	}
}

func setInfoFanLayoutGrid(layoutName string) {
	if layoutName == LayoutFan {
		grid.Set(
			ui.NewRow(0.92,
				ui.NewCol(0.5, fanStatusPanel),
				ui.NewCol(0.5, fanTempPanel),
			),
			ui.NewRow(0.08,
				ui.NewCol(1.0, fanControlPanel),
			),
		)
	} else {
		grid.Set(
			ui.NewRow(1.0,
				ui.NewCol(1.0, infoParagraph),
			),
		)
	}
}

func setHistoryLikeLayoutGrid(layoutName string) {
	switch layoutName {
	case LayoutHistoryFull:
		setHistoryFullLayoutGrid()
	case LayoutGPUMemory:
		setGPUMemoryLayoutGrid()
	default:
		setHistoryLayoutGrid()
	}
}

func setHistoryLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/3,
			ui.NewCol(1.0, gpuHistoryChart),
		),
		ui.NewRow(1.0/3,
			ui.NewCol(1.0/2, powerHistoryChart),
			ui.NewCol(1.0/2, memoryHistoryChart),
		),
		ui.NewRow(1.0/3,
			ui.NewCol(1.0, processList),
		),
	)
}

func setGPUMemoryLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/2, gpuGauge),
			ui.NewCol(1.0/2, memoryGauge),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0/2, gpuSparklineGroup),
			ui.NewCol(1.0/2, memoryHistoryChart),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, memBWHistoryChart),
		),
		ui.NewRow(1.0/4,
			ui.NewCol(1.0, processList),
		),
	)
}

func setHistoryFullLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/3,
			ui.NewCol(1.0/2, cpuHistoryChart),
			ui.NewCol(1.0/2, gpuHistoryChart),
		),
		ui.NewRow(1.0/3,
			ui.NewCol(1.0/2, powerHistoryChart),
			ui.NewCol(1.0/2, memoryHistoryChart),
		),
		ui.NewRow(1.0/3,
			ui.NewCol(1.0, processList),
		),
	)
}

func setPortsLayoutGrid() {
	grid.Set(
		ui.NewRow(1.0/5,
			ui.NewCol(1.0/3, NetworkInfo),
			ui.NewCol(1.0/3, modelText),
			ui.NewCol(1.0/3, PowerChart),
		),
		ui.NewRow(4.0/5,
			ui.NewCol(1.0, processList),
		),
	)
}

func setHistorySoCLayoutGrid() {
	// SoC History layout for ANE/ML workloads (all charts, no process list):
	// Row 1: CPU + GPU
	// Row 2: ANE + SoC Power (rightmost)
	// Row 3 (bottom): Memory BW (left) | Memory Used | SSD Read
	// Rows scaled ×1.25 from the former 0.24/0.24/0.32 to reclaim the height
	// the process list used to occupy.
	grid.Set(
		ui.NewRow(0.30,
			ui.NewCol(1.0/2, cpuHistoryChart),
			ui.NewCol(1.0/2, gpuHistoryChart),
		),
		ui.NewRow(0.30,
			ui.NewCol(1.0/2, aneHistoryChart),
			ui.NewCol(1.0/2, socPowerHistoryChart),
		),
		ui.NewRow(0.40,
			ui.NewCol(1.0/3, bandwidthHistoryChart), // Memory BW leftmost
			ui.NewCol(1.0/3, memoryHistoryChart),
			ui.NewCol(1.0/3, ssdReadHistoryChart),
		),
	)

	// Force orange for memory graph specifically in history_soc
	if effectiveLayout() == LayoutHistorySoC && memoryHistoryChart != nil {
		memoryHistoryChart.LineColors = []ui.Color{ui.ColorOrange, ui.ColorMagenta}
	}
}
