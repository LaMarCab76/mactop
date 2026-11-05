package app

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"

	"github.com/metaspartan/mactop/v2/internal/i18n"
)

//go:embed settings_catalog.json
var settingsCatalogJSON []byte

type settingSpec struct {
	ApplyEnglish string   `json:"apply_en"`
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	LabelES      string   `json:"label_es"`
	Category     string   `json:"category"`
	Kind         string   `json:"kind"`
	Default      string   `json:"default"`
	Scope        string   `json:"scope"`
	Apply        string   `json:"apply"`
	CLI          []string `json:"cli"`
	Note         string   `json:"note"`
	Choices      []string `json:"choices"`
	Min          float64  `json:"min"`
	Max          float64  `json:"max"`
}

var settingCatalog = func() []settingSpec {
	var specs []settingSpec
	if err := json.Unmarshal(settingsCatalogJSON, &specs); err != nil {
		panic(err)
	}
	return specs
}()

func (s settingSpec) label() string {
	if i18n.T("Settings_Language") == "es" {
		return s.LabelES
	}
	return s.Label
}

func (s settingSpec) persistent() bool {
	return s.Kind != "action" && s.Kind != "session" && s.Kind != "legacy-readonly"
}

func configRawValue(c AppConfig, path string) json.RawMessage {
	raw, _ := json.Marshal(c)
	for _, key := range strings.Split(path, ".") {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return nil
		}
		raw = obj[key]
	}
	return raw
}

func configValue(c AppConfig, path string) string {
	raw := configRawValue(c, path)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, ",")
	}
	return string(raw)
}

func withConfigRawValue(c AppConfig, path string, raw json.RawMessage) AppConfig {
	b, _ := json.Marshal(c)
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(b, &obj)
	_ = setRawPreference(obj, strings.Split(path, "."), raw)
	b, _ = json.Marshal(obj)
	var result AppConfig
	_ = json.Unmarshal(b, &result)
	return result
}

func parseSetting(s settingSpec, value string) (json.RawMessage, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	} // inherit/default, including optional booleans
	var result any = value
	switch s.Kind {
	case "bool":
		v, err := strconv.ParseBool(value)
		if err != nil {
			return nil, err
		}
		result = v
	case "int", "float", "enum-int", "session":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%s: invalid number", s.label())
		}
		if s.Kind != "float" && math.Trunc(v) != v {
			return nil, fmt.Errorf("%s: integer required", s.label())
		}
		if s.Max > s.Min && (v < s.Min || v > s.Max) {
			return nil, fmt.Errorf("%s: %g..%g", s.label(), s.Min, s.Max)
		}
		result = v
	case "color", "theme":
		valid := false
		if IsHexColor(value) {
			_, err := ParseHexColor(value)
			valid = err == nil
		}
		if s.Kind == "theme" {
			for _, name := range themeOrder {
				valid = valid || name == value
			}
		}
		if strings.HasSuffix(s.Key, "background") || s.Key == "background" {
			_, ok := bgColorMap[value]
			valid = valid || ok
		}
		if !valid {
			return nil, fmt.Errorf("%s: use #RRGGBB or a supported preset", s.label())
		}
	case "list", "ordered-list":
		seen := map[string]bool{}
		var values []string
		for _, item := range strings.Split(value, ",") {
			item = strings.ToLower(strings.TrimSpace(item))
			valid := overlayValidSections[item]
			if s.Key == "overlay.collapsed_sections" {
				valid = stringIn(item, []string{"fps", "frame", "cpu", "gpu", "ane", "memory"})
			}
			if s.Key == "overlay.sections" {
				valid = stringIn(item, []string{"cpu", "gpu", "ane", "memory", "power", "temps", "thermal", "fans", "bandwidth", "network", "gpu_freq"})
			}
			if s.Key == "menubar.bar_order" {
				valid = stringIn(item, []string{"cpu", "gpu", "ane", "memory"})
			}
			if !valid || seen[item] {
				return nil, fmt.Errorf("invalid or duplicate section: %s", item)
			}
			seen[item] = true
			values = append(values, item)
		}
		if s.Key == "menubar.bar_order" && len(values) != 4 {
			return nil, fmt.Errorf("bar order requires cpu,gpu,ane,memory")
		}
		if s.Key == "menubar.bar_order" || s.Key == "overlay.sections" {
			result = strings.Join(values, ",")
		} else {
			result = values
		}
	case "string":
		if s.Key == "services.prometheus_address" {
			_, port, err := net.SplitHostPort(value)
			n, _ := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("use host:port or :port (1..65535)")
			}
		}
	}
	if len(s.Choices) > 0 && !stringIn(value, s.Choices) {
		return nil, fmt.Errorf("%s: %s", s.label(), strings.Join(s.Choices, ", "))
	}
	return json.Marshal(result)
}

func stringIn(v string, values []string) bool {
	for _, s := range values {
		if v == s {
			return true
		}
	}
	return false
}

func validateLoadedPreferences(c *AppConfig) {
	for _, s := range settingCatalog {
		if !s.persistent() {
			continue
		}
		value := configValue(*c, s.Key)
		if value == "" {
			continue
		}
		if _, err := parseSetting(s, value); err != nil {
			*c = withConfigRawValue(*c, s.Key, nil)
		}
	}
}

func (s settingSpec) application() string {
	if i18n.T("Settings_Language") == "es" {
		return s.Apply
	}
	return s.ApplyEnglish
}

func settingCategoryLabel(category string) string {
	keys := map[string]string{"General": "General", "Apariencia Original": "Original", "Nueva TUI": "NewUI", "Procesos": "Processes", "Colores Nueva TUI": "Colors", "Barra de menú": "MenuBar", "Overlay": "Overlay", "Salida headless": "Headless", "Servicios": "Services", "Acciones y diagnósticos": "Actions"}
	if key, ok := keys[category]; ok {
		return i18n.T("Settings_Category" + key)
	}
	return category
}
