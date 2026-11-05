package app

/*
#include <sys/sysctl.h>
#include <pwd.h>
#include <unistd.h>
#include <libproc.h>
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <mach/mach_init.h>
#include <mach/mach_time.h>

static inline time_t get_proc_starttime(struct kinfo_proc *kp) {
    return kp->kp_proc.p_un.__p_starttime.tv_sec * 1000000LL + kp->kp_proc.p_un.__p_starttime.tv_usec;
}

extern kern_return_t vm_deallocate(vm_map_t target_task, vm_address_t address, vm_size_t size);
*/
import "C"
import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"syscall"
	"time"
	"unsafe"

	"github.com/mattn/go-runewidth"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

var uidCache = make(map[uint32]string)
var uidCacheMutex sync.RWMutex

func getUsername(uid uint32) string {
	uidCacheMutex.RLock()
	name, ok := uidCache[uid]
	uidCacheMutex.RUnlock()
	if ok {
		return name
	}

	uidCacheMutex.Lock()
	defer uidCacheMutex.Unlock()

	// Double check
	if name, ok := uidCache[uid]; ok {
		return name
	}

	// Use C.getpwuid
	pwd := C.getpwuid(C.uid_t(uid))
	if pwd != nil {
		name = C.GoString(pwd.pw_name)
	} else {
		name = fmt.Sprintf("%d", uid)
	}
	uidCache[uid] = name
	return name
}

type ProcessTimeState struct {
	Time      uint64
	Timestamp time.Time
	Command   string
	CreateSec int64
	Valid     bool
}

var prevProcessTimes = make(map[int]ProcessTimeState)
var prevProcessTimesMutex sync.Mutex

var timebaseInfo C.mach_timebase_info_data_t
var timebaseOnce sync.Once

func getTimebase() {
	C.mach_timebase_info(&timebaseInfo)
}

func processOsProc(kp C.struct_kinfo_proc, now time.Time, prevProcessTimes map[int]ProcessTimeState, totalMem uint64, numer, denom uint64) (ProcessMetrics, int, ProcessTimeState, bool) {
	pid := int(kp.kp_proc.p_pid)
	if pid == 0 {
		return ProcessMetrics{}, 0, ProcessTimeState{}, false
	}

	comm := C.GoString(&kp.kp_proc.p_comm[0])
	createSec := int64(C.get_proc_starttime(&kp))

	// Fast path: reuse full command name to avoid heavy proc_pidpath syscall on every tick.
	// p_comm is a 16-byte truncation of the full name, so check if cached command starts with it.
	if prevState, ok := prevProcessTimes[pid]; ok && prevState.CreateSec == createSec && prevState.Command != "" && strings.HasPrefix(prevState.Command, comm) {
		comm = prevState.Command
	} else {
		var pathBuf [C.PROC_PIDPATHINFO_MAXSIZE]C.char
		if C.proc_pidpath(C.int(pid), unsafe.Pointer(&pathBuf), C.PROC_PIDPATHINFO_MAXSIZE) > 0 {
			fullPath := C.GoString(&pathBuf[0])
			comm = filepath.Base(fullPath)
		}
	}

	rssBytes := int64(0)
	vszBytes := int64(0)
	totalTimeNs := uint64(0)

	var taskInfo C.struct_proc_taskinfo
	ret := C.proc_pidinfo(C.int(pid), C.PROC_PIDTASKINFO, 0, unsafe.Pointer(&taskInfo), C.int(C.sizeof_struct_proc_taskinfo))
	if ret == C.int(C.sizeof_struct_proc_taskinfo) {
		rssBytes = int64(taskInfo.pti_resident_size)
		vszBytes = int64(taskInfo.pti_virtual_size)
		rawTime := uint64(taskInfo.pti_total_user) + uint64(taskInfo.pti_total_system)
		totalTimeNs = (rawTime * numer) / denom
	}

	taskValid := ret == C.int(C.sizeof_struct_proc_taskinfo)
	cpuPercent, cpuValid := 0.0, false
	if previous, ok := prevProcessTimes[pid]; ok && taskValid && previous.Valid && previous.CreateSec == createSec && totalTimeNs >= previous.Time {
		elapsed := now.Sub(previous.Timestamp).Seconds()
		if elapsed > 0 {
			cpuPercent = float64(totalTimeNs-previous.Time) / elapsed / 1e9 * 100
			cpuValid = true
		}
	}

	newState := ProcessTimeState{
		Time:      totalTimeNs,
		Timestamp: now,
		Command:   comm,
		CreateSec: createSec,
		Valid:     taskValid,
	}

	memPercent := 0.0
	if totalMem > 0 {
		memPercent = (float64(rssBytes) / float64(totalMem)) * 100.0
	}

	state := processStateString(kp.kp_proc.p_stat)

	uid := uint32(kp.kp_eproc.e_ucred.cr_uid)
	user := getUsername(uid)

	totalSeconds := float64(totalTimeNs) / 1e9
	timeStr := formatTime(totalSeconds)

	pm := ProcessMetrics{
		PID:         pid,
		User:        user,
		CPU:         cpuPercent,
		Memory:      memPercent,
		VSZ:         vszBytes / 1024,
		RSS:         rssBytes / 1024,
		Command:     comm,
		State:       state,
		Started:     "",
		Time:        timeStr,
		LastUpdated: now,
		BirthTime:   createSec, CPUValid: cpuValid, RSSValid: taskValid, MemoryPercentValid: taskValid && totalMem > 0,
	}
	return pm, pid, newState, true
}

