package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoamScience/beads-tui/internal/bd"
)

const lanesTOML = `
max_active = 2
[lane.story]
cwd = "~/repo/game"
takes = { labels = ["area:story"] }
[lane.quests]
cwd = "~/repo/game"
workspace = "game"
takes = { labels = ["area:quests"], epics = ["g"] }
`

func config(t *testing.T) *Config {
	t.Helper()
	c, err := ParseConfig(lanesTOML)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseConfig(t *testing.T) {
	c := config(t)
	if got := c.Names(); strings.Join(got, ",") != "quests,story" {
		t.Fatalf("names %v", got)
	}
	if l := c.Lanes["story"]; l.Name != "story" || strings.HasPrefix(l.Cwd, "~") {
		t.Fatalf("lane defaults not applied: %+v", l)
	}
	for _, bad := range []string{`[lane.x]` + "\nbogus = 1", `[lane.x]` + "\nrevive = \"resume\""} {
		if _, err := ParseConfig(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRoute(t *testing.T) {
	c := config(t)
	s := bd.NewSnapshot([]bd.Issue{
		{ID: "g", IssueType: "epic", Status: "open"},
		{ID: "g.1", Parent: "g", Status: "open"},
		{ID: "x.1", Status: "open", Labels: []string{"area:story"}},
		{ID: "x.2", Status: "open", Labels: []string{"area:story", "lane:quests"}},
		{ID: "x.3", Status: "open", Labels: []string{"area:story", "area:quests"}},
		{ID: "x.4", Status: "open"},
		{ID: "x.5", Status: "open", Labels: []string{"lane:nope", "area:story"}},
	})
	want := map[string]string{"g.1": "quests", "x.1": "story", "x.2": "quests", "x.3": "", "x.4": "", "x.5": ""}
	for id, lane := range want {
		if got := Route(c, s, s.ByID[id]); got != lane {
			t.Errorf("%s routed to %q, want %q", id, got, lane)
		}
	}
}

func TestStateOf(t *testing.T) {
	s := bd.NewSnapshot([]bd.Issue{
		{ID: "a", Status: "open"},
		{ID: "b", Status: "open", Dependencies: []bd.Dependency{{IssueID: "b", DependsOnID: "a", Type: "blocks"}}},
		{ID: "c", Status: "in_progress"},
		{ID: "d", Status: "in_progress", Labels: []string{"partial"}},
		{ID: "e", Status: "closed"},
		{ID: "f", Status: "open", IssueType: "epic"},
	})
	want := map[string]Progress{"a": Queued, "b": Waiting, "c": InProgress, "d": Partial, "e": Completed, "f": Other}
	for id, p := range want {
		if got := StateOf(s, s.ByID[id]); got != p {
			t.Errorf("%s is %s, want %s", id, got, p)
		}
	}
}

func TestPlan(t *testing.T) {
	c := config(t)
	now := time.Now()
	issues := []bd.Issue{
		{ID: "s.1", Status: "open", Priority: 1, Labels: []string{"area:story"}},
		{ID: "s.2", Status: "open", Priority: 2, Labels: []string{"area:story"}},
		{ID: "s.3", Status: "in_progress", Priority: 3, Assignee: "story", Labels: []string{"area:story", "partial"}},
		{ID: "q.1", Status: "open", Labels: []string{"area:quests"}},
	}
	idle := []Pane{{ID: "p1", Agent: "claude", Status: "idle", Title: "s.3", Tab: "t1"}}
	all := Allow{Lanes: []string{"story", "quests"}}
	fresh := func(a Allow) *State { st := NewState(); st.Allow = a; return st }

	if acts := Plan(c, bd.NewSnapshot(issues), idle, NewState(), now); len(acts) != 0 {
		t.Fatalf("empty allow-list dispatched: %v", acts)
	}

	st := fresh(all)
	acts := Plan(c, bd.NewSnapshot(issues), idle, st, now)
	if len(acts) != 2 || acts[0].Kind != Spawn || acts[0].Bead != "q.1" || acts[0].Text != "/work q.1 (lane: quests)" ||
		acts[1].Kind != Send || acts[1].Bead != "s.3" || acts[1].Pane != "p1" {
		t.Fatalf("partial bead not sent to its own session, or new bead not spawned: %v", acts)
	}
	if again := Plan(c, bd.NewSnapshot(issues), idle, st, now.Add(time.Second)); len(again) != 0 {
		t.Fatalf("resent before pickup: %v", again)
	}
	late := Plan(c, bd.NewSnapshot(issues), idle, st, now.Add(pickupTimeout+time.Second))
	if len(late) != 3 || late[0].Kind != Note || late[0].Bead != "q.1" || late[1].Kind != Note || late[2].Bead != "s.3" {
		t.Fatalf("want spawn give-up, pickup timeout note and re-offer: %v", late)
	}
	if later := Plan(c, bd.NewSnapshot(issues), idle, st, now.Add(2*pickupTimeout)); len(later) != 0 {
		t.Fatalf("failed spawn retried or bead resent early: %v", later)
	}

	issues[2].Status, issues[2].Labels = "closed", []string{"area:story"}
	acts = Plan(c, bd.NewSnapshot(issues), idle, fresh(Allow{Lanes: []string{"story"}, Beads: []string{"s.2"}}), now)
	if len(acts) != 2 || acts[0].Kind != Close || acts[0].Pane != "p1" || acts[0].Tab != "t1" || acts[1].Kind != Note || acts[1].Bead != "s.2" {
		t.Fatalf("closed bead's session kept, or listed bead not ahead of priority: %v", acts)
	}
	shared := append([]Pane{{ID: "p2", Agent: "zsh", Tab: "t1"}}, idle...)
	if acts = Plan(c, bd.NewSnapshot(issues), shared, fresh(Allow{}), now); len(acts) != 1 || acts[0].Kind != Close || acts[0].Tab != "" {
		t.Fatalf("closed a tab another pane shares: %v", acts)
	}
	c.Lanes["story"].Workspace = "game"
	acts = Plan(c, bd.NewSnapshot(issues), nil, fresh(Allow{Beads: []string{"s.2"}}), now)
	if len(acts) != 1 || acts[0].Kind != Spawn || acts[0].Bead != "s.2" {
		t.Fatalf("a listed bead alone must go, and nothing else: %v", acts)
	}

	issues[0].Status, issues[0].Assignee = "in_progress", "story"
	if acts = Plan(c, bd.NewSnapshot(issues), nil, fresh(all), now); len(acts) != 1 || acts[0].Lane != "quests" {
		t.Fatalf("lane with active bead got more work: %v", acts)
	}

	c.MaxActive = 1
	issues[0].Status, issues[0].Assignee = "open", ""
	working := []Pane{{ID: "p1", Agent: "claude", Status: "working", Title: "s.1"}}
	if acts = Plan(c, bd.NewSnapshot(issues), working, fresh(all), now); len(acts) != 0 {
		t.Fatalf("max_active exceeded: %v", acts)
	}
}

func TestDecider(t *testing.T) {
	c, err := ParseConfig(lanesTOML + "[decider]\nmodel = \"haiku\"\n")
	if err != nil {
		t.Fatal(err)
	}
	s := bd.NewSnapshot([]bd.Issue{
		{ID: "g", IssueType: "epic", Status: "open"},
		{ID: "g.1", Parent: "g", IssueType: "epic", Status: "open"},
		{ID: "g.1.1", Parent: "g.1", Status: "open"},
		{ID: "g.1.2", Parent: "g.1", Status: "open", Labels: []string{"area:story"}},
		{ID: "g.1.3", Parent: "g.1", Status: "in_progress"},
		{ID: "o.1", Status: "open"},
		{ID: "g.2", Parent: "g", Status: "open"},
		{ID: "g.2.1", Parent: "g.2", Status: "in_progress"},
	})
	c.Lanes["quests"].Takes.Epics = nil
	tried := map[string]bool{}
	a := Allow{Epics: []string{"g"}, Beads: []string{"o.1"}}
	if ask := Unrouted(c, s, Allow{}, tried); len(ask) != 0 {
		t.Fatalf("decider asked about beads nothing allows: %v", ask)
	}
	ask := Unrouted(c, s, Allow{Epics: []string{"g"}}, tried)
	if len(ask) != 1 || ask[0].ID != "g.1.1" {
		t.Fatalf("asked %v, want only the unrouted ready bead in scope", ask)
	}
	if !strings.Contains(DeciderPrompt(c, s, ask), "id g.1.1") {
		t.Fatal("prompt lacks the bead")
	}
	out := "g.1.1\tdialog work\tstory\no.1\tnot asked\tstory\ng.1.1\tdup\tquests\nnoise"
	if vs := ParseVerdicts(c, ask, out); len(vs) != 1 || vs[0] != (Verdict{ID: "g.1.1", Lane: "story", Why: "dialog work"}) {
		t.Fatalf("verdicts %v", vs)
	}
	if vs := ParseVerdicts(c, ask, "g.1.1\tno fit\tnone\ng.1.1\tx\tghost"); len(vs) != 0 {
		t.Fatalf("none or unknown lane accepted: %v", vs)
	}
	if ask := Unrouted(c, s, a, tried); len(ask) != 2 {
		t.Fatalf("listed bead not asked: %v", ask)
	}
	tried["g.1.1"], tried["o.1"] = true, true
	if ask := Unrouted(c, s, a, tried); len(ask) != 0 {
		t.Fatalf("asked twice: %v", ask)
	}
}

func TestAllow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.toml")
	if a, err := LoadAllow(path); err != nil || len(a.Lanes)+len(a.Epics)+len(a.Beads) != 0 {
		t.Fatalf("missing file must allow nothing: %v %+v", err, a)
	}
	want := Allow{Lanes: []string{"game"}, Epics: []string{"tasks-i3g.159"}, Beads: []string{"x.1"}}
	if err := SaveAllow(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAllow(path)
	if err != nil || strings.Join(got.Lanes, ",") != "game" || got.Epics[0] != "tasks-i3g.159" || got.Beads[0] != "x.1" {
		t.Fatalf("round trip: %v %+v", err, got)
	}
	for _, bad := range []string{"lanes = [\"game\"\n", "lane = [\"game\"]\n"} {
		if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAllow(path); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if l := Toggle(Toggle([]string{"a"}, "b"), "a"); strings.Join(l, ",") != "b" {
		t.Fatalf("toggle %v", l)
	}
	s := bd.NewSnapshot([]bd.Issue{{ID: "e", IssueType: "epic"}, {ID: "e.1", Parent: "e"}, {ID: "z"}})
	if !(Allow{Epics: []string{"e"}}).Permits(s, s.ByID["e.1"], "") || (Allow{Lanes: []string{"game"}}).Permits(s, s.ByID["z"], "") {
		t.Fatal("permits by epic, or a lane allow leaked to an unrouted bead")
	}
}

func TestAutoLanes(t *testing.T) {
	dir := t.TempDir()
	lanes := filepath.Join(dir, "lanes.toml")
	if err := os.WriteFile(lanes, []byte(lanesTOML+"[auto]\nmax_lanes = 1\ncwd = \"~/repo/game\"\nworkspace = \"game\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(lanes)
	if err != nil {
		t.Fatal(err)
	}
	ask := []*bd.Issue{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	out := "a\tfits story\tstory\nb\tno lane fits\tnew:game-ui\tHUD and screens\nc\tbad\tnew:Bad Name"
	vs := ParseVerdicts(c, ask, out)
	if len(vs) != 2 || vs[0].New || !vs[1].New || vs[1].Lane != "game-ui" || vs[1].About != "HUD and screens" {
		t.Fatalf("verdicts %+v", vs)
	}
	if !strings.Contains(DeciderPrompt(c, bd.NewSnapshot(nil), ask), "new:<name>") {
		t.Fatal("prompt does not offer new lanes while under the cap")
	}
	if _, err := c.NewAutoLane("story", ""); err == nil {
		t.Fatal("opened a lane that exists")
	}
	if _, err := c.NewAutoLane("Bad Name", ""); err == nil {
		t.Fatal("accepted a bad lane name")
	}
	l, err := c.NewAutoLane("game-ui", "HUD and screens")
	if err != nil || l.Workspace != "game" || strings.HasPrefix(l.Cwd, "~") {
		t.Fatalf("auto lane %+v %v", l, err)
	}
	if err := SaveAutoLane(filepath.Join(dir, "lanes.auto.toml"), l); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(lanes)
	if err != nil || c.Lanes["game-ui"] == nil || !c.Lanes["game-ui"].Auto || c.Lanes["story"].Auto {
		t.Fatalf("reload: %v %+v", err, c.Lanes["game-ui"])
	}
	if _, err := c.NewAutoLane("game-net", ""); err == nil {
		t.Fatal("cap ignored")
	}
	if strings.Contains(DeciderPrompt(c, bd.NewSnapshot(nil), ask), "new:<name>") {
		t.Fatal("prompt offers new lanes at the cap")
	}
	c.Auto = nil
	if vs := ParseVerdicts(c, ask, out); len(vs) != 1 {
		t.Fatalf("new lanes accepted with [auto] unset: %+v", vs)
	}
}

func TestDeleteLane(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lanes.toml")
	src := "# my lanes\nmax_active = 2\n\n[lane.story] # the story lane\ncwd = \"/x\"\nowns = [\n  [\"nested\", \"array\"],\n]\n\n[lane.story.takes]\nlabels = [\"area:story\"]\n\n# quests below\n[lane.quests]\ncwd = \"/y\"\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLane(path, "story", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if want := "# my lanes\nmax_active = 2\n\n# quests below\n[lane.quests]\ncwd = \"/y\"\n"; string(b) != want {
		t.Fatalf("got %q\nwant %q", b, want)
	}
	if err := DeleteLane(path, "ghost", false); err == nil {
		t.Fatal("deleted a lane that is not there")
	}
	l := &Lane{Name: "game-ui", Cwd: "/z"}
	if err := SaveAutoLane(AutoPath(path), l); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLane(path, "game-ui", true); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadConfig(path); err != nil || c.Lanes["game-ui"] != nil || c.Lanes["quests"] == nil {
		t.Fatalf("auto delete: %v %v", err, c)
	}
	if err := DeleteLane(path, "game-ui", true); err == nil {
		t.Fatal("deleting a missing auto lane reported success")
	}
}
