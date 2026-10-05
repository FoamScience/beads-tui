package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/FoamScience/beads-tui/internal/bd"
)

// state is the per-user UI state kept across runs.
type state struct {
	NowStatuses []string `json:"now_statuses"`
	Machine     string   `json:"machine"` // Now/Ready filter: "" this host, "all", or a machine label value
	ReadyFilter string   `json:"ready_filter"`
}

func statePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bt", "state.json")
}

func defaultState() *state {
	return &state{NowStatuses: []string{"in_progress", "pinned", "hooked"}}
}

func loadState() *state {
	s := defaultState()
	if b, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}

func (s *state) save() error {
	p := statePath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p, b, 0o644)
}

// The last snapshot is cached so bt paints immediately while bd (seconds per call) reloads.
func cachePath(beadsDir string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bt", strings.ReplaceAll(strings.Trim(beadsDir, "/"), "/", "_")+".json")
}

func loadCache(beadsDir string) []bd.Issue {
	b, err := os.ReadFile(cachePath(beadsDir))
	if err != nil {
		return nil
	}
	var is []bd.Issue
	if json.Unmarshal(b, &is) != nil {
		return nil
	}
	return is
}

func saveCache(beadsDir string, is []bd.Issue) {
	p := cachePath(beadsDir)
	if p == "" || os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	b, err := json.Marshal(is)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}
