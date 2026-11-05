package app

import (
	"flag"
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/mactop/v2/internal/i18n"
)

type settingsEditor struct {
	*ui.Block
	draft                   AppConfig
	startup                 bool
	quick                   bool
	category, index, scroll int
	editing                 bool
	input, status, command  string
	sessionValues           map[string]string
	edits                   preferencePatch
}

var activeSettings *settingsEditor

func newSettingsEditor(startup bool) *settingsEditor {
	return &settingsEditor{Block: ui.NewBlock(), draft: cloneConfig(currentConfig), startup: startup, sessionValues: map[string]string{"session.pid": strconv.Itoa(filterPID)}, edits: make(preferencePatch)}
}

func settingsCategories() []string {
	var categories []string
	for _, s := range settingCatalog {
		if s.Kind != "legacy-readonly" && !stringIn(s.Category, categories) {
			categories = append(categories, s.Category)
		}
	}
	return categories
}

func (e *settingsEditor) fields() []settingSpec {
	if e.quick {
		var fields []settingSpec
		for _, s := range settingCatalog {
			if strings.HasPrefix(s.Key, "htop.processes.") {
				fields = append(fields, s)
			}
		}
		return fields
	}
	categories := settingsCategories()
	if len(categories) == 0 {
		return nil
	}
	category := categories[e.category%len(categories)]
	var fields []settingSpec
	for _, s := range settingCatalog {
		if s.Kind != "legacy-readonly" && s.Category == category {
			fields = append(fields, s)
		}
	}
	return fields
}

func (e *settingsEditor) current() settingSpec {
	fields := e.fields()
	if len(fields) == 0 {
		return settingSpec{}
	}
	e.index = max(0, min(e.index, len(fields)-1))
	return fields[e.index]
}

func (e *settingsEditor) value(s settingSpec) string {
	if !s.persistent() {
		return e.sessionValues[s.Key]
	}
	return configValue(e.draft, s.Key)
}

func (e *settingsEditor) setValue(s settingSpec, value string) error {
	if !s.persistent() {
		if s.Kind == "session" {
			if _, err := parseSetting(s, value); err != nil {
				return err
			}
		}
		e.sessionValues[s.Key] = value
		e.command = e.actionCommand(s)
		return nil
	}
	raw, err := parseSetting(s, value)
	if err != nil {
		return err
	}
	e.draft = withConfigRawValue(e.draft, s.Key, raw)
	e.edits[s.Key] = raw
	if s.Key == "theme" || s.Key == "background" || strings.HasPrefix(s.Key, "custom_theme.") {
		e.draft.ThemeSource = "config"
		e.edits["theme_source"] = configRawValue(e.draft, "theme_source")
		e.status = i18n.T("Settings_ConfigColors")
	}
	return nil
}

func (e *settingsEditor) cycle(s settingSpec, step int) {
	choices := append([]string{""}, s.Choices...)
	if s.Kind == "bool" {
		choices = []string{"", "true", "false"}
		// Scalar booleans cannot represent a distinct inherited state.
		// Their catalog supplies the two states that survive normalization.
		if len(s.Choices) > 0 {
			choices = append([]string(nil), s.Choices...)
		}
	}
	if s.Kind == "theme" {
		choices = append([]string{""}, themeOrder...)
	}
	if s.Key == "background" || s.Key == "htop.background" {
		choices = append([]string{""}, bgColorOrder...)
	}
	if len(choices) < 2 {
		return
	}
	index := 0
	for i, v := range choices {
		if v == e.value(s) {
			index = i
			break
		}
	}
	if err := e.setValue(s, choices[(index+step+len(choices))%len(choices)]); err != nil {
		e.status = err.Error()
	}
}