func processStateString(stat C.char) string {
	switch stat {
	case C.SIDL:
		return "I"
	case C.SRUN:
		return "R"
	case C.SSLEEP:
		return "S"
	case C.SSTOP:
		return "T"
	case C.SZOMB:
		return "Z"
	default:
		return "?"
	}
}

func getProcessSnapshot(systemGpuPercent float64) ([]ProcessMetrics, error) {
	var mib []C.int
	var mibLen C.u_int

	if filterPID > 0 {
		// Fetch only the specific process by PID
		mib = []C.int{C.CTL_KERN, C.KERN_PROC, C.KERN_PROC_PID, C.int(filterPID)}
		mibLen = 4
	} else {
		// Fetch all processes
		mib = []C.int{C.CTL_KERN, C.KERN_PROC, C.KERN_PROC_ALL}
		mibLen = 3
	}
	var size C.size_t

	if _, err := C.sysctl(&mib[0], mibLen, nil, &size, nil, 0); err != nil {
		return nil, fmt.Errorf("sysctl size check failed: %v", err)
	}

	if size == 0 {
		// PID not found or no processes
		return nil, nil
	}

	buf := make([]byte, size)
	if _, err := C.sysctl(&mib[0], mibLen, unsafe.Pointer(&buf[0]), &size, nil, 0); err != nil {
		return nil, fmt.Errorf("sysctl fetch failed: %v", err)
	}

	count := int(size) / int(C.sizeof_struct_kinfo_proc)
	kprocs := (*[1 << 30]C.struct_kinfo_proc)(unsafe.Pointer(&buf[0]))[:count:count]

	var processes []ProcessMetrics
	now := time.Now()

	prevProcessTimesMutex.Lock()
	defer prevProcessTimesMutex.Unlock()

	nextProcessTimes := make(map[int]ProcessTimeState)

	mibMem := []C.int{6, 24}
	var memSize C.uint64_t
	memLen := C.size_t(unsafe.Sizeof(memSize))
	totalMem := uint64(0)
	if _, err := C.sysctl(&mibMem[0], 2, unsafe.Pointer(&memSize), &memLen, nil, 0); err == nil {
		totalMem = uint64(memSize)
	}

	timebaseOnce.Do(getTimebase)
	numer := uint64(timebaseInfo.numer)
	denom := uint64(timebaseInfo.denom)
	if denom == 0 {
		denom = 1
	}

	for _, kp := range kprocs {
		pm, pid, ns, ok := processOsProc(kp, now, prevProcessTimes, totalMem, numer, denom)
		if ok {
			processes = append(processes, pm)
			nextProcessTimes[pid] = ns
		}
	}

	prevProcessTimes = nextProcessTimes

	updateProcessGPUMetrics(processes, now, systemGpuPercent)

	return processes, nil
}

