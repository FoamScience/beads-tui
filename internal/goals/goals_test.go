package goals

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoamScience/beads-tui/internal/bd"
)

const sample = `
[[cycle]]
id    = "T1-2027"
start = 2026-12-01
end   = 2027-02-28
doc   = "~/agreement.pdf"

  [[cycle.goal]]
  id      = "G1"
  title   = "Viewer live"
  kr      = "1.1"
  type    = "performance"
  items = [
    { label = "SR-141", bead = "t-a", pts = 8 },
    { label = "SR-145", bead = "t-b", pts = 12.5 },
    { label = "SR-159", bead = "t-c", pts = 79.5 },
  ]

  [[cycle.goal]]
  id      = "G3"
  title   = "Runbook agent"
  type    = "ai"
  scoring = "ladder"
  items = [
    { label = "use case", pts = 25 },
    { label = "built", bead = "t-d", pts = 25 },
    { label = "used", pts = 25 },
    { label = "effect", pts = 30 },
  ]

[[cycle]]
id    = "T2-2027"
start = 2027-03-01
end   = 2027-06-30
`

func load(t *testing.T, side string) *Set {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "goals.toml")
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if side != "" {
		if err := os.WriteFile(SidecarPath(p), []byte(side), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func snap() *bd.Snapshot {
	return bd.NewSnapshot([]bd.Issue{
		{ID: "t-a", Status: "closed"},
		{ID: "t-b", Status: "closed"},
		{ID: "t-c", Status: "in_progress"},
		{ID: "t-d", Status: "open"},
	})
}

func date(s string) time.Time {
	t, _ := time.ParseInLocation(time.DateOnly, s, time.Local)
	return t
}

func TestMissingFileIsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "goals.toml"))
	if err != nil || s.File != nil || s.Current(time.Now()) != -1 {
		t.Fatalf("missing goals.toml: %v %v", s.File, err)
	}
}

func TestPointsScoreCountsAcceptedOnly(t *testing.T) {
	s := load(t, "[accepted]\n\"T1-2027/G1/SR-141\" = \"2026-12-10\"\n")
	c := s.File.Cycles[0]
	g := c.Goals[0]
	if g.Items[1].Pts != 12.5 {
		t.Fatalf("pts = %v", g.Items[1].Pts)
	}
	earned, done := s.Score(snap(), c, g)
	if earned != 8 || done != 12.5 {
		t.Fatalf("earned %v done %v, want 8 and 12.5", earned, done)
	}
	want := []State{Accepted, Done, InProgress}
	for i, w := range want {
		if st := s.State(snap(), c, g, i); st != w {
			t.Fatalf("item %d state %v, want %v", i, st, w)
		}
	}
}

func TestLadderLocksAfterFirstGap(t *testing.T) {
	s := load(t, "[accepted]\n\"T1-2027/G3/use case\" = \"2026-12-05\"\n\"T1-2027/G3/used\" = \"2027-01-20\"\n")
	c := s.File.Cycles[0]
	g := c.Goals[1]
	want := []State{Accepted, Open, Locked, Locked}
	for i, w := range want {
		if st := s.State(snap(), c, g, i); st != w {
			t.Fatalf("rung %d state %v, want %v", i, st, w)
		}
	}
	if earned, _ := s.Score(snap(), c, g); math.Abs(earned-25*100/105.0) > 1e-9 {
		t.Fatalf("ladder earned %v", earned)
	}
	if len(s.Warning) != 1 {
		t.Fatalf("want one warning for G3 summing to 105, got %v", s.Warning)
	}
}

func TestNoBeadUsesSidecarBead(t *testing.T) {
	s := load(t, "[beads]\n\"T1-2027/G3/use case\" = \"t-a\"\n")
	c := s.File.Cycles[0]
	g := c.Goals[1]
	if st := s.State(snap(), c, g, 0); st != Done {
		t.Fatalf("sidecar bead state %v, want Done", st)
	}
	if tags := s.Tags(c); len(tags["t-a"]) != 2 {
		t.Fatalf("t-a scores for G1 and G3, tags %v", tags)
	}
}

func TestAcceptanceAfterPastCycleEndIgnored(t *testing.T) {
	s := load(t, "[accepted]\n\"T1-2027/G1/SR-141\" = \"2027-03-02\"\n")
	c := s.File.Cycles[0]
	if st := s.State(snap(), c, c.Goals[0], 0); st != Done {
		t.Fatalf("late acceptance state %v, want Done", st)
	}
}

func TestCurrentCycle(t *testing.T) {
	s := load(t, "")
	for _, tc := range []struct {
		now  string
		want int
	}{{"2026-11-20", 0}, {"2027-02-28", 0}, {"2027-03-01", 1}, {"2027-09-01", 1}} {
		if got := s.Current(date(tc.now).Add(12 * time.Hour)); got != tc.want {
			t.Fatalf("%s: cycle %d, want %d", tc.now, got, tc.want)
		}
	}
	c := s.File.Cycles[0]
	if p := c.TimePct(date("2026-12-01")); p != 0 {
		t.Fatalf("time at start %v", p)
	}
	if p := c.TimePct(date("2027-03-01")); p != 100 {
		t.Fatalf("time at end %v", p)
	}
}

func TestSaveSidecarRoundTrip(t *testing.T) {
	s := load(t, "")
	s.Side.Accepted["T1-2027/G1/SR-141"] = "2026-12-10"
	s.Side.Beads["T1-2027/G3/use case"] = "t-x"
	if err := s.SaveSidecar(); err != nil {
		t.Fatal(err)
	}
	r, err := Load(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Side.Accepted["T1-2027/G1/SR-141"] != "2026-12-10" || r.Side.Beads["T1-2027/G3/use case"] != "t-x" {
		t.Fatalf("round trip lost data: %+v", r.Side)
	}
}

func TestSidecarProblemsWarn(t *testing.T) {
	s := load(t, "[accepted]\n\"T1-2027/G1/SR-141\" = \"10.12.2026\"\n\"T1-2027/G1/SR-999\" = \"2026-12-10\"\n")
	c := s.File.Cycles[0]
	if st := s.State(snap(), c, c.Goals[0], 0); st != Done {
		t.Fatalf("unreadable date state %v, want Done", st)
	}
	var orphan, badDate bool
	for _, w := range s.Warning {
		orphan = orphan || strings.Contains(w, "SR-999") && strings.Contains(w, "matches no item")
		badDate = badDate || strings.Contains(w, "10.12.2026")
	}
	if !orphan || !badDate {
		t.Fatalf("warnings %v", s.Warning)
	}
}

func TestUnknownScoringRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "goals.toml")
	src := "[[cycle]]\nid = \"T\"\nstart = 2026-01-01\nend = 2026-02-01\n[[cycle.goal]]\nid = \"G\"\nscoring = \"steps\"\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "steps") {
		t.Fatalf("scoring steps: err %v", err)
	}
	if s := load(t, ""); s.File.Cycles[0].Goals[0].Scoring != "points" {
		t.Fatal("empty scoring should default to points")
	}
}

func TestDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	old := time.Local
	time.Local = loc
	defer func() { time.Local = old }()
	c := &Cycle{Start: date("2027-03-01"), End: date("2027-06-30")}
	if e, n := c.Days(date("2027-04-01").Add(10 * time.Hour)); n != 122 || e != 31 {
		t.Fatalf("days %d/%d, want 31/122", e, n)
	}
}
