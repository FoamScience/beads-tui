package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

func searchFixture() *searchIndex {
	issues := []*bd.Issue{
		{ID: "t-1", Title: "Pump report viewer colours", Description: "colour range controls for the report"},
		{ID: "t-2", Title: "Migrations cannot replay", Description: "a fresh laravel migrate fails because seeders moved on"},
		{ID: "t-3", Title: "Cleanup stage archives nothing", Notes: "the slurm job dir is removed before tar runs"},
		{ID: "t-4", Title: "Unrelated chore", Labels: []string{"repo:x"}},
		{ID: "t-5", Title: "Second report about migrate", Description: "report migrate"},
	}
	heads := make([]string, len(issues))
	for i, is := range issues {
		heads[i] = is.ID + " " + is.Title + " " + strings.Join(is.Labels, " ")
	}
	return newSearchIndex(issues, heads)
}

func TestSearchRanksSubstringHitsFirstThenBodyMatches(t *testing.T) {
	x := searchFixture()
	if got := x.rank(""); !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("empty query = %v, want every issue in order", got)
	}
	if got := x.rank("report migrate"); len(got) == 0 || got[0] != 4 {
		t.Fatalf("phrase in a title should lead: %v", got)
	}
	// "migrate report" is no substring anywhere, so BM25 order: t-5 has both words, t-1 and t-2 one each
	if got := x.rank("migrate report"); len(got) < 3 || got[0] != 4 {
		t.Fatalf("both words should beat one: %v", got)
	}
	if got := x.rank("laravel"); !slices.Equal(got, []int{1}) {
		t.Fatalf("description-only word: %v", got)
	}
	if got := x.rank("slurm"); !slices.Equal(got, []int{2}) {
		t.Fatalf("notes-only word: %v", got)
	}
	if got := x.rank("lara"); !slices.Equal(got, []int{1}) {
		t.Fatalf("last word should match as a prefix while typing: %v", got)
	}
	if got := x.rank("lara "); len(got) != 0 {
		t.Fatalf("a finished word is exact: %v", got)
	}
	if got := x.rank("repo:x"); !slices.Equal(got, []int{3}) {
		t.Fatalf("label substring: %v", got)
	}
	if got := x.rank("zzqx"); len(got) != 0 {
		t.Fatalf("no hits: %v", got)
	}
}

func TestSearchModalUsesTheIndex(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	m := a.searchModal()
	if len(m.visible) != len(fixture()) {
		t.Fatalf("empty search shows %d of %d", len(m.visible), len(fixture()))
	}
	m.input.SetValue("second note")
	m.filter()
	if len(m.visible) != 1 || m.options[m.visible[0]].value != "t-a.1" {
		t.Fatalf("notes match: %v", m.visible)
	}
}

func TestSearchFollowsMachineFilterUntilWidened(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.state.Machine = "elsewhere"
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	m := a.searchModal()
	for _, i := range m.visible {
		if m.options[i].value == "t-a.1" {
			t.Fatal("u-host's bead shown under the elsewhere filter")
		}
	}
	if len(m.visible) != len(fixture())-1 || !strings.Contains(m.title, "elsewhere") {
		t.Fatalf("visible %d, title %q", len(m.visible), m.title)
	}
	m.update(tea.KeyPressMsg{Code: tea.KeyTab})
	if len(m.visible) != len(fixture()) || !strings.Contains(m.title, "all machines") {
		t.Fatalf("tab should widen: visible %d, title %q", len(m.visible), m.title)
	}
	m.update(tea.KeyPressMsg{Code: tea.KeyTab})
	if len(m.visible) != len(fixture())-1 {
		t.Fatalf("second tab should narrow again: %d", len(m.visible))
	}
}

func TestSearchSurvivesReloadWhileOpen(t *testing.T) {
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(snapshotMsg{issues: fixture(), gen: a.gen})
	m := a.searchModal()
	a.Update(snapshotMsg{issues: fixture()[:2], gen: a.gen})
	if a.search != nil {
		t.Fatal("reload should drop the cached index")
	}
	m.input.SetValue("ready")
	m.filter()
	if len(m.visible) != 1 || m.options[m.visible[0]].value != "t-a.3" {
		t.Fatalf("open search keeps its own snapshot: %v", m.visible)
	}
}