// updateProcessGPUMetrics calculates per-process GPU usage and updates process metrics
func updateProcessGPUMetrics(processes []ProcessMetrics, now time.Time, systemGpuPercent float64) {
	gpuProcessStatsMutex.Lock()
	defer gpuProcessStatsMutex.Unlock()

	currentGPUStats := GetGPUProcessStats()
	gpuElapsed := now.Sub(lastGPUProcessStatsTime).Seconds()
	gpuMsPerSec := make(map[int]float64)
	var totalRawGpuMs float64
	if currentGPUStats != nil && lastGPUProcessStats != nil && gpuElapsed > 0 {
		for pid, currentTime := range currentGPUStats {
			if previous, ok := lastGPUProcessStats[pid]; ok && currentTime >= previous {
				gpuMs := float64(currentTime-previous) / gpuElapsed / 1_000_000
				gpuMsPerSec[pid] = gpuMs
				totalRawGpuMs += gpuMs
			}
		}
	}

	rawTotalPercent := totalRawGpuMs / 10.0

	scaleFactor := 1.0
	if rawTotalPercent > 0.01 && systemGpuPercent > 0.01 {
		scaleFactor = systemGpuPercent / rawTotalPercent
	}

	for i := range processes {
		if gpuMs, ok := gpuMsPerSec[processes[i].PID]; ok && lastGPUProcessBirth[processes[i].PID] == processes[i].BirthTime {
			processes[i].GPU = gpuMs * scaleFactor
			processes[i].GPUValid = true
		}
	}

	lastGPUProcessBirth = make(map[int]int64, len(processes))
	for _, p := range processes {
		lastGPUProcessBirth[p.PID] = p.BirthTime
	}
	lastGPUProcessStats = currentGPUStats
	lastGPUProcessStatsTime = now
}

func GetCPUUsage() ([]CPUUsage, error) {
	var numCPUs C.natural_t
	var cpuLoad *C.processor_cpu_load_info_data_t
	var cpuMsgCount C.mach_msg_type_number_t
	host := C.mach_host_self()
	kernReturn := C.host_processor_info(
		host,
		C.PROCESSOR_CPU_LOAD_INFO,
		&numCPUs,
		(*C.processor_info_array_t)(unsafe.Pointer(&cpuLoad)),
		&cpuMsgCount,
	)
	if kernReturn != C.KERN_SUCCESS {
		return nil, fmt.Errorf("error getting CPU info: %d", kernReturn)
	}
	defer C.vm_deallocate(
		C.mach_task_self_,
		(C.vm_address_t)(uintptr(unsafe.Pointer(cpuLoad))),
		C.vm_size_t(cpuMsgCount)*C.sizeof_processor_cpu_load_info_data_t,
	)
	cpuLoadInfo := (*[1 << 30]C.processor_cpu_load_info_data_t)(unsafe.Pointer(cpuLoad))[:numCPUs:numCPUs]
	cpuUsage := make([]CPUUsage, numCPUs)
	for i := 0; i < int(numCPUs); i++ {
		cpuUsage[i] = CPUUsage{
			User:   float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_USER]),
			System: float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_SYSTEM]),
			Idle:   float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_IDLE]),
			Nice:   float64(cpuLoadInfo[i].cpu_ticks[C.CPU_STATE_NICE]),
		}
	}
	return cpuUsage, nil
}

func getThemeColorName(themeColor ui.Color) string {
	switch themeColor {
	case ui.ColorBlack:
		return "black"
	case ui.ColorRed:
		return "red"
	case ui.ColorGreen:
		return "green"
	case ui.ColorYellow:
		return "yellow"
	case ui.ColorBlue:
		return "blue"
	case ui.ColorMagenta:
		return "magenta"
	case ui.ColorSkyBlue:
		return "skyblue"
	case ui.ColorGold:
		return "gold"
	case ui.ColorSilver:
		return "silver"
	case ui.ColorWhite:
		return "white"
	case ui.ColorLime:
		return "lime"
	case ui.ColorOrange:
		return "orange"
	case ui.ColorViolet:
		return "violet"
	case ui.ColorPink:
		return "pink"
	default:
		return "white"
	}
}

