package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

func fixture() []bd.Issue {
	now := time.Now().UTC()
	started := now.Add(-5 * time.Hour)
	return []bd.Issue{
		{ID: "t-a", Title: "Epic A with a rather long title that must be truncated somewhere", IssueType: "epic", Status: "open", Priority: 1, UpdatedAt: now},
		{ID: "t-a.1", Parent: "t-a", Title: "Active child", IssueType: "task", Status: "in_progress", Priority: 0, UpdatedAt: now.Add(-72 * time.Hour), StartedAt: &started,
			Labels: []string{"machine:u-host"}, EstimatedMinutes: 90, Notes: "first note\nsecond note", Description: "# Heading\n\nbody"},
		{ID: "t-a.2", Parent: "t-a", Title: "Blocked child", IssueType: "bug", Status: "open", Priority: 2, UpdatedAt: now,
			Dependencies: []bd.Dependency{{IssueID: "t-a.2", DependsOnID: "t-a.1", Type: "blocks"}}},
		{ID: "t-a.3", Parent: "t-a", Title: "Ready child", IssueType: "task", Status: "open", Priority: 2, UpdatedAt: now},
		{ID: "t-b", Title: "Orphan done", IssueType: "task", Status: "closed", Priority: 3, UpdatedAt: now},
	}
}

func TestViewsFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{60, 15}, {80, 24}, {120, 40}, {160, 50}} {
		for tab := range 7 {
			for _, enter := range []bool{false, true} {
				name := fmt.Sprintf("%dx%d/tab%d/enter=%v", size[0], size[1], tab+1, enter)
				a := New(bd.Client{Dir: t.TempDir()})
				a.state, a.host = defaultState(), "u-host"
				a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				a.Update(snapshotMsg{issues: fixture()})
				a.active = tab
				if tab == 5 {
					a.views[5].(*graphView).setRoot(a, a.snap.ByID["t-a.1"])
				}
				if enter {
					a.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				}
				out := a.render()
				if tab == 0 && !strings.Contains(out, "Active child") {
					t.Errorf("%s: Now shows no fixture rows", name)
				}
				lines := strings.Split(out, "\n")
				if len(lines) != size[1] {
					t.Errorf("%s: %d lines, want %d", name, len(lines), size[1])
				}
				for i, l := range lines {
					if w := lipgloss.Width(l); w > size[0] {
						t.Errorf("%s: line %d is %d wide: %q", name, i, w, l)
					}
				}
			}
		}
	}
}

func TestReadyAndBlocked(t *testing.T) {
	s := bd.NewSnapshot(fixture())
	if !s.IsBlocked(s.ByID["t-a.2"]) {
		t.Error("t-a.2 should be blocked by in-progress t-a.1")
	}
	if !ready(s, s.ByID["t-a.3"]) || ready(s, s.ByID["t-a.2"]) || ready(s, s.ByID["t-a"]) {
		t.Error("ready: want only t-a.3")
	}
	if !bd.IDLess("t-a.2", "t-a.10") || bd.IDLess("t-a.10", "t-a.2") {
		t.Error("IDLess must compare segments numerically")
	}
}

func TestCloseParentOffersCascade(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state = defaultState()
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	a.closeFlow("t-a")
	if a.modal == nil || !strings.Contains(a.modal.title, "3 open descendant") {
		t.Fatalf("want cascade picker, got %+v", a.modal)
	}
	if a.modal.options[0].value != "cascade" {
		t.Fatalf("first option %q", a.modal.options[0].value)
	}
	if got := a.openDescendants("t-a"); len(got) != 3 || got[len(got)-1] == "t-a" {
		t.Fatalf("descendants %v", got)
	}
	a.modal = nil
	a.closeFlow("t-a.3")
	if a.modal == nil || a.modal.picker {
		t.Fatal("leaf without blockers should go straight to the reason prompt")
	}
}
