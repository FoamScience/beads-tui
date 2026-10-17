package dispatch

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
	"github.com/FoamScience/beads-tui/internal/bd"
)

// Allow is dispatch.toml: nothing is dispatched unless it is listed here.
type Allow struct {
	Lanes []string `toml:"lanes"` // a lane's whole queue
	Epics []string `toml:"epics"` // every bead under these
	Beads []string `toml:"beads"` // single beads, sent ahead of the rest of their lane's queue
}

const allowHeader = `# bt dispatch sends only what is listed here, and re-reads this file every tick.
#   lanes: the lane's whole queue   epics: every bead under them   beads: single beads, sent first
# bt dispatch rewrites this file when you toggle entries from its TUI (comments are not kept).
`

func AllowPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bt", "dispatch.toml")
}

// LoadAllow reads dispatch.toml; a missing file allows nothing.
func LoadAllow(path string) (Allow, error) {
	var a Allow
	md, err := toml.DecodeFile(path, &a)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Allow{}, nil
	case err != nil:
		return Allow{}, err
	case len(md.Undecoded()) > 0:
		return Allow{}, fmt.Errorf("unknown keys: %v", md.Undecoded())
	}
	return a, nil
}

func SaveAllow(path string, a Allow) error {
	var b bytes.Buffer
	b.WriteString(allowHeader)
	if err := toml.NewEncoder(&b).Encode(a); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// Permits reports whether is may be dispatched to lane ("" when it has no lane yet, for the decider).
func (a Allow) Permits(s *bd.Snapshot, is *bd.Issue, lane string) bool {
	if lane != "" && slices.Contains(a.Lanes, lane) || slices.Contains(a.Beads, is.ID) {
		return true
	}
	for p := s.ByID[is.Parent]; p != nil; p = s.ByID[p.Parent] {
		if slices.Contains(a.Epics, p.ID) {
			return true
		}
	}
	return false
}

// Toggle adds v to the list or removes it.
func Toggle(list []string, v string) []string {
	if i := slices.Index(list, v); i >= 0 {
		return slices.Delete(list, i, i+1)
	}
	return append(list, v)
}