func sortProcesses(processes []ProcessMetrics) {
	sort.Slice(processes, func(i, j int) bool {
		var less bool
		var equal bool

		switch columns[selectedColumn] {
		case "PID":
			less = processes[i].PID < processes[j].PID
			equal = processes[i].PID == processes[j].PID
		case "USER":
			u1, u2 := strings.ToLower(processes[i].User), strings.ToLower(processes[j].User)
			less = u1 < u2
			equal = u1 == u2
		case "VIRT":
			less = processes[i].VSZ > processes[j].VSZ // Descending default
			equal = processes[i].VSZ == processes[j].VSZ
		case "RES":
			less = processes[i].RSS > processes[j].RSS // Descending default
			equal = processes[i].RSS == processes[j].RSS
		case "CPU":
			less = processes[i].CPU > processes[j].CPU // Descending default
			equal = processes[i].CPU == processes[j].CPU
		case "GPU":
			less = processes[i].GPU > processes[j].GPU // Descending default
			equal = processes[i].GPU == processes[j].GPU
		case "MEM":
			less = processes[i].Memory > processes[j].Memory // Descending default
			equal = processes[i].Memory == processes[j].Memory
		case "TIME":
			iTime := parseTimeString(processes[i].Time)
			jTime := parseTimeString(processes[j].Time)
			less = iTime > jTime // Descending default
			equal = iTime == jTime
		case "CMD":
			c1, c2 := strings.ToLower(processes[i].Command), strings.ToLower(processes[j].Command)
			less = c1 < c2
			equal = c1 == c2
		default:
			less = processes[i].CPU > processes[j].CPU
			equal = processes[i].CPU == processes[j].CPU
		}

		if equal {
			// Secondary sort by PID (always ascending) to ensure stability
			return processes[i].PID < processes[j].PID
		}

		if sortReverse {
			return !less
		}
		return less
	})
}

func calculateMaxWidths(availableWidth int) map[string]int {
	maxWidths := map[string]int{
		"PID":  5,
		"USER": 8,
		"VIRT": 6,
		"RES":  6,
		"CPU":  6,
		"GPU":  6,
		"MEM":  5,
		"TIME": 11,
		"CMD":  15,
	}
	usedWidth := 0
	for col, width := range maxWidths {
		if col != "CMD" {
			usedWidth += width + 1
		}
	}

	cmdWidth := availableWidth - usedWidth
	if cmdWidth < 5 {
		cmdWidth = 5
	}
	maxWidths["CMD"] = cmdWidth
	return maxWidths
}

func buildHeader(maxWidths map[string]int, themeColorStr, selectedHeaderFg string) string {
	return buildHeaderWithSort(maxWidths, themeColorStr, selectedHeaderFg, selectedColumn, sortReverse)
}

func buildHeaderWithSort(maxWidths map[string]int, themeColorStr, selectedHeaderFg string, sortColumn int, reverse bool) string {
	header := ""
	for i, col := range columns {
		width := maxWidths[col]

		// Determine arrow for selected column
		arrow := ""
		if i == sortColumn {
			arrow = "↓"
			if reverse {
				arrow = "↑"
			}
		}

		// Build column text with arrow included in width
		colWithArrow := i18n.T("Process_"+col) + arrow

		w := runewidth.StringWidth(colWithArrow)
		padding := width - w
		if padding < 0 {
			padding = 0
		}

		colText := ""
		switch col {
		case "USER", "CMD": // Left-align
			colText = colWithArrow + strings.Repeat(" ", padding)
		default: // Right-align
			colText = strings.Repeat(" ", padding) + colWithArrow
		}

		header += fmt.Sprintf("[%s](fg:%s,bg:%s)", colText, selectedHeaderFg, themeColorStr)

		if i < len(columns)-1 {
			header += fmt.Sprintf("[%s](fg:%s,bg:%s)", "|", selectedHeaderFg, themeColorStr)
		}
	}
	return header
}

