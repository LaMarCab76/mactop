package app

import (
	"encoding/json"
	"sync"
)

// A worker may keep an older configuration while the TUI edits another field.
// Compare with the values last displayed by this worker, then write only the
// user's actual changes, rather than sending its entire stale configuration.
type nativePreferenceSnapshot struct {
	sync.Mutex
	path   string
	values map[string]json.RawMessage
}

func (s *nativePreferenceSnapshot) record(values map[string]json.RawMessage) {
	s.Lock()
	defer s.Unlock()
	s.path, s.values = mactopConfigPath("config.json"), values
}

func (s *nativePreferenceSnapshot) commit(values map[string]json.RawMessage) error {
	s.Lock()
	defer s.Unlock()
	path := mactopConfigPath("config.json")
	if s.path != path {
		s.path, s.values = path, nil
	}
	patch := diffPreferences(s.values, values)
	if len(patch) == 0 {
		return nil
	}
	if err := commitNativePreferences(patch); err != nil {
		return err
	}
	s.values = values
	return nil
}

var nativeMenuBarPreferences, nativeOverlayPreferences nativePreferenceSnapshot

var nativeMenuBarKeys = []string{"status_bar_width", "status_bar_height", "sparkline_width", "sparkline_height", "show_cpu", "show_gpu", "show_ane", "show_memory", "show_power", "show_percent", "font_size", "power_font_size", "cpu_color", "gpu_color", "ane_color", "mem_color", "label_color"}

func menuBarSnapshotValues(m MenuBarConfig) map[string]json.RawMessage {
	values := configLeaves(AppConfig{MenuBar: &m})
	snapshot := make(map[string]json.RawMessage, len(nativeMenuBarKeys))
	for _, key := range nativeMenuBarKeys {
		path := "menubar." + key
		snapshot[path] = values[path]
	}
	return snapshot
}

func overlaySnapshotValues(collapsed, expanded []string, opacity float64) (map[string]json.RawMessage, error) {
	values := make(map[string]json.RawMessage)
	for path, value := range map[string]any{"overlay.collapsed_sections": collapsed, "overlay.expanded_order": expanded, "overlay.opacity": opacity} {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		values[path] = raw
	}
	return values, nil
}