// Returns close/quit. No monitor shortcut or fan control receives editor keys.
func (e *settingsEditor) key(key string) (close, quit bool) {
	if key == "<C-c>" {
		return true, true
	}
	if key == "<F10>" || key == "<C-s>" {
		if e.editing {
			if err := e.setValue(e.current(), e.input); err != nil {
				e.status = err.Error()
				return
			}
			e.editing = false
		}
		before := cloneConfig(currentConfig)
		if err := commitPreferenceDraft(e.draft, e.edits); err != nil {
			e.status = i18n.T("Settings_SaveError") + ": " + err.Error()
			return
		}
		if !e.startup {
			for key := range e.edits {
				if strings.HasPrefix(key, "htop.processes.") {
					btopProcessOverride = nil
					break
				}
			}
			applySavedSettings(before)
		}
		if e.quick {
			btopProcessOverride = nil
			updateProcessList()
		}
		return true, false
	}
	if e.editing {
		switch key {
		case "<Escape>":
			e.editing = false
			e.input = ""
		case "<Enter>":
			if err := e.setValue(e.current(), e.input); err != nil {
				e.status = err.Error()
				return
			}
			e.editing = false
		case "<Backspace>", "<C-h>":
			if len(e.input) > 0 {
				_, size := utf8.DecodeLastRuneInString(e.input)
				e.input = e.input[:len(e.input)-size]
			}
		case "<C-u>":
			e.input = ""
		case "<Space>", " ":
			e.input += " "
		default:
			if !strings.HasPrefix(key, "<") && utf8.ValidString(key) {
				e.input += key
			}
		}
		return
	}
	s := e.current()
	if e.quick && key == "<Enter>" {
		var draft *BtopProcessConfig
		if e.draft.Htop != nil {
			draft = e.draft.Htop.Processes
		}
		c := normalizeBtopProcessOptions(draft)
		btopProcessOverride = &c
		updateProcessList()
		return true, false
	}
	if e.quick && key == "e" {
		e.editing = true
		e.input = e.value(s)
		e.status = ""
		return
	}
	switch key {
	case "<Escape>", "q":
		return true, false
	case "<Tab>", "]":
		e.category = (e.category + 1) % len(settingsCategories())
		e.index, e.scroll = 0, 0
	case "<Backtab>", "[":
		e.category = (e.category + len(settingsCategories()) - 1) % len(settingsCategories())
		e.index, e.scroll = 0, 0
	case "<Down>", "j", "<MouseWheelDown>":
		e.index = min(len(e.fields())-1, e.index+1)
	case "<Up>", "k", "<MouseWheelUp>":
		e.index = max(0, e.index-1)
	case "<PageDown>":
		e.index = min(len(e.fields())-1, e.index+8)
	case "<PageUp>":
		e.index = max(0, e.index-8)
	case "<Home>":
		e.index = 0
	case "<End>":
		e.index = max(0, len(e.fields())-1)
	case "<Left>":
		e.cycle(s, -1)
	case "<Right>", "<Space>", " ":
		e.cycle(s, 1)
	case "d", "<Delete>":
		if s.persistent() {
			if err := e.setValue(s, ""); err != nil {
				e.status = err.Error()
			}
		}
	case "<Enter>":
		if s.Kind == "action" {
			e.command = e.actionCommand(s)
		} else {
			e.editing = true
			e.input = e.value(s)
			e.status = ""
		}
	case "I":
		if file := loadThemeFile(); file != nil {
			if err := e.importTheme(file); err != nil {
				e.status = err.Error()
			} else {
				e.status = i18n.T("Settings_Imported")
			}
		}
	}
	return
}

func (e *settingsEditor) actionCommand(s settingSpec) string {
	if s.Key == "session.pid" {
		pid, _ := strconv.Atoi(e.sessionValues[s.Key])
		command := fmt.Sprintf("mactop --pid %d", pid)
		if pid > 0 && processBirthTime(pid) == 0 {
			command += " · " + i18n.T("Settings_PIDGone")
		}
		return command
	}
	flagName := ""
	if len(s.CLI) > 0 {
		flagName = s.CLI[0]
	}
	command := "mactop " + flagName
	switch s.Key {
	case "launch.prometheus":
		address := configValue(e.draft, "services.prometheus_address")
		if address == "" {
			address = ":9090"
		}
		command += " " + address
	case "launch.headless":
		format := configValue(e.draft, "output.format")
		if format == "" {
			format = "json"
		}
		command += " --format " + format
	case "fan.control", "fan.auto":
		command = "sudo " + command
	case "fan.set":
		command = "sudo mactop --fan-set <RPM|60%|min|max|auto> [--fan-id ID]"
	case "fan.id":
		command = "sudo mactop --fan-set <valor> --fan-id <ID>"
	}
	return command
}