func buildProcessRows(processes []ProcessMetrics, maxWidths map[string]int) []string {
	items := make([]string, len(processes))
	for i, p := range processes {
		seconds := parseTimeString(p.Time)
		timeStr := formatTime(seconds)
		virtStr := formatMemorySize(p.VSZ)
		resStr := formatResMemorySize(p.RSS)
		username := runewidth.Truncate(p.User, maxWidths["USER"], "...")

		cmdName := p.Command // Already simplified by ps -c

		// Convert GPU from ms/s to percentage (ms/s / 10 = %)
		// 1000 ms/s = 100% GPU utilization
		gpuPercent := p.GPU / 10.0

		cpuText, gpuText, memText := fmt.Sprintf("%.1f%%", p.CPU), fmt.Sprintf("%.1f%%", gpuPercent), fmt.Sprintf("%.1f%%", p.Memory)
		if isHtopMode() {
			if !p.RSSValid {
				virtStr, resStr, timeStr = "--", "--", "--"
			}
			if !p.CPUValid {
				cpuText = "--"
			}
			if !p.GPUValid {
				gpuText = "--"
			}
			if !p.MemoryPercentValid {
				memText = "--"
			}
		}
		line := fmt.Sprintf("%*d %-*s %*s %*s %*s %*s %*s %*s %-s",
			maxWidths["PID"], p.PID,
			maxWidths["USER"], username,
			maxWidths["VIRT"], virtStr,
			maxWidths["RES"], resStr,
			maxWidths["CPU"], cpuText,
			maxWidths["GPU"], gpuText,
			maxWidths["MEM"], memText,
			maxWidths["TIME"], timeStr,
			runewidth.Truncate(cmdName, maxWidths["CMD"], "..."),
		)

		if i == processList.SelectedRow-1 {
			items[i] = line
		} else if currentUser != "" && currentUser != "root" && p.User != currentUser {
			color := GetProcessTextColor(false)
			items[i] = fmt.Sprintf("[%s](fg:%s)", line, color)
		} else {
			color := GetProcessTextColor(true)
			items[i] = fmt.Sprintf("[%s](fg:%s)", line, color)
		}
	}
	return items
}

func updateProcessList() {
	refreshBtopProcesses()
	if isPortsLayoutActive() {
		updatePortsList()
		return
	}

	if processList == nil {
		return
	}
	var selected ProcessMetrics
	if row := processList.SelectedRow - 1; row >= 0 && row < len(visibleProcesses) {
		selected = visibleProcesses[row]
	}
	processes, valid, total := currentProcessView()
	visibleProcesses = append([]ProcessMetrics(nil), processes...)
	if selected.PID > 0 {
		if isHtopMode() && btopProcessOptions().Mode == "list" {
			processList.SelectedRow = 0
		}
		for i, p := range processes {
			if p.PID == selected.PID && p.BirthTime == selected.BirthTime {
				processList.SelectedRow = i + 1
				break
			}
		}
	}
	processList.SelectedRow = max(0, min(processList.SelectedRow, len(processes)))

	themeColorStr, selectedHeaderFg := resolveProcessThemeColor()

	termWidth, _ := GetCachedTerminalDimensions()
	availableWidth := termWidth - 2
	if availableWidth < 1 {
		availableWidth = 1
	}

	maxWidths := calculateMaxWidths(availableWidth)

	header := buildHeader(maxWidths, themeColorStr, selectedHeaderFg)
	if isTop3Layout() {
		column := map[string]int{"cpu": 4, "memory": 3, "gpu": 5}[htopConfig().TopMetric]
		header = buildHeaderWithSort(maxWidths, themeColorStr, selectedHeaderFg, column, false)
	}
	rows := buildProcessRows(processes, maxWidths)

	items := make([]string, len(processes)+1)
	items[0] = header
	copy(items[1:], rows)

	processList.Title, processList.TitleStyle = getProcessListTitle()
	if isTop3Layout() {
		processList.Title = top3Title(valid, total)
		if len(processes) == 0 {
			items = append(items, i18n.T("Top3_NoData"))
		}
	}
	processList.Rows = items
}

