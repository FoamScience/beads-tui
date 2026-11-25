package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

// TestWispActions drives Q, B and X on a wisp root row against a fake bd that logs its args.
func TestWispActions(t *testing.T) {
	lastCall := fakeBD(t, `case "$*" in *"gc --dry-run"*) printf '{"cleaned_ids":["t-wisp-1"],"issue_ids":["t-wisp-gone","t-wisp-1"]}';; esac`)
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	now := time.Now()
	issues := append(fixture(),
		bd.Issue{ID: "t-wisp-1", Title: "patrol", IssueType: "molecule", Status: "open", Ephemeral: true, UpdatedAt: now},
		bd.Issue{ID: "t-wisp-2", Parent: "t-wisp-1", Title: "wisp step", IssueType: "task", Status: "open", Ephemeral: true, UpdatedAt: now},
	)
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.switchTo(a.viewIndex("Workflows"))
	v := a.views[a.active].(*workflowsView)
	v.Msg(a, molDataMsg{})
	for i, it := range v.items {
		if it.issue != nil && it.issue.ID == "t-wisp-1" {
			v.cursor = i
		}
	}
	key := func(k string) { v.Update(a, tea.KeyPressMsg{Code: rune(k[0]), Text: k}) }

	key("Q")
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "mol squash t-wisp-1" {
		t.Fatalf("Q on a wisp root ran bd %q", got)
	}
	key("B")
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "mol burn t-wisp-1 --force" {
		t.Fatalf("B on a wisp root ran bd %q", got)
	}

	key("X")
	if cmd := a.modal.onSubmit([]string{"foo"}); cmd != nil {
		t.Fatal("typed text that matches no gc mode must not start a preview")
	}
	for mode, want := range map[string]string{"abandoned": "mol wisp gc --dry-run --json", "closed": "mol wisp gc --dry-run --closed --json"} {
		m := a.modal.onSubmit([]string{mode})().(wispGCMsg)
		if got := lastCall(); got != want || m.mode != mode || m.err != nil {
			t.Fatalf("%s preview ran bd %q (want %q), msg %+v", mode, got, want, m)
		}
		if got := strings.Join(m.ids, " "); got != "t-wisp-1 t-wisp-gone" {
			t.Fatalf("%s preview ids %s, want each id once", mode, got)
		}
	}

	cmd := v.Msg(a, wispGCMsg{mode: "closed"})
	if m, ok := cmd().(flashMsg); !ok || !strings.Contains(m.text, "nothing to collect") {
		t.Fatalf("empty dry run flash = %#v", m)
	}
	v.Msg(a, wispGCMsg{mode: "closed", ids: []string{"t-wisp-1", "t-wisp-gone"}})
	if !strings.Contains(a.modal.title, "Delete 2 closed wisp rows on every machine") ||
		!strings.Contains(a.modal.title, "patrol") || !strings.Contains(a.modal.title, "wisp-gone") {
		t.Fatalf("gc confirm should count and list the dry run's ids: %q", a.modal.title)
	}
	a.modal.onSubmit(nil)()
	if got := lastCall(); got != "delete --force t-wisp-1 t-wisp-gone" {
		t.Fatalf("gc confirm ran bd %q, want a delete of exactly the previewed ids", got)
	}
}
