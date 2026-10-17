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

func TestBarCells(t *testing.T) {
	for _, c := range []struct {
		counts []int
		want   []int
	}{
		{[]int{0, 0, 0, 0, 0}, []int{0, 0, 0, 0, 0}},
		{[]int{19, 0, 0, 1, 0}, []int{19, 0, 0, 1, 0}},
		{[]int{100, 1, 0, 0, 0}, []int{19, 1, 0, 0, 0}},
		{[]int{1, 1, 1, 0, 0}, []int{7, 7, 6, 0, 0}},
		{[]int{5, 0, 0, 5, 10}, []int{5, 0, 0, 5, 10}},
	} {
		got := barCells(c.counts, 20)
		if !slices.Equal(got, c.want) {
			t.Errorf("barCells(%v) = %v, want %v", c.counts, got, c.want)
		}
	}
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
	for _, l := range lines[:len(lines)-1] {
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