func handleSearchInput(e ui.Event) {
	switch e.ID {
	case "<Escape>":
		searchMode = false
		searchText = ""
		filteredProcesses = nil
		filteredPorts = nil
		updateProcessList()
	case "<Enter>":
		searchMode = false
		updateProcessList()
	case "<F9>":
		attemptKillProcess()
	case "<Backspace>":
		if len(searchText) > 0 {
			runes := []rune(searchText)
			searchText = string(runes[:len(runes)-1])
		}
		if isPortsLayoutActive() {
			updateFilteredPorts()
		} else {
			updateFilteredProcesses()
		}
		updateProcessList()
	case "<Space>":
		searchText += " "
		if isPortsLayoutActive() {
			updateFilteredPorts()
		} else {
			updateFilteredProcesses()
		}
		updateProcessList()
	default:
		// Only append printable characters (simple check)
		if len(e.ID) == 1 || (isHtopMode() && !strings.HasPrefix(e.ID, "<") && utf8.ValidString(e.ID)) {
			searchText += e.ID
			if isPortsLayoutActive() {
				updateFilteredPorts()
			} else {
				updateFilteredProcesses()
			}
			updateProcessList()
		}
	}
}

func refreshFilteredProcesses() {
	if isPortsLayoutActive() {
		refreshFilteredPorts()
		return
	}
	if searchText == "" {
		filteredProcesses = nil
		return
	}
	filteredProcesses = nil
	lowerText := strings.ToLower(searchText)
	for _, p := range lastProcesses {
		if strings.Contains(strings.ToLower(p.Command), lowerText) {
			filteredProcesses = append(filteredProcesses, p)
		}
	}
}

func updateFilteredProcesses() {
	if isPortsLayoutActive() {
		updateFilteredPorts()
		return
	}
	refreshFilteredProcesses()
	if len(filteredProcesses) > 0 {
		processList.SelectedRow = 1
	} else {
		processList.SelectedRow = 0
	}
}

func updateKillModal() {
	termWidth, termHeight := GetCachedTerminalDimensions()
	modalWidth := 50
	modalHeight := 10 // Slightly taller for the buttons provided by widget

	x := (termWidth - modalWidth) / 2
	y := (termHeight - modalHeight) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	confirmModal.SetRect(x, y, x+modalWidth, y+modalHeight)

	// Theme colors
	var primaryColor ui.Color
	var bg ui.Color = effectiveBackground()
	// Ensure opacity
	if bg == ui.ColorClear {
		bg = ui.ColorBlack
		if isHtopMode() {
			bg = ui.NewRGBColor(0, 0, 0)
		}
	}

	if IsPaletteTheme(effectiveThemeName()) {
		primaryColor = processList.TitleStyle.Fg
	} else if IsLightMode && effectiveThemeName() == "white" {
		primaryColor = ui.ColorBlack
	} else if color, ok := colorMap[effectiveThemeName()]; ok {
		primaryColor = color
	} else {
		primaryColor = ui.ColorGreen
	}

	confirmModal.BackgroundColor = bg
	confirmModal.TextStyle = ui.NewStyle(ui.ColorWhite, bg)
	if IsLightMode {
		confirmModal.TextStyle = ui.NewStyle(ui.ColorBlack, bg)
	}
	if isHtopMode() {
		color := ui.ColorWhite
		r, g, b := bg.RGB()
		if .299*float64(r)+.587*float64(g)+.114*float64(b) > 127.5 {
			color = ui.ColorBlack
		}
		confirmModal.TextStyle = ui.NewStyle(color, bg)
	}
	confirmModal.BorderStyle = ui.NewStyle(primaryColor, bg)
	confirmModal.TitleStyle = ui.NewStyle(primaryColor, bg, ui.ModifierBold)
	// Style buttons
	for _, btn := range confirmModal.Buttons {
		btn.TextStyle = ui.NewStyle(primaryColor, bg)
		btn.ActiveStyle = ui.NewStyle(bg, primaryColor)
		btn.BorderStyle = ui.NewStyle(primaryColor, bg)
	}
}