func settingOverride(s settingSpec) string {
	if strings.HasPrefix(s.Key, "htop.processes.") && btopProcessOverride != nil {
		return "F6 (session)"
	}
	for _, name := range s.CLI {
		if flagWasSet(strings.TrimLeft(name, "-")) {
			return name
		}
	}
	if s.Key == "language" && os.Getenv("MACTOP_LANG") != "" {
		return "MACTOP_LANG"
	}
	if (s.Key == "theme" || s.Key == "background" || strings.HasPrefix(s.Key, "custom_theme.")) && currentConfig.ThemeSource != "config" && loadThemeFile() != nil {
		return "theme.json"
	}
	return ""
}

func (e *settingsEditor) Draw(buf *ui.Buffer) {
	bg, p := htopEffectiveBg(), htopPalette()
	text := ui.NewStyle(p.text, bg)
	buf.Fill(ui.NewCell(' ', text), e.Rectangle)
	if e.Dx() < 3 || e.Dy() < 3 {
		return
	}
	e.Title = runewidth.Truncate(i18n.T("Settings_Title"), e.Dx()-4, "")
	e.BorderStyle, e.TitleStyle = ui.NewStyle(p.border, bg), text
	e.BackgroundColor = bg
	e.Block.Draw(buf)
	area := e.Inner
	write := func(row int, value string, style ui.Style) {
		if row >= area.Min.Y && row < area.Max.Y && area.Dx() > 0 {
			buf.SetString(runewidth.Truncate(value, area.Dx(), ""), style, image.Pt(area.Min.X, row))
		}
	}
	categories := settingsCategories()
	heading := fmt.Sprintf("[%d/%d] %s · Tab / Shift-Tab", e.category+1, len(categories), settingCategoryLabel(categories[e.category]))
	if e.quick {
		heading = i18n.T("Btop_QuickTitle")
	}
	write(area.Min.Y, heading, ui.NewStyle(p.user, bg, ui.ModifierBold))
	footer := min(6, max(1, area.Dy()/3))
	rows := max(0, area.Dy()-footer-1)
	if e.index < e.scroll {
		e.scroll = e.index
	}
	if e.index >= e.scroll+rows {
		e.scroll = max(0, e.index-rows+1)
	}
	fields := e.fields()
	labelWidth := max(8, min(42, area.Dx()/2))
	for offset := 0; offset < rows && e.scroll+offset < len(fields); offset++ {
		i := e.scroll + offset
		s := fields[i]
		value := e.value(s)
		if value == "" {
			value = i18n.T("Settings_Default") + " (" + s.Default + ")"
		}
		if s.Kind == "action" {
			value = "↵ " + i18n.T("Settings_Command")
		}
		label := runewidth.Truncate(s.label(), labelWidth, "")
		line := label + strings.Repeat(" ", max(1, labelWidth-runewidth.StringWidth(label)+1)) + value
		style := text
		if i == e.index {
			style = ui.NewStyle(ui.ColorBlack, ui.ColorCyan)
			line = "> " + line
		} else {
			line = "  " + line
		}
		write(area.Min.Y+1+offset, line, style)
	}
	s := e.current()
	row := area.Max.Y - footer
	if e.editing {
		write(row, "> "+e.input+"_", ui.NewStyle(p.user, bg))
		row++
	} else if source := settingOverride(s); source != "" {
		write(row, i18n.T("Settings_Override")+": "+source+" = "+effectiveSettingValue(s), text)
		row++
	}
	if row < area.Max.Y-2 {
		write(row, s.application(), text)
		row++
	}
	if e.command != "" && row < area.Max.Y-2 {
		write(row, e.command, ui.NewStyle(p.user, bg))
		row++
	}
	status := e.status
	if status == "" {
		status = preferenceError()
	}
	if status != "" {
		write(area.Max.Y-2, status, ui.NewStyle(p.system, bg))
	}
	keys := i18n.T("Settings_Keys")
	if e.quick {
		keys = i18n.T("Btop_QuickKeys")
	}
	write(area.Max.Y-1, keys, text)
}

