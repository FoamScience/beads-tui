package ui

import (
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/FoamScience/beads-tui/internal/dispatch"
)

// stallIdle is how long a live session may sit idle or blocked on a bead nobody updated before the
// bead counts as stalled.
const stallIdle = 30 * time.Minute

// attention is a bead nobody is moving, and why.
type attention struct {
	is   *bd.Issue
	why  string
	rank int // orphaned or abandoned, then stalled, then lane gaps
}

type lanesMsg struct {
	cfg   *dispatch.Config
	allow dispatch.Allow
}

// loadLanes reads lanes.toml and dispatch.toml for the lane gaps; a missing or broken lanes.toml means no lanes.
func loadLanes() tea.Msg {
	cfg, err := dispatch.LoadConfig(dispatch.ConfigPath())
	if err != nil {
		return lanesMsg{}
	}
	allow, _ := dispatch.LoadAllow(dispatch.AllowPath())
	return lanesMsg{cfg, allow}
}

// forgotten lists the open, unblocked beads nobody is moving: claimed by sessions that all ended
// (orphaned when in progress, abandoned when work is left under it), claimed by live sessions idle
// or blocked for stallIdle on a bead untouched as long (stalled), or next in an allowed lane queue
// with no session (lane gap). It needs herdr to tell live sessions from ended ones.
func (a *App) forgotten() []attention {
	if a.herdr == nil {
		return nil
	}
	busyKids := map[string]int{}
	for _, g := range a.groupAgents(a.agents()) {
		if g.head == nil {
			continue
		}
		for _, k := range g.kids {
			if a.busy(k.id) {
				busyKids[g.head.id]++
			}
		}
	}
	var out []attention
	for _, is := range a.snap.Issues {
		if is.Closed() || is.Status == "deferred" || a.snap.IsBlocked(is) || !a.machineMatch(is) {
			continue
		}
		ss := sessions(is)
		var live []string
		for _, s := range ss {
			if _, ok := a.herdr[s.id]; ok {
				live = append(live, s.id)
			}
		}
		switch {
		case len(ss) == 0:
		case len(live) == 0:
			n, moving := a.openBelow(is)
			switch {
			case moving:
			case n > 0:
				out = append(out, attention{is, fmt.Sprintf("abandoned +%d", n), 0})
			case is.Status == "in_progress":
				out = append(out, attention{is, "orphaned", 0})
			}
		case time.Since(is.UpdatedAt) >= stallIdle:
			if quiet, ok := a.quietSince(live, busyKids); ok {
				out = append(out, attention{is, "stalled " + age(quiet), 1})
			}
		}
	}
	listed := map[string]bool{}
	for _, f := range out {
		listed[f.is.ID] = true
	}
	for _, f := range a.laneGaps() {
		if !listed[f.is.ID] {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].is.UpdatedAt.Before(out[j].is.UpdatedAt)
	})
	return out
}

// quietSince is when the last of the live sessions went idle or blocked, if every one of them has been
// for stallIdle.
func (a *App) quietSince(live []string, busyKids map[string]int) (time.Time, bool) {
	var quiet time.Time
	for _, id := range live {
		st, _, since, _ := a.liveOf(id, busyKids[id])
		if st < lsBlocked || time.Since(since) < stallIdle {
			return time.Time{}, false
		}
		quiet = latest(quiet, since)
	}
	return quiet, true
}

// openBelow counts the descendants of is that are neither closed nor deferred, and tells whether a
// live session holds any of them, in which case the work under is is still moving.
func (a *App) openBelow(is *bd.Issue) (n int, moving bool) {
	for _, c := range a.snap.Children[is.ID] {
		if c.Closed() || c.Status == "deferred" {
			continue
		}
		n++
		for _, s := range sessions(c) {
			if _, ok := a.herdr[s.id]; ok {
				return n, true
			}
		}
		m, mv := a.openBelow(c)
		if mv {
			return n, true
		}
		n += m
	}
	return n, false
}

// laneGaps are the beads bt dispatch would send next to lanes that have no session and no active bead.
func (a *App) laneGaps() []attention {
	if a.lanes == nil {
		return nil
	}
	var panes []dispatch.Pane
	for id, p := range a.herdr {
		panes = append(panes, dispatch.Pane{ID: p.Pane, Agent: "claude", Status: p.Status, Title: p.Title, Session: id, Tab: p.Tab})
	}
	var out []attention
	for name, v := range dispatch.Lanes(a.lanes, a.snap, panes, a.allow) {
		if v.Live || v.Active != "" {
			continue
		}
		for _, is := range v.Queue {
			if a.allow.Permits(a.snap, is, name) {
				if a.machineMatch(is) {
					out = append(out, attention{is, "lane gap " + name, 2})
				}
				break
			}
		}
	}
	return out
}
