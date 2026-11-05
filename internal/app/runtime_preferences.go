package app

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	ui "github.com/metaspartan/gotui/v5"
)

var runtimeInterval atomic.Int64

func normalizeCLIColors() {
	// flag.Parse writes the original argument over the legacy parser's value.
	// Preserve its case-insensitive preset behavior in the effective resolver.
	if !IsHexColor(cliFgColor) {
		cliFgColor = strings.ToLower(cliFgColor)
	}
	if !IsHexColor(cliBgColor) {
		cliBgColor = strings.ToLower(cliBgColor)
	}
}

func getUpdateInterval() int {
	if n := runtimeInterval.Load(); n > 0 {
		return int(n)
	}
	return 1000
}

func flagWasSet(names ...string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		for _, name := range names {
			if f.Name == name {
				found = true
			}
		}
	})
	return found
}

// Run once before alternate modes, and again after startup settings. No
// sampling goroutine exists yet. A CLI interval is never copied into config.
func resolveRuntimePreferences() error {
	ms := 1000
	if currentConfig.Interval > 0 {
		ms = currentConfig.Interval
	}
	if flagWasSet("interval", "i") {
		ms = updateInterval
	}
	if ms <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	runtimeInterval.Store(int64(ms))
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
	if currentConfig.SortColumn != nil && *currentConfig.SortColumn >= 0 && *currentConfig.SortColumn < len(columns) {
		selectedColumn = *currentConfig.SortColumn
	}
	if filterPID < 0 {
		return fmt.Errorf("PID must be nonnegative")
	}
	if headless && currentConfig.Output != nil {
		if !flagWasSet("format") && currentConfig.Output.Format != "" {
			headlessFormat = currentConfig.Output.Format
		}
		if !flagWasSet("count") {
			headlessCount = currentConfig.Output.Count
		}
		if !flagWasSet("pretty") {
			headlessPretty = currentConfig.Output.Pretty
		}
	}
	return nil
}

// Caller holds renderMutex. Used by keys and editor; collectors read atomically.
func setUpdateInterval(ms int) error {
	if ms < 100 || ms > 5000 {
		return fmt.Errorf("interval: 100..5000 ms")
	}
	runtimeInterval.Store(int64(ms))
	if ticker != nil {
		ticker.Reset(time.Duration(ms) * time.Millisecond)
	}
	if partyTicker != nil {
		partyTicker.Reset(time.Duration(ms/2) * time.Millisecond)
	}
	if mainBlock != nil {
		updateIntervalText()
	}
	return nil
}

type originalAppearance struct {
	Theme, Background string
	Custom            *CustomThemeConfig
}

var activeOriginalAppearance *originalAppearance
var partyTheme string

func resolveOriginalAppearance(c AppConfig) originalAppearance {
	a := originalAppearance{Theme: c.Theme, Background: c.Background, Custom: c.CustomTheme}
	if a.Theme == "" {
		a.Theme = "green"
	}
	if a.Background == "" {
		a.Background = "clear"
	}
	if c.ThemeSource != "config" {
		if file := loadThemeFile(); file != nil {
			if IsHexColor(file.Foreground) {
				a.Theme = file.Foreground
			}
			if IsHexColor(file.Background) {
				a.Background = file.Background
			}
			a.Custom = file
		}
	}
	if cliFgColor != "" {
		a.Theme = cliFgColor
	}
	if cliBgColor != "" {
		a.Background = cliBgColor
	}
	return a
}

func effectiveThemeName() string {
	if partyTheme != "" {
		return partyTheme
	}
	if activeOriginalAppearance != nil {
		return activeOriginalAppearance.Theme
	}
	if currentConfig.Theme != "" {
		return currentConfig.Theme
	}
	return "green"
}

func effectiveCustomTheme() *CustomThemeConfig {
	if partyTheme != "" {
		return nil
	}
	if activeOriginalAppearance != nil {
		return activeOriginalAppearance.Custom
	}
	return currentConfig.CustomTheme
}

func applyResolvedOriginalAppearance() {
	a := resolveOriginalAppearance(currentConfig)
	activeOriginalAppearance = &a
	applyBackground(a.Background)
	applyTheme(effectiveThemeName(), IsLightMode)
	applyEffectiveComponentColors()
}

func applyEffectiveComponentColors() {
	if theme := effectiveCustomTheme(); theme != nil {
		fg := GetThemeColorWithLightMode(effectiveThemeName(), IsLightMode)
		applyCustomPerComponentColors(theme, fg)
	}
}

func htopConfig() HtopConfig {
	c := HtopConfig{Layout: LayoutDefault, Background: "#000000", BarStyle: "gradient_blocks", TopMetric: "cpu"}
	if currentConfig.Htop != nil {
		h := currentConfig.Htop
		if h.Layout != "" {
			c.Layout = h.Layout
		}
		if h.Background != "" {
			c.Background = h.Background
		}
		if h.BarStyle != "" {
			c.BarStyle = h.BarStyle
		}
		if h.TopMetric != "" {
			c.TopMetric = h.TopMetric
		}
		c.Colors = h.Colors
	}
	return c
}

func htopEffectiveBg() ui.Color {
	name := htopConfig().Background
	if cliBgColor != "" {
		name = cliBgColor
	}
	if IsHexColor(name) {
		if c, e := ParseHexColor(name); e == nil {
			return c
		}
	}
	if name == "black" {
		return ui.NewRGBColor(0, 0, 0)
	}
	if c, ok := bgColorMap[name]; ok {
		return c
	}
	return ui.NewRGBColor(0, 0, 0)
}

func effectiveBackground() ui.Color {
	if isHtopMode() {
		return htopEffectiveBg()
	}
	return CurrentBgColor
}

func resolvedLanguagePreference() string {
	if cliLanguage != "" {
		return cliLanguage
	}
	if value := os.Getenv("MACTOP_LANG"); value != "" {
		return value
	}
	return currentConfig.Language
}
