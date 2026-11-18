package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/dispatch"
	"github.com/charmbracelet/x/ansi"
)

func TestForgottenBeads(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	issues := []bd.Issue{
		{ID: "t-o", Title: "orphan", IssueType: "task", Status: "in_progress", UpdatedAt: old.Add(-time.Hour), Metadata: []byte(`{"claude_session.dead":"p"}`)},
		{ID: "t-e", Title: "epic", IssueType: "epic", Status: "open", UpdatedAt: old, Metadata: []byte(`{"claude_session.dead2":"p"}`)},
		{ID: "t-e.1", Parent: "t-e", Title: "left", IssueType: "task", Status: "open", UpdatedAt: old},
		{ID: "t-e.2", Parent: "t-e", Title: "parked", IssueType: "task", Status: "deferred", UpdatedAt: old},
		{ID: "t-s", Title: "stalled", IssueType: "task", Status: "in_progress", UpdatedAt: old, Metadata: []byte(`{"claude_session.idle":"p"}`)},
		{ID: "t-f", Title: "fresh", IssueType: "task", Status: "in_progress", UpdatedAt: now, Metadata: []byte(`{"claude_session.idle":"p"}`)},
		{ID: "t-w", Title: "working", IssueType: "task", Status: "in_progress", UpdatedAt: old, Metadata: []byte(`{"claude_session.busy":"p"}`)},
		{ID: "t-b", Title: "blocked", IssueType: "task", Status: "in_progress", UpdatedAt: old, Metadata: []byte(`{"claude_session.dead":"p"}`),
			Dependencies: []bd.Dependency{{IssueID: "t-b", DependsOnID: "t-o", Type: "blocks"}}},
		{ID: "t-g", Title: "lane epic", IssueType: "epic", Status: "open", UpdatedAt: old},
		{ID: "t-g.1", Parent: "t-g", Title: "queued", IssueType: "task", Status: "open", UpdatedAt: old},
		{ID: "t-m", Title: "moving epic", IssueType: "epic", Status: "open", UpdatedAt: old, Metadata: []byte(`{"claude_session.dead2":"p"}`)},
		{ID: "t-m.1", Parent: "t-m", Title: "child", IssueType: "task", Status: "open", UpdatedAt: old},
		{ID: "t-m.2", Parent: "t-m", Title: "worked", IssueType: "task", Status: "in_progress", UpdatedAt: now, Metadata: []byte(`{"claude_session.busy":"p"}`)},
		{ID: "t-c", Title: "done", IssueType: "task", Status: "closed", UpdatedAt: old, Metadata: []byte(`{"claude_session.dead":"p"}`)},
	}
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.Update(herdrMsg{panes: map[string]agentPane{
		"idle": {Pane: "w1:p1", Status: "idle", Title: "sleepy"},
		"busy": {Pane: "w1:p2", Status: "working", Title: "t-x"},
	}})
	a.Update(pulseMsg{"idle": {turnEnd: now.Add(-45 * time.Minute)}, "busy": {turnStart: now.Add(-time.Minute)}})
	cfg, err := dispatch.ParseConfig("[lane.infra]\ncwd = \"/tmp\"\ntakes = { epics = [\"t-g\"] }\n")
	if err != nil {
		t.Fatal(err)
	}
	a.Update(lanesMsg{cfg: cfg, allow: dispatch.Allow{Epics: []string{"t-g"}}})

	got := map[string]string{}
	var order []string
	for _, f := range a.forgotten() {
		got[f.is.ID] = f.why
		order = append(order, f.is.ID)
	}
	want := map[string]string{"t-o": "orphaned", "t-e": "abandoned +1", "t-s": "stalled 45m", "t-g.1": "lane gap infra"}
	for id, why := range want {
		if got[id] != why {
			t.Errorf("%s: %q, want %q (all: %v)", id, got[id], why, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("flagged %v, want only %v", got, want)
	}
	if strings.Join(order, ",") != "t-o,t-e,t-s,t-g.1" {
		t.Errorf("order %v: ended sessions first, then stalled, then lane gaps, oldest update first", order)
	}

	a.active = a.viewIndex("Activity")
	out := ansi.Strip(a.render())
	if !strings.Contains(out, "Needs attention") || !strings.Contains(out, "stalled 45m") {
		t.Fatalf("Activity lacks the attention section:\n%s", out)
	}

	if msg, ok := a.resume(a.snap.ByID["t-g.1"])().(flashMsg); !ok || !strings.Contains(msg.text, "bt dispatch") {
		t.Errorf("R on a lane gap should point to bt dispatch, got %+v", msg)
	}

	a.Update(herdrMsg{})
	if fs := a.forgotten(); fs != nil {
		t.Errorf("without herdr nothing can be told ended, got %v", fs)
	}
}
