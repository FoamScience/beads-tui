package dispatch

import "github.com/FoamScience/beads-tui/internal/bd"

// Ledgers lists the live ledgers under the bead's ancestors, nearest first.
func Ledgers(s *bd.Snapshot, is *bd.Issue) []string {
	var out []string
	for p := s.ByID[is.Parent]; p != nil; p = s.ByID[p.Parent] {
		for _, c := range s.Children[p.ID] {
			if c.IsLedger() {
				out = append(out, c.ID)
			}
		}
	}
	return out
}
