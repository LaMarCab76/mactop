package app

import (
	"fmt"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

// isLightNamedTheme returns true for named themes that need dark text on header
// Only indigo is dark enough to need white text
func isLightNamedTheme(theme string) bool {
	return theme != "indigo"
}

func resolveProcessThemeColor() (string, string) {
	if isHtopMode() {
		p := htopPalette()
		return p.greenHex, p.headerFg
	}
	themeColor := processList.TextStyle.Fg
	var themeColorStr string

	// Check if custom ProcessList color is set
	if effectiveCustomTheme() != nil && effectiveCustomTheme().ProcessList != "" {
		if IsHexColor(effectiveCustomTheme().ProcessList) {
			themeColorStr = effectiveCustomTheme().ProcessList
		}
	}

	if themeColorStr == "" {
		// Check if the theme is a hex color
		if IsHexColor(effectiveThemeName()) {
			themeColorStr = effectiveThemeName()
		} else if IsPaletteTheme(effectiveThemeName()) {
			themeColorStr = GetPaletteHex(effectiveThemeName(), "Primary")
		} else if IsLightMode && effectiveThemeName() == "white" {
			themeColorStr = "black"
		} else if effectiveThemeName() == "1977" {
			themeColorStr = "green"
		} else if color, ok := colorMap[effectiveThemeName()]; ok {
			hexStr := resolveThemeColorString(effectiveThemeName())
			if hexStr != effectiveThemeName() {
				themeColorStr = hexStr
			} else {
				themeColorStr = getThemeColorName(color)
			}
		} else {
			themeColorStr = getThemeColorName(themeColor)
		}
	}

	return themeColorStr, resolveSelectedHeaderFg(themeColorStr)
}

// resolveSelectedHeaderFg determines the foreground color for selected headers
// based on the theme color, ensuring good contrast.
func resolveSelectedHeaderFg(themeColorStr string) string {
	if themeColorStr == "black" || themeColorStr == "#000000" || themeColorStr == "#020202" {
		return "#ffffff"
	}
	if IsLightMode {
		return "#020202"
	}
	if IsPaletteTheme(effectiveThemeName()) {
		return GetPaletteHex(effectiveThemeName(), "Base")
	}
	if IsHexColor(themeColorStr) {
		if IsLightHexColor(themeColorStr) {
			return "#020202"
		}
		return "#ffffff"
	}
	if isLightNamedTheme(effectiveThemeName()) {
		return "#020202"
	}
	return "#ffffff"
}

func getProcessListTitle() (string, ui.Style) {
	// Resolve title color (use process list color if set)
	var titleColor ui.Color = ui.ColorClear
	if effectiveCustomTheme() != nil && effectiveCustomTheme().ProcessList != "" {
		if color, err := ParseHexColor(effectiveCustomTheme().ProcessList); err == nil {
			titleColor = color
		}
	}
	if titleColor == ui.ColorClear {
		titleColor = GetThemeColorWithLightMode(effectiveThemeName(), IsLightMode)
	}
	if isHtopMode() {
		titleColor = htopPalette().user
	}

	if killPending {
		return fmt.Sprintf(i18n.T("TUI_ProcessListKill"), killPID), ui.NewStyle(ui.ColorRed, CurrentBgColor, ui.ModifierBold)
	} else if searchMode || searchText != "" {
		return fmt.Sprintf(i18n.T("TUI_ProcessListSearch"), searchText), ui.NewStyle(titleColor, CurrentBgColor, ui.ModifierBold)
	} else if isFrozen {
		return i18n.T("TUI_ProcessListFrozen"), ui.NewStyle(titleColor, CurrentBgColor, ui.ModifierBold)
	} else if filterPID > 0 {
		return fmt.Sprintf(i18n.T("TUI_ProcessListPID"), filterPID), ui.NewStyle(titleColor, CurrentBgColor, ui.ModifierBold)
	}
	return i18n.T("TUI_ProcessListFull"), ui.NewStyle(titleColor, CurrentBgColor)
}

func attemptKillProcess() {
	if isPortsLayoutActive() {
		ports := currentViewPorts()
		if len(ports) > 0 && processList.SelectedRow < len(ports)+1 {
			if processList.SelectedRow > 0 {
				processIndex := processList.SelectedRow - 1
				if processIndex < len(ports) {
					showKillModal(ports[processIndex].PID)
				}
			}
		}
		return
	}

	row := processList.SelectedRow - 1
	if row >= 0 && row < len(visibleProcesses) {
		p := visibleProcesses[row]
		showKillModal(p.PID)
		if p.BirthTime > 0 {
			killBirthTime = p.BirthTime
		}
	}

}

func handleSearchToggle() {
	searchMode = true
	searchText = ""
	filteredProcesses = nil
	filteredPorts = nil
	updateProcessList()
}

func handleSearchClear() {
	if searchText != "" {
		searchText = ""
		filteredProcesses = nil
		filteredPorts = nil
		updateProcessList()
	}
}

func handleVerticalNavigation(e ui.Event) {
	switch e.ID {
	case "<Up>", "k", "<MouseWheelUp>":
		if processList.SelectedRow > 0 {
			processList.SelectedRow--
			updateProcessList()
		}
	case "<Down>", "j", "<MouseWheelDown>":
		if processList.SelectedRow < len(processList.Rows)-1 {
			processList.SelectedRow++
			updateProcessList()
		}
	case "g", "<Home>":
		if len(processList.Rows) > 1 {
			processList.SelectedRow = 1
			updateProcessList()
		} else {
			processList.SelectedRow = 0
		}
	case "G", "<End>":
		if len(processList.Rows) > 0 {
			processList.SelectedRow = len(processList.Rows) - 1
			updateProcessList()
		}
	}
}

func handleColumnNavigation(e ui.Event) {
	if isTop3Layout() {
		if e.ID == "<Left>" {
			cycleTopMetric(-1)
		} else if e.ID == "<Right>" {
			cycleTopMetric(1)
		}
		return
	}
	if isPortsLayoutActive() {
		switch e.ID {
		case "<Left>":
			if portSelectedColumn > 0 {
				portSelectedColumn--
				updateProcessList()
			}
		case "<Right>":
			if portSelectedColumn < len(portColumns)-1 {
				portSelectedColumn++
				updateProcessList()
			}
		case "<Enter>", "<Space>":
			handleSortToggle()
		}
		return
	}

	switch e.ID {
	case "<Left>":
		if selectedColumn > 0 {
			selectedColumn--
			currentConfig.SortColumn = &selectedColumn
			saveConfig()
			updateProcessList()
		}
	case "<Right>":
		if selectedColumn < len(columns)-1 {
			selectedColumn++
			currentConfig.SortColumn = &selectedColumn
			saveConfig()
			updateProcessList()
		}
	case "<Enter>", "<Space>":
		handleSortToggle()
	}
}

func handleSortToggle() {
	if isTop3Layout() {
		return
	}
	if isPortsLayoutActive() {
		portSortReverse = !portSortReverse
		updateProcessList()
		return
	}
	sortReverse = !sortReverse
	currentConfig.SortReverse = sortReverse
	saveConfig()
	updateProcessList()
}