// Caller holds renderMutex; this path never runs before widget setup.
func applySavedSettings(before AppConfig) {
	for _, key := range []string{"htop.processes.mode", "htop.processes.count", "htop.processes.blocks", "htop.processes.name", "htop.processes.user"} {
		if configValue(before, key) != configValue(currentConfig, key) {
			btopProcessOverride = nil
			break
		}
	}
	if before.Interval != currentConfig.Interval && !flagWasSet("interval", "i") {
		ms := currentConfig.Interval
		if ms == 0 {
			ms = 1000
		}
		_ = setUpdateInterval(ms)
	}
	if !flagWasSet("unit-network") {
		networkUnit = "auto"
		if currentConfig.Units != nil && currentConfig.Units.Network != "" {
			networkUnit = currentConfig.Units.Network
		}
	}
	if !flagWasSet("unit-disk") {
		diskUnit = "auto"
		if currentConfig.Units != nil && currentConfig.Units.Disk != "" {
			diskUnit = currentConfig.Units.Disk
		}
	}
	if !flagWasSet("unit-temp") {
		tempUnit = "celsius"
		if currentConfig.Units != nil && currentConfig.Units.Temperature != "" {
			tempUnit = currentConfig.Units.Temperature
		}
	}
	selectedColumn, sortReverse = 4, currentConfig.SortReverse
	if currentConfig.SortColumn != nil {
		selectedColumn = *currentConfig.SortColumn
	}
	for _, mode := range []string{UIModeOriginal, UIModeHtop} {
		path := "default_layout"
		if mode == UIModeHtop {
			path = "htop.layout"
		}
		if configValue(before, path) != configValue(currentConfig, path) {
			layout := configValue(currentConfig, path)
			if layout == "" {
				layout = LayoutDefault
			}
			state := modeLayoutState{Current: layout, Last: layout}
			modeLayouts[mode] = state
			if activeUIMode == mode {
				runtimeLayout, lastActiveLayout, previousLayout, previousStableLayout = layout, layout, "", ""
				temporaryLayout, previousLayoutTemporary = false, false
			}
		}
	}

	applyResolvedOriginalAppearance()
	updateLayoutNumber()
	if !showHelp {
		applyLayout(effectiveLayout())
	}
	updateHelpText()
	updateProcessList()
	updateInfoUI()
}

func effectiveSettingValue(s settingSpec) string {
	if strings.HasPrefix(s.Key, "htop.processes.") {
		c := btopProcessOptions()
		return configValue(AppConfig{Htop: &HtopConfig{Processes: &c}}, s.Key)
	}
	switch s.Key {
	case "ui_mode":
		return activeUIMode
	case "language":
		if resolvedLanguage != "" {
			return resolvedLanguage
		}
		return "auto"
	case "interval":
		return strconv.Itoa(getUpdateInterval()) + "ms"
	case "units.network":
		return networkUnit
	case "units.disk":
		return diskUnit
	case "units.temperature":
		return tempUnit
	case "theme":
		return resolveOriginalAppearance(currentConfig).Theme
	case "background":
		return resolveOriginalAppearance(currentConfig).Background
	case "htop.background":
		if cliBgColor != "" {
			return cliBgColor
		}
		return htopConfig().Background
	}
	for _, name := range s.CLI {
		name = strings.TrimLeft(name, "-")
		if f := flag.Lookup(name); f != nil && flagWasSet(name) {
			return f.Value.String()
		}
	}
	if strings.HasPrefix(s.Key, "custom_theme.") {
		a := resolveOriginalAppearance(currentConfig)
		return configValue(AppConfig{CustomTheme: a.Custom}, s.Key)
	}
	return configValue(currentConfig, s.Key)
}

func (e *settingsEditor) importTheme(file *CustomThemeConfig) error {
	// Validate the whole import before changing the draft. File primary colors
	// write only canonical keys; config.json's legacy aliases remain untouched.
	values := preferencePatch{}
	for _, spec := range settingCatalog {
		if !spec.persistent() {
			continue
		}
		var value string
		switch {
		case spec.Key == "theme":
			if file.Foreground == "" {
				continue
			}
			value = file.Foreground
		case spec.Key == "background":
			if file.Background == "" {
				continue
			}
			value = file.Background
		case strings.HasPrefix(spec.Key, "custom_theme."):
			value = configValue(AppConfig{CustomTheme: file}, spec.Key)
		default:
			continue
		}
		raw, err := parseSetting(spec, value)
		if err != nil {
			return err
		}
		values[spec.Key] = raw
	}
	for path, raw := range values {
		e.draft = withConfigRawValue(e.draft, path, raw)
		e.edits[path] = raw
	}
	e.draft.ThemeSource = "config"
	e.edits["theme_source"] = configRawValue(e.draft, "theme_source")
	return nil
}
