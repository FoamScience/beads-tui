package dispatch

import (
	"testing"
	"time"

	"github.com/FoamScience/beads-tui/internal/bd"
)

func TestLedgerDispatch(t *testing.T) {
	c := config(t)
	issues := []bd.Issue{
		{ID: "g", IssueType: "epic", Status: "open"},
		{ID: "g.1", Parent: "g", Status: "open", Labels: []string{"area:quests"}},
		{ID: "g.2", Parent: "g", Status: "pinned", Labels: []string{"live", "area:quests"}},
		{ID: "g.3", Parent: "g", Status: "pinned"},
	}
	s := bd.NewSnapshot(issues)
	st := NewState()
	st.Allow = Allow{Lanes: []string{"quests"}, Beads: []string{"g.2"}}
	acts := Plan(c, s, nil, st, time.Now())
	if len(acts) != 1 || acts[0].Bead != "g.1" || acts[0].Text != "/work g.1 (lane: quests; live ledgers: g.2)" {
		t.Fatalf("want g.1 sent with its ledger and the ledger itself never sent: %v", acts)
	}
}