func showKillModal(pid int) {
	killPending = true
	killPID = pid
	killBirthTime = processBirthTime(pid)
	confirmModal.ActiveButtonIndex = 1

	if len(confirmModal.Buttons) >= 2 {
		confirmModal.Buttons[0].OnClick = func() {
			executeKill()
		}
		confirmModal.Buttons[1].OnClick = func() {
			hideKillModal()
			updateProcessList()
		}
	}

	confirmModal.Title = fmt.Sprintf(i18n.T("TUI_KillPIDTitle"), pid)
	updateKillModal()
}

func hideKillModal() {
	killPending = false
}

func handleKillPending(e ui.Event) {
	switch e.ID {
	case "y", "Y": // Quick confirm
		executeKill()
	case "n", "N", "<Escape>": // Quick cancel
		hideKillModal()
		updateProcessList()
	case "<Left>", "h":
		confirmModal.ActiveButtonIndex = 0
		updateKillModal()
	case "<Right>", "l":
		confirmModal.ActiveButtonIndex = 1
		updateKillModal()
	case "<Enter>", "<Space>":
		if confirmModal.ActiveButtonIndex >= 0 && confirmModal.ActiveButtonIndex < len(confirmModal.Buttons) {
			if confirmModal.Buttons[confirmModal.ActiveButtonIndex].OnClick != nil {
				confirmModal.Buttons[confirmModal.ActiveButtonIndex].OnClick()
			}
		}
	}
}

func executeKill() {
	if killBirthTime <= 0 || processBirthTime(killPID) != killBirthTime {
		stderrLogger.Printf("Process %d changed identity or exited; signal cancelled", killPID)
		hideKillModal()
		updateProcessList()
		return
	}
	if err := syscall.Kill(killPID, syscall.SIGTERM); err == nil {
		stderrLogger.Printf("Sent SIGTERM to PID %d\n", killPID)

		if procs, err := getProcessSnapshot(lastGPUMetrics.ActivePercent); err == nil {
			lastProcesses = procs
			if searchMode || searchText != "" {
				updateFilteredProcesses()
			}
		}
		if isPortsLayoutActive() {
			if ports, err := collectListeningPorts(); err == nil {
				lastPorts = ports
				if searchMode || searchText != "" || portsExternalOnly {
					updateFilteredPorts()
				}
			}
		}
	} else {
		stderrLogger.Printf("Failed to kill PID %d: %v\n", killPID, err)
	}
	hideKillModal()
	updateProcessList()
}

func handleNavigation(e ui.Event) {
	if searchMode {
		return
	}

	switch e.ID {
	case "/":
		handleSearchToggle()
	case "<Escape>":
		handleSearchClear()
	case "<Up>", "k", "<MouseWheelUp>", "<Down>", "j", "<MouseWheelDown>", "g", "<Home>", "G", "<End>":
		handleVerticalNavigation(e)
	case "<Left>", "<Right>":
		handleColumnNavigation(e)
	case "<Enter>", "<Space>":
		handleSortToggle()
	case "<F9>":
		attemptKillProcess()
	case "e":
		if isPortsLayoutActive() {
			togglePortsExternalFilter()
		}
	}
}

func handleProcessListEvents(e ui.Event) {
	// Don't handle process list navigation when in Info or Fan layout (allow their own scrolling)
	if effectiveLayout() == LayoutInfo || effectiveLayout() == LayoutFan {
		return
	}
	if killPending {
		handleKillPending(e)
		return
	}
	if searchMode {
		handleSearchInput(e)
		return
	}
	if handleBtopProcessKey(e.ID) {
		return
	}
	handleNavigation(e)
}

func processBirthTime(pid int) int64 {
	mib := []C.int{C.CTL_KERN, C.KERN_PROC, C.KERN_PROC_PID, C.int(pid)}
	var kp C.struct_kinfo_proc
	size := C.size_t(C.sizeof_struct_kinfo_proc)
	if _, err := C.sysctl(&mib[0], 4, unsafe.Pointer(&kp), &size, nil, 0); err != nil || size == 0 {
		return 0
	}
	return int64(C.get_proc_starttime(&kp))
}
