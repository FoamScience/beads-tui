package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/goals"
	"github.com/charmbracelet/x/ansi"
)

// writeGoals points BT_GOALS at a cycle around today over the fixture beads.
func writeGoals(t *testing.T) string {
	t.Helper()
	now := time.Now()
	src := `
[[cycle]]
id    = "T1"
start = ` + now.AddDate(0, 0, -60).Format(time.DateOnly) + `
end   = ` + now.AddDate(0, 0, 30).Format(time.DateOnly) + `

  [[cycle.goal]]
  id    = "G1"
  title = "Ship epic A"
  kr    = "1.1"
  type  = "performance"
  items = [
    { label = "epic A", bead = "t-a", pts = 60 },
    { label = "orphan", bead = "t-b", pts = 40 },
  ]

  [[cycle.goal]]
  id      = "G2"
  title   = "Agent ladder"
  type    = "ai"
  scoring = "ladder"
  items = [
    { label = "agreed", pts = 50 },
    { label = "built", bead = "t-a.3", pts = 50 },
  ]
`
	p := filepath.Join(t.TempDir(), "goals.toml")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BT_GOALS", p)
	return p
}

func goalsApp(t *testing.T) (*App, *goalsView) {
	writeGoals(t)
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.Update(tea.KeyPressMsg{Code: '@', Text: "@"})
	v, ok := a.views[a.active].(*goalsView)
	if !ok {
		t.Fatalf("@ opened %s", a.views[a.active].Name())
	}
	return a, v
}

// press sends keys and feeds back the messages their commands produce, so flashes land.
func press(a *App, keys ...string) {
	var run func(cmd tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch m := cmd().(type) {
		case tea.BatchMsg:
			for _, c := range m {
				run(c)
			}
		case flashMsg:
			a.Update(m)
		}
	}
	for _, k := range keys {
		_, cmd := a.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		run(cmd)
	}
}

func TestGoalsTabScoresAndAccepts(t *testing.T) {
	a, v := goalsApp(t)
	out := ansi.Strip(a.render())
	for _, want := range []string{tabLabel(a.active, v), "G1 Ship epic A", "G2 Agent ladder", "epic A", "NEXT under epic A", "behind"} {
		if !strings.Contains(out, want) {
			t.Errorf("goals tab lacks %q", want)
		}
	}
	// NEXT under epic A: in-progress t-a.1 first, then ready t-a.3; blocked t-a.2 is left out
	var next []string
	for _, r := range v.next.rows {
		next = append(next, r.issue.ID)
	}
	if strings.Join(next, " ") != "t-a.1 t-a.3" {
		t.Fatalf("NEXT = %v", next)
	}
	press(a, "l", "j", "x") // accept the closed orphan
	c, g, _ := v.cur(a)
	if earned, _ := a.goals.Score(a.snap, c, g); earned != 40 {
		t.Fatalf("earned %v after accepting orphan, want 40", earned)
	}
	if r, err := goals.Load(goals.Path()); err != nil || len(r.Side.Accepted) != 1 {
		t.Fatalf("sidecar not written: %v %v", r.Side, err)
	}
	press(a, "x")
	if earned, _ := a.goals.Score(a.snap, c, g); earned != 0 {
		t.Fatal("a second x should withdraw the acceptance")
	}
}

func TestGoalsLadderAndTags(t *testing.T) {
	a, v := goalsApp(t)
	press(a, "j", "l", "j", "x")
	c, g, _ := v.cur(a)
	if st := a.goals.State(a.snap, c, g, 1); st != goals.Locked {
		t.Fatalf("second rung state %v, want Locked", st)
	}
	if !strings.Contains(a.flash, "first") {
		t.Fatalf("accepting a locked rung flashed %q", a.flash)
	}
	if got := a.goalTag(a.snap.ByID["t-a.3"]); got != "G1,G2" {
		t.Fatalf("t-a.3 tag %q, want G1,G2 (own rung, plus epic A)", got)
	}
	a.switchTo(0)
	if out := ansi.Strip(a.render()); !strings.Contains(out, "G1 Active child") {
		t.Fatal("Now rows should carry the goal tag")
	}
}

func TestGoalsAcceptRollsBackWhenSidecarUnwritable(t *testing.T) {
	a, v := goalsApp(t)
	if err := os.Mkdir(goals.SidecarPath(goals.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	press(a, "l", "j", "x")
	c, g, _ := v.cur(a)
	if st := a.goals.State(a.snap, c, g, 1); st != goals.Done || !a.flashErr {
		t.Fatalf("after a failed save: state %v, flash err %v", st, a.flashErr)
	}
}

func TestGoalsCycleChoiceSurvivesTabSwitch(t *testing.T) {
	a, v := goalsApp(t)
	v.cycle = 5 // gone: falls back to the current cycle
	a.switchTo(0)
	a.Update(tea.KeyPressMsg{Code: '@', Text: "@"})
	if v.cycle != 0 {
		t.Fatalf("cycle %d after reopening, want 0", v.cycle)
	}
}
