package ui

import (
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/dispatch"
	"github.com/charmbracelet/x/ansi"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestMain points config and cache at a scratch directory, so no test can overwrite the
// developer's real ~/.config/bt/state.json or snapshot cache.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bt-test-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("XDG_CACHE_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestDispatchColumnsAlign(t *testing.T) {
	cfg, err := dispatch.ParseConfig(`
[lane.a-very-long-lane-name-indeed]
cwd = "/x"
workspace = "game"
[lane.b]
cwd = "/x"
workspace = "game"
`)
	if err != nil {
		t.Fatal(err)
	}
	s := bd.NewSnapshot([]bd.Issue{
		{ID: "tasks-i3g.163.7.12.4", Status: "in_progress", Assignee: "a-very-long-lane-name-indeed"},
		{ID: "tasks-i3g.1", Status: "open", Labels: []string{"lane:b"}},
	})
	panes := []dispatch.Pane{{ID: "wP:p100", Agent: "claude", Status: "idle", Title: "a-very-long-lane-name-indeed"}}
	d := &Dispatch{state: &state{}, rep: dispatch.Report{Cfg: cfg, Snap: s, Lanes: dispatch.Lanes(cfg, s, panes, dispatch.Allow{})}}
	lines := d.renderLanes()
	want := -1
	for _, l := range lines {
		if strings.HasPrefix(l, "      ") {
			continue // a lane's fit line
		}
		plain := []rune(ansi.Strip(l))
		at := slices.IndexFunc(plain, func(r rune) bool { return r == '▰' || r == '▱' })
		if strings.Contains(string(plain), "PROGRESS") {
			at = strings.Index(string(plain), "PROGRESS")
			at = len([]rune(string(plain)[:at]))
		}
		if want == -1 {
			want = at
		}
		if at != want {
			t.Errorf("progress column at %d, want %d in %q", at, want, string(plain))
		}
	}
}

func TestActiveScope(t *testing.T) {
	s := bd.NewSnapshot([]bd.Issue{
		{ID: "p", IssueType: "epic", Status: "open"},
		{ID: "p.1", Parent: "p", Status: "in_progress"},
		{ID: "p.2", Parent: "p", Status: "open"},
		{ID: "p.2.1", Parent: "p.2", Status: "closed"},
		{ID: "p.2.2", Parent: "p.2", Status: "in_progress"},
		{ID: "p.3", Parent: "p", Status: "in_progress"},
		{ID: "p.3.1", Parent: "p.3", Status: "closed"},
		{ID: "p.3.2", Parent: "p.3", Status: "open"},
	})
	d := &Dispatch{rep: dispatch.Report{Snap: s}}
	for _, c := range []struct {
		active, id  string
		done, total int
	}{
		{"", "", 0, 0},
		{"p.1", "p.1", 0, 1},   // parent is the top-level epic: the bead alone
		{"p.2.2", "p.2", 1, 2}, // a leaf advances its parent
		{"p.3", "p.3", 1, 2},   // a bead with children advances them
	} {
		id, done, total := d.activeScope(&dispatch.LaneView{Active: c.active})
		if id != c.id || done != c.done || total != c.total {
			t.Errorf("activeScope(%q) = %q %d/%d, want %q %d/%d", c.active, id, done, total, c.id, c.done, c.total)
		}
	}
}
