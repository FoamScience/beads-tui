package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

type lintMsg struct {
	id  string
	res string
	ok  bool
}

type epicsView struct {
	listView
	expanded   map[string]bool
	showClosed bool
	lint       map[string]lintMsg
}

func newEpics() *epicsView {
	return &epicsView{
		listView: listView{empty: "no epics", issueList: issueList{noteW: 15}},
		expanded: map[string]bool{},
		lint:     map[string]lintMsg{},
	}
}

func (v *epicsView) Name() string { return "Epics" }

func (v *epicsView) Rebuild(a *App) {
	var rows []row
	var walk func(is *bd.Issue, depth int)
	walk = func(is *bd.Issue, depth int) {
		rows = append(rows, row{issue: is, indent: depth, note: v.note(a, is)})
		for _, c := range a.snap.Children[is.ID] {
			if c.IssueType == "epic" && (v.showClosed || !c.Closed()) {
				walk(c, depth+1)
			} else if v.expanded[is.ID] && c.IssueType != "epic" && (v.showClosed || !c.Closed()) {
				rows = append(rows, row{issue: c, indent: depth + 1})
			}
		}
	}
	var roots []*bd.Issue
	for _, is := range a.snap.Issues {
		if is.IssueType != "epic" || (!v.showClosed && is.Closed()) {
			continue
		}
		if p := a.snap.Epic(is); p == nil || (!v.showClosed && p.Closed()) {
			roots = append(roots, is)
		}
	}
	byPriority(roots)
	for i, r := range roots {
		if i > 0 {
			rows = append(rows, row{spacer: true})
		}
		walk(r, 0)
	}
	v.SetRows(rows)
}

func (v *epicsView) note(a *App, is *bd.Issue) string {
	done, total := a.snap.Progress(is.ID)
	n := progressBar(done, total, 5) + fmt.Sprintf(" %d/%d", done, total)
	if l, ok := v.lint[is.ID]; ok {
		if l.ok {
			n = sOK.Render("✓ ") + n
		} else {
			n = sWarn.Render("⚠ ") + n
		}
	}
	return n
}

func (v *epicsView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	sel := v.Selected()
	switch k.String() {
	case "z":
		if sel != nil && sel.IssueType == "epic" {
			v.expanded[sel.ID] = !v.expanded[sel.ID]
			v.Rebuild(a)
		}
		return true, nil
	case "H":
		v.showClosed = !v.showClosed
		v.Rebuild(a)
		return true, nil
	case "L":
		if sel == nil || sel.IssueType != "epic" {
			return true, nil
		}
		id := sel.ID
		c := a.client
		return true, tea.Batch(flash("linting "+a.shortID(id)+"…"), func() tea.Msg {
			out, err := c.Output("swarm", "validate", id)
			if err != nil {
				return lintMsg{id, err.Error(), false}
			}
			res := string(out)
			return lintMsg{id, res, strings.Contains(res, "Swarmable: YES") && !strings.Contains(res, "⚠")}
		})
	}
	return v.HandleKey(k.String()), nil
}

func (v *epicsView) Msg(a *App, msg tea.Msg) tea.Cmd {
	if m, ok := msg.(lintMsg); ok {
		v.lint[m.id] = m
		v.Rebuild(a)
		if m.ok {
			return flash(a.shortID(m.id) + ": swarmable, no warnings")
		}
		return flashErr(fmt.Errorf("%s: %s", a.shortID(m.id), lintSummary(m.res)))
	}
	return nil
}

func lintSummary(out string) string {
	var warn []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.Contains(l, "⚠") || strings.Contains(strings.ToLower(l), "cycle") || strings.HasPrefix(l, "✗") {
			warn = append(warn, l)
		}
	}
	if len(warn) == 0 {
		return strings.TrimSpace(out)
	}
	return strings.Join(warn, " · ")
}

func (v *epicsView) Hints() []string {
	return []string{"z expand", "L lint", "H closed", "a new child"}
}
