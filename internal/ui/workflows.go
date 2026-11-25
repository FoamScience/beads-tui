package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

type (
	molDataMsg struct {
		formulas []bd.FormulaSummary
		err      error
	}
	formulaMsg struct {
		name string
		f    bd.Formula
		err  error
	}
	editDoneMsg struct {
		name string
		err  error
	}
	dryRunMsg struct {
		spec spawnSpec
		out  string
		err  error
	}
	wispGCMsg struct {
		mode string
		ids  []string
		err  error
	}
	bondFormulaMsg struct {
		b   bondSpec
		f   bd.Formula
		err error
	}
	bondDryRunMsg struct {
		b   bondSpec
		out string
		err error
	}
)

// bondSpec is a bd mol bond onto a molecule: other is a formula name or another molecule's id.
type bondSpec struct {
	mol, other, typ string
	formula         bool
	vars            map[string]string
}

type spawnSpec struct {
	formula string
	wisp    bool
	vars    map[string]string
	parent  string
	title   string
	labels  []string
}

type molItem struct {
	header  string
	formula *bd.FormulaSummary
	issue   *bd.Issue
	src     *bd.Snapshot // the snapshot holding issue: a.snap, a.snap.Ephemeral or a.snap.Gates
	st      molStatus
	stale   bool
}

// molStatus holds what bd mol progress, current, last-activity and ready --gated report, read
// from the snapshot rather than bd, whose reads would take the Dolt lock. bd counts direct children
// as steps; here a step is a leaf work item at any depth (a parallel-bonded sub-molecule is a
// container, not a step), a gate is a wait condition rather than a step, and the last activity
// spans every descendant.
type molStatus struct {
	done, total int
	current     []*bd.Issue // in progress or hooked
	ready       []*bd.Issue
	waiting     []*bd.Issue // open gates under the root
	resume      []*bd.Issue // ready steps whose gate closed: bd mol ready --gated
	last        time.Time
	lastStep    *bd.Issue // nil when the root itself moved last
}

func statusOf(s *bd.Snapshot, root *bd.Issue) molStatus {
	st := molStatus{last: root.UpdatedAt}
	hooked := root.Status == "hooked"
	var walk func(id string)
	walk = func(id string) {
		for _, c := range s.Children[id] {
			hooked = hooked || c.Status == "hooked"
			if c.UpdatedAt.After(st.last) {
				st.last, st.lastStep = c.UpdatedAt, c
			}
			switch {
			case c.IssueType == "gate":
				if !c.Closed() {
					st.waiting = append(st.waiting, c)
				}
			case len(s.Children[c.ID]) > 0:
			default:
				st.total++
				switch {
				case c.Closed():
					st.done++
				case c.Status == "in_progress" || c.Status == "hooked":
					st.current = append(st.current, c)
				case s.Ready(c):
					st.ready = append(st.ready, c)
				}
			}
			walk(c.ID)
		}
	}
	walk(root.ID)
	if !hooked {
		for _, c := range st.ready {
			if slices.ContainsFunc(c.Dependencies, func(d bd.Dependency) bool {
				g := s.Find(d.DependsOnID)
				return bd.Blocking(d.Type) && g != nil && g.IssueType == "gate" && g.Closed()
			}) {
				st.resume = append(st.resume, c)
			}
		}
	}
	return st
}

const minGraphRows = 6

// previewPane puts a molecule's or wisp's status lines under its step graph's title. The graph
// keeps at least minGraphRows rows; status lines give way from the bottom on short terminals.
func (v *workflowsView) previewPane(a *App, w, h int) string {
	it := v.current()
	if it == nil || it.issue == nil || isGate(it) {
		return v.renderPreview(a, w, h)
	}
	lines := molStatusLines(a, it, w)
	lines = lines[:min(len(lines), max(h-minGraphRows, 1))]
	head, rest, _ := strings.Cut(v.renderPreview(a, w, max(h-len(lines), 1)), "\n")
	return head + "\n" + strings.Join(lines, "\n") + "\n" + rest
}

// molStatusLines lists progress, the current or next steps, open gates, gate-resume candidates
// and the last activity.
func molStatusLines(a *App, it *molItem, w int) []string {
	st, s := it.st, it.src
	var lines []string
	kv := func(k, val string) {
		lines = append(lines, ansi.Truncate(" "+sKey.Render(padRight(k, 9))+val, w, "…"))
	}
	pct := 0
	if st.total > 0 {
		pct = st.done * 100 / st.total
	}
	kv("progress", fmt.Sprintf("%d/%d steps · %d%%", st.done, st.total, pct))
	steps := func(k string, xs []*bd.Issue, label func(*bd.Issue) string) {
		for i, x := range xs {
			if i == 2 {
				kv("", sDim.Render(fmt.Sprintf("… %d more", len(xs)-2)))
				break
			}
			kv(k, glyph(s, x)+" "+a.shortID(x.ID)+"  "+label(x))
			k = ""
		}
	}
	title := func(x *bd.Issue) string { return x.Title }
	steps("current", st.current, title)
	if len(st.current) == 0 {
		steps("next", st.ready, title)
	}
	steps("waiting", st.waiting, func(g *bd.Issue) string {
		var ws []string
		for _, x := range waiters(s, g) {
			ws = append(ws, a.shortID(x.ID))
		}
		return "on " + g.AwaitType + " gate" + sDim.Render(", holds "+strings.Join(ws, " "))
	})
	steps("resume", st.resume, title)
	when := age(st.last)
	if when != "now" {
		when += " ago"
	}
	if st.lastStep != nil {
		when += sDim.Render("  " + a.shortID(st.lastStep.ID) + "  " + st.lastStep.Title)
	}
	kv("activity", when)
	return lines
}

type workflowsView struct {
	items      []molItem
	cursor     int
	offset     int
	formulas   []bd.FormulaSummary
	details    map[string]*bd.Formula
	loading    bool
	loaded     bool
	showClosed bool
	preview    *graphView

	nMols, nWisps, nStale, nGates, nResume int
}

func newWorkflows() *workflowsView {
	return &workflowsView{details: map[string]*bd.Formula{}, preview: newGraph()}
}

func (v *workflowsView) Name() string { return "Workflows" }

func (v *workflowsView) formulaDir(a *App) string { return filepath.Join(a.client.Dir, "formulas") }

// Activate loads formulas the first time the tab opens; molecules and wisps come from the snapshot.
func (v *workflowsView) Activate(a *App) tea.Cmd {
	if v.loaded || v.loading {
		return nil
	}
	return v.load(a)
}

func (v *workflowsView) load(a *App) tea.Cmd {
	v.loading = true
	c := a.client
	return func() tea.Msg {
		var m molDataMsg
		m.formulas, m.err = c.Formulas()
		return m
	}
}

func (v *workflowsView) loadFormula(a *App, name string) tea.Cmd {
	if _, ok := v.details[name]; ok {
		return nil
	}
	v.details[name] = nil
	c := a.client
	return func() tea.Msg {
		f, err := c.Formula(name)
		return formulaMsg{name, f, err}
	}
}

func (v *workflowsView) isMol(a *App, id string) bool {
	return strings.HasPrefix(id, a.prefix+"mol-") || strings.HasPrefix(id, a.prefix+"wisp-")
}

// isMolRoot matches the top of a poured molecule or wisp: molecule-typed or in the mol/wisp id
// family, under no parent or one outside both. A step whose root was squashed or burned is no
// root, nor is a sub-molecule nested in another.
func (v *workflowsView) isMolRoot(a *App, is *bd.Issue) bool {
	if is.IssueType != "molecule" && !v.isMol(a, is.ID) {
		return false
	}
	if is.Parent == "" {
		return true
	}
	p := a.snap.Find(is.Parent)
	return p != nil && p.IssueType != "molecule" && !v.isMol(a, p.ID)
}

func (v *workflowsView) Rebuild(a *App) {
	var keep string
	if it := v.current(); it != nil {
		keep = v.itemKey(*it)
	}
	v.items = v.items[:0]
	v.items = append(v.items, molItem{header: "Formulas"})
	for i := range v.formulas {
		v.items = append(v.items, molItem{formula: &v.formulas[i]})
	}
	mols, wisps := v.roots(a, a.snap), v.roots(a, a.snap.Ephemeral)
	v.nMols, v.nWisps, v.nStale, v.nResume = len(mols), len(wisps), 0, 0
	v.items = append(v.items, molItem{header: "Molecules"})
	for _, is := range mols {
		it := rootItem(a.snap, is)
		if it.stale = stale(a.snap, is); it.stale {
			v.nStale++
		}
		v.items = append(v.items, it)
	}
	if len(wisps) > 0 {
		v.items = append(v.items, molItem{header: "Wisps"})
		for _, is := range wisps {
			v.items = append(v.items, rootItem(a.snap.Ephemeral, is))
		}
	}
	for _, it := range v.items {
		if len(it.st.resume) > 0 {
			v.nResume++
		}
	}
	gates := v.gates(a)
	v.nGates = len(gates)
	if len(gates) > 0 {
		v.items = append(v.items, molItem{header: "Gates"})
		for _, is := range gates {
			v.items = append(v.items, molItem{issue: is, src: a.snap.Gates})
		}
	}
	v.cursor = 1
	for i, it := range v.items {
		if it.header == "" && v.itemKey(it) == keep {
			v.cursor = i
		}
	}
	v.move(0)
}

// roots lists the molecule or wisp roots in src that pass the machine filter, newest first.
func (v *workflowsView) roots(a *App, src *bd.Snapshot) []*bd.Issue {
	var out []*bd.Issue
	for _, is := range src.Issues {
		if v.isMolRoot(a, is) && a.machineMatch(is) && (v.showClosed || !is.Closed()) {
			out = append(out, is)
		}
	}
	slices.SortFunc(out, func(x, y *bd.Issue) int { return y.UpdatedAt.Compare(x.UpdatedAt) })
	return out
}

// gates lists the gates whose waiters pass the machine filter (a gate with no waiter always
// shows), newest first.
func (v *workflowsView) gates(a *App) []*bd.Issue {
	var out []*bd.Issue
	for _, is := range a.snap.Gates.Issues {
		if !v.showClosed && is.Closed() {
			continue
		}
		ws := waiters(a.snap.Gates, is)
		if len(ws) == 0 || slices.ContainsFunc(ws, a.machineMatch) {
			out = append(out, is)
		}
	}
	slices.SortFunc(out, func(x, y *bd.Issue) int { return y.UpdatedAt.Compare(x.UpdatedAt) })
	return out
}

// waiters returns the issues a gate blocks.
func waiters(s *bd.Snapshot, gate *bd.Issue) []*bd.Issue {
	var out []*bd.Issue
	for _, d := range s.Dependents(gate.ID) {
		if bd.Blocking(d.Type) {
			if w := s.Find(d.IssueID); w != nil {
				out = append(out, w)
			}
		}
	}
	return out
}

func isGate(it *molItem) bool { return it != nil && it.issue != nil && it.issue.IssueType == "gate" }

func rootItem(src *bd.Snapshot, is *bd.Issue) molItem {
	return molItem{issue: is, src: src, st: statusOf(src, is)}
}

// stale matches bd mol stale: an open root whose direct children are all closed. Wisps never
// call it, since all steps closed under an open root is how a wisp waits for its squash.
func stale(s *bd.Snapshot, is *bd.Issue) bool {
	kids := s.Children[is.ID]
	return !is.Closed() && len(kids) > 0 && !slices.ContainsFunc(kids, func(c *bd.Issue) bool { return !c.Closed() })
}

func (v *workflowsView) itemKey(it molItem) string {
	switch {
	case it.formula != nil:
		return "f:" + it.formula.Name
	case it.issue != nil:
		return "i:" + it.issue.ID
	}
	return ""
}

func (v *workflowsView) current() *molItem {
	if v.cursor < 0 || v.cursor >= len(v.items) || v.items[v.cursor].header != "" {
		return nil
	}
	return &v.items[v.cursor]
}

func (v *workflowsView) move(d int) {
	if len(v.items) == 0 {
		return
	}
	step := 1
	if d < 0 {
		step = -1
	}
	i := clamp(v.cursor+d, 0, len(v.items)-1)
	for i >= 0 && i < len(v.items) && v.items[i].header != "" {
		i += step
	}
	if i < 0 || i >= len(v.items) {
		for i = clamp(v.cursor, 0, len(v.items)-1); i >= 0 && v.items[i].header != ""; i-- {
		}
	}
	v.cursor = clamp(i, 0, len(v.items)-1)
}

func (v *workflowsView) Selected() *bd.Issue {
	if it := v.current(); it != nil {
		return it.issue
	}
	return nil
}

func (v *workflowsView) Msg(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case molDataMsg:
		v.loading, v.loaded = false, true
		if m.err != nil {
			return flashErr(m.err)
		}
		v.formulas = m.formulas
		v.Rebuild(a)
		return v.selectionCmd(a)
	case formulaMsg:
		if m.err != nil {
			delete(v.details, m.name)
			return flashErr(m.err)
		}
		v.details[m.name] = &m.f
		return nil
	case distilledMsg:
		return editFile(m.path, m.name)
	case editDoneMsg:
		if m.err != nil {
			return flashErr(m.err)
		}
		return tea.Batch(v.reloadFormulas(a, m.name), v.cook(a, m.name))
	case convertedMsg:
		return v.converted(a, m)
	case dryRunMsg:
		if m.err != nil {
			return flashErr(m.err)
		}
		v.confirmSpawn(a, m.spec, m.out)
		return nil
	case wispGCMsg:
		return confirmGC(a, m)
	case bondFormulaMsg:
		if m.err != nil {
			return flashErr(m.err)
		}
		v.details[m.b.other] = &m.f
		return v.promptVars(a, &m.f, m.b.vars, func() tea.Cmd { return dryRunBond(a, m.b) })
	case bondDryRunMsg:
		if m.err != nil {
			return flashErr(m.err)
		}
		confirmBond(a, m.b, m.out)
		return nil
	}
	return nil
}

func (v *workflowsView) selectionCmd(a *App) tea.Cmd {
	if it := v.current(); it != nil && it.formula != nil {
		return v.loadFormula(a, it.formula.Name)
	}
	return nil
}

// cook validates a formula after editing; bd reports the parse error with its location.
func (v *workflowsView) cook(a *App, name string) tea.Cmd {
	c := a.client
	return func() tea.Msg {
		if _, err := c.Output("cook", name); err != nil {
			return flashMsg{text: "cook " + name + ": " + err.Error(), err: true}
		}
		return flashMsg{text: "cook " + name + ": ok"}
	}
}

func (v *workflowsView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	it := v.current()
	switch k.String() {
	case "j", "down":
		v.move(1)
		return true, v.selectionCmd(a)
	case "k", "up":
		v.move(-1)
		return true, v.selectionCmd(a)
	case "ctrl+d", "pgdown":
		v.move(5)
		return true, v.selectionCmd(a)
	case "ctrl+u", "pgup":
		v.move(-5)
		return true, v.selectionCmd(a)
	case "h", "l", "left", "right":
		_, cmd := v.preview.Update(a, k)
		return true, cmd
	case "J", "K":
		// j/k move the list, so shifted keys walk the step graph vertically
		key := map[string]string{"J": "j", "K": "k"}[k.String()]
		_, cmd := v.preview.Update(a, tea.KeyPressMsg{Code: rune(key[0]), Text: key})
		return true, cmd
	case "H":
		v.showClosed = !v.showClosed
		v.Rebuild(a)
		return true, nil
	case "r":
		v.details = map[string]*bd.Formula{}
		return true, tea.Batch(v.reloadFormulas(a), a.load())
	case "n":
		a.modal = newPrompt("New formula name (lowercase, dashes)", "", func(name string) tea.Cmd {
			return v.newFormula(a, name)
		})
		return true, nil
	case "E":
		if it != nil && it.formula != nil {
			return true, editFile(it.formula.Source, it.formula.Name)
		}
		return true, flash("select a formula to edit")
	case "t":
		if it == nil || it.formula == nil {
			return true, flash("select a formula to convert")
		}
		return true, v.convertModal(a, it.formula)
	case "V":
		if it == nil || it.formula == nil {
			return true, flash("select a formula to seed-check")
		}
		return true, v.seed(a, it.formula.Name)
	case "d":
		a.modal = v.distillModal(a)
		return true, nil
	case "p", "w":
		if it == nil || it.formula == nil {
			return true, flash("select a formula to pour")
		}
		f := v.details[it.formula.Name]
		if f == nil {
			return true, v.loadFormula(a, it.formula.Name)
		}
		return true, v.askVars(a, spawnSpec{formula: f.Name, wisp: k.String() == "w", vars: map[string]string{}}, f)
	case "G":
		if !isGate(it) || it.issue.Closed() {
			return true, flash("select an open gate to resolve")
		}
		g := it.issue
		a.modal = newPrompt("Resolve gate "+a.shortID(g.ID)+": reason (optional)", "", func(r string) tea.Cmd {
			args := []string{"gate", "resolve", g.ID}
			if r = strings.TrimSpace(r); r != "" {
				args = append(args, "--reason", r)
			}
			title := "Resolve gate " + a.shortID(g.ID) + "? This closes it"
			if ws := waiters(a.snap.Gates, g); len(ws) > 0 {
				lines := make([]string, len(ws))
				for i, w := range ws {
					lines[i] = "  " + a.shortID(w.ID) + "  " + w.Title
				}
				title += " and unblocks:\n\n" + strings.Join(lines, "\n")
			}
			a.modal = newConfirm(title, func() tea.Cmd {
				return a.write("resolve gate "+a.shortID(g.ID), a.mut(g.ID, func(i *bd.Issue) { i.Status = "closed" }), args...)
			})
			return nil
		})
		return true, nil
	case "T":
		if !isGate(it) {
			return true, flash("select a gate to check gates")
		}
		n := 0
		for _, g := range a.snap.Gates.Issues {
			if !g.Closed() {
				n++
			}
		}
		a.modal = newConfirm(fmt.Sprintf("Run bd gate check over all %d open gates, on every machine?\n\n"+
			"It evaluates timer, bead, gh:run and gh:pr gates (the gh ones call the gh CLI) and closes\n"+
			"each gate whose condition is met, unblocking its waiters. Human gates stay open; nothing is escalated.", n), func() tea.Cmd {
			return a.write("gate check", nil, "gate", "check")
		})
		return true, nil
	case "Q", "B":
		if it == nil || it.formula != nil || isGate(it) {
			return true, flash("select a molecule")
		}
		is := it.issue
		if k.String() == "Q" {
			a.modal = newConfirm("Squash "+a.shortID(is.ID)+" into a digest?", func() tea.Cmd {
				return a.write("squash "+a.shortID(is.ID), nil, "mol", "squash", is.ID)
			})
		} else {
			a.modal = newConfirm("Burn "+a.shortID(is.ID)+"? This deletes the molecule and cannot be undone.", func() tea.Cmd {
				return a.write("burn "+a.shortID(is.ID), nil, "mol", "burn", is.ID, "--force")
			})
		}
		return true, nil
	case "X":
		a.modal = newPicker("Collect which wisps?", []option{
			{"abandoned", "abandoned: open, untouched for over an hour, not blocked or in progress"},
			{"closed", "closed: every closed wisp"},
		}, func(mode string) tea.Cmd {
			flags, ok := wispGCModes[mode]
			if !ok {
				return nil
			}
			c := a.client
			return func() tea.Msg {
				ids, err := c.WispGCPreview(flags...)
				return wispGCMsg{mode, ids, err}
			}
		})
		return true, nil
	case "+":
		if it == nil || it.formula != nil || isGate(it) || it.issue.Closed() {
			return true, flash("select an open molecule to bond onto")
		}
		a.modal = v.bondModal(a, it.issue)
		return true, nil
	}
	return false, nil
}

var wispGCModes = map[string][]string{"abandoned": nil, "closed": {"--closed"}}

// confirmGC lists what the dry run found and deletes exactly those rows: gc --force would pick its
// set again at run time. gc ignores the machine filter, so the list spans machines.
func confirmGC(a *App, m wispGCMsg) tea.Cmd {
	if m.err != nil {
		return flashErr(m.err)
	}
	if len(m.ids) == 0 {
		return flash("wisp gc: nothing to collect")
	}
	lines := make([]string, 0, len(m.ids))
	for _, id := range m.ids {
		l := "  " + a.shortID(id)
		if is := a.snap.Ephemeral.ByID[id]; is != nil {
			l += "  " + is.Title
		}
		lines = append(lines, l)
	}
	if len(lines) > 14 {
		lines = append(lines[:14], fmt.Sprintf("  … %d more", len(lines)-14))
	}
	title := fmt.Sprintf("Delete %d %s wisp rows on every machine, with no digest?\n\n%s", len(m.ids), m.mode, strings.Join(lines, "\n"))
	a.modal = newConfirm(title, func() tea.Cmd {
		return a.write(fmt.Sprintf("wisp gc: %d rows", len(m.ids)), nil, append([]string{"delete", "--force"}, m.ids...)...)
	})
	return nil
}

var bondTypes = []string{"sequential", "parallel", "conditional"}

// bondModal picks what to bond onto mol (a formula, or another open molecule of the same phase to
// join), then the bond type. A formula's variables are asked for before the dry run. bd joins a
// wisp and a persistent molecule without re-phasing either, so a persistent row could end up with
// a synced blocks edge on a wisp no other machine has; the picker offers same-phase joins only.
func (v *workflowsView) bondModal(a *App, mol *bd.Issue) *modal {
	var opts []option
	for _, f := range v.formulas {
		opts = append(opts, option{"f:" + f.Name, "formula   " + f.Name + sDim.Render(fmt.Sprintf("  %d steps · %d vars", f.Steps, f.Vars))})
	}
	for _, it := range v.items {
		if is := it.issue; is != nil && !isGate(&it) && !is.Closed() && is.ID != mol.ID && is.Ephemeral == mol.Ephemeral {
			opts = append(opts, option{"m:" + is.ID, "molecule  " + a.shortID(is.ID) + "  " + is.Title})
		}
	}
	return newPicker("Bond onto "+a.shortID(mol.ID)+": a formula, or a molecule to join", opts, func(pick string) tea.Cmd {
		kind, other, ok := strings.Cut(pick, ":")
		if !ok {
			return nil
		}
		b := bondSpec{mol: mol.ID, other: other, formula: kind == "f", vars: map[string]string{}}
		what := other
		if !b.formula {
			what = a.shortID(other)
		}
		short := a.shortID(mol.ID)
		a.modal = newPicker("Bond "+what+" onto "+short+" as", []option{
			{"sequential", "sequential: runs after " + short + " completes"},
			{"parallel", "parallel: runs alongside " + short + ", attached under it"},
			{"conditional", "conditional: runs only if " + short + " fails"},
		}, func(typ string) tea.Cmd {
			if !slices.Contains(bondTypes, typ) {
				return nil
			}
			b.typ = typ
			if !b.formula {
				return dryRunBond(a, b)
			}
			if f := v.details[other]; f != nil {
				return v.promptVars(a, f, b.vars, func() tea.Cmd { return dryRunBond(a, b) })
			}
			c := a.client
			return func() tea.Msg {
				f, err := c.Formula(other)
				return bondFormulaMsg{b, f, err}
			}
		})
		return nil
	})
}

func (b bondSpec) args() []string {
	args := []string{"mol", "bond", b.mol, b.other, "--type", b.typ}
	names := make([]string, 0, len(b.vars))
	for n := range b.vars {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		args = append(args, "--var", n+"="+b.vars[n])
	}
	return args
}

func dryRunBond(a *App, b bondSpec) tea.Cmd {
	c := a.client
	return func() tea.Msg {
		out, err := c.Output(append(b.args(), "--dry-run")...)
		return bondDryRunMsg{b, string(out), err}
	}
}

func confirmBond(a *App, b bondSpec, preview string) {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(preview), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	a.modal = newConfirm("Bond onto "+a.shortID(b.mol)+"?\n\n"+strings.Join(lines, "\n"), func() tea.Cmd {
		return bond(a, b)
	})
}

// bond runs bd mol bond. A formula bonded sequential or conditional spawns its own root, which bd
// leaves without labels and mol bond takes none, so it gets this host's machine label as a pour
// does; a parallel bond attaches under mol and a join spawns nothing.
func bond(a *App, b bondSpec) tea.Cmd {
	a.pending++
	c, host := a.client, a.host
	desc := "bonded " + b.other + " onto " + a.shortID(b.mol)
	return func() tea.Msg {
		out, err := c.Output(append(b.args(), "--json")...)
		if err != nil || !b.formula || b.typ == "parallel" {
			return writeDoneMsg{desc, err}
		}
		var r struct {
			IDMapping map[string]string `json:"id_mapping"`
		}
		if err := json.Unmarshal(out, &r); err != nil {
			return writeDoneMsg{desc + ", but reading the new root id failed", err}
		}
		id := r.IDMapping[b.other]
		if id == "" {
			return writeDoneMsg{desc, fmt.Errorf("bd mol bond named no root for %s, so it has no machine label", b.other)}
		}
		if err := c.Exec("update", id, "--add-label", "machine:"+host); err != nil {
			return writeDoneMsg{desc + " as " + id + ", but labelling it failed", err}
		}
		return writeDoneMsg{desc + " as " + id, nil}
	}
}

var formulaName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const formulaScaffold = `formula = %q
description = """
What this workflow does and when to pour it.
"""
type = "workflow"
version = 1

[vars.subject]
description = "what this round is about"
required = true

[[steps]]
id = "start"
title = "%s {{subject}}: first step"
type = "task"
priority = 2
description = """
Acceptance: ...
"""

[[steps]]
id = "finish"
title = "%s {{subject}}: verify and close"
type = "task"
priority = 2
needs = ["start"]
`

func (v *workflowsView) newFormula(a *App, name string) tea.Cmd {
	if !formulaName.MatchString(name) {
		return flashErr(fmt.Errorf("formula name %q: use lowercase letters, digits and dashes", name))
	}
	path := filepath.Join(v.formulaDir(a), name+".formula.toml")
	if _, err := os.Stat(path); err == nil {
		return flashErr(fmt.Errorf("%s already exists", path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return flashErr(err)
	}
	if err := os.WriteFile(path, fmt.Appendf(nil, formulaScaffold, name, name, name), 0o644); err != nil {
		return flashErr(err)
	}
	return editFile(path, name)
}

func editFile(path, name string) tea.Cmd {
	return tea.ExecProcess(editorCmd(path), func(err error) tea.Msg { return editDoneMsg{name, err} })
}

func (v *workflowsView) distillModal(a *App) *modal {
	var opts []option
	for _, is := range a.snap.Issues {
		if is.IssueType == "epic" && !v.isMol(a, is.ID) {
			opts = append(opts, option{is.ID, statusGlyph[is.Status] + " " + a.shortID(is.ID) + "  " + is.Title})
		}
	}
	return newPicker("Distill a formula from epic", opts, func(id string) tea.Cmd {
		a.modal = newPrompt("Formula name for "+a.shortID(id), slug(a.snap.ByID[id].Title), func(name string) tea.Cmd {
			if !formulaName.MatchString(name) {
				return flashErr(fmt.Errorf("formula name %q: use lowercase letters, digits and dashes", name))
			}
			dir := v.formulaDir(a)
			c := a.client
			return func() tea.Msg {
				if _, err := c.Output("mol", "distill", id, name, "--output", dir); err != nil {
					return flashMsg{text: err.Error(), err: true}
				}
				matches, _ := filepath.Glob(filepath.Join(dir, name+".formula.*"))
				if len(matches) == 0 {
					return editDoneMsg{name, fmt.Errorf("distilled %s but found no %s.formula.* in %s", id, name, dir)}
				}
				return distilledMsg{name, matches[0]}
			}
		})
		return nil
	})
}

type distilledMsg struct{ name, path string }

func slug(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s[:min(len(s), 32)]
}

// askVars prompts for each declared variable in name order, then requests a dry run.
func (v *workflowsView) askVars(a *App, spec spawnSpec, f *bd.Formula) tea.Cmd {
	c := a.client
	return v.promptVars(a, f, spec.vars, func() tea.Cmd {
		return func() tea.Msg {
			out, err := c.DryRun(spec.formula, spec.wisp, spec.vars)
			return dryRunMsg{spec, out, err}
		}
	})
}

// promptVars asks for each declared variable in name order into vars, then runs done.
func (v *workflowsView) promptVars(a *App, f *bd.Formula, vars map[string]string, done func() tea.Cmd) tea.Cmd {
	names := make([]string, 0, len(f.Vars))
	for n := range f.Vars {
		names = append(names, n)
	}
	sort.Strings(names)
	var ask func(i int) tea.Cmd
	ask = func(i int) tea.Cmd {
		if i == len(names) {
			return done()
		}
		n := names[i]
		fv := f.Vars[n]
		title := fmt.Sprintf("%s (%d/%d)", n, i+1, len(names))
		if fv.Description != "" {
			title += ": " + fv.Description
		}
		a.modal = newPrompt(title, fv.Default, func(val string) tea.Cmd {
			if val == "" && fv.Required {
				return flashErr(fmt.Errorf("%s is required", n))
			}
			if val != "" {
				vars[n] = val
			}
			return ask(i + 1)
		})
		return nil
	}
	return ask(0)
}

// confirmSpawn shows the dry run, then asks where the new root goes so it never lands as a stray.
func (v *workflowsView) confirmSpawn(a *App, spec spawnSpec, preview string) {
	verb := "Pour"
	if spec.wisp {
		verb = "Wisp"
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(preview), "\n") {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(strings.TrimSpace(l), "⚠") {
			lines = append(lines, l)
		}
	}
	if len(lines) > 14 {
		lines = append(lines[:14], fmt.Sprintf("  … %d more", len(lines)-14))
	}
	a.modal = newConfirm(verb+" "+spec.formula+"?\n\n"+strings.Join(lines, "\n"), func() tea.Cmd {
		opts := []option{{"", "(top level)"}}
		for _, is := range a.snap.Issues {
			if is.IssueType == "epic" && !is.Closed() {
				opts = append(opts, option{is.ID, a.shortID(is.ID) + "  " + is.Title})
			}
		}
		a.modal = newPicker("Parent epic for the new root", opts, func(parent string) tea.Cmd {
			spec.parent = parent
			def := spec.formula
			for _, val := range spec.vars {
				def += " " + val
			}
			a.modal = newPrompt("Root title", def, func(t string) tea.Cmd {
				spec.title = t
				a.modal = newPrompt("Root labels (comma separated)", spec.formula+", machine:"+a.host, func(ls string) tea.Cmd {
					for _, l := range strings.Split(ls, ",") {
						if l = strings.TrimSpace(l); l != "" {
							spec.labels = append(spec.labels, l)
						}
					}
					return v.spawn(a, spec)
				})
				return nil
			})
			return nil
		})
		return nil
	})
}

func (v *workflowsView) spawn(a *App, spec spawnSpec) tea.Cmd {
	a.pending++
	c := a.client
	desc := "poured " + spec.formula
	if spec.wisp {
		desc = "wisped " + spec.formula
	}
	return func() tea.Msg {
		id, err := c.Spawn(spec.formula, spec.wisp, spec.vars)
		if err != nil {
			return writeDoneMsg{desc, err}
		}
		args := []string{"update", id}
		if spec.parent != "" {
			args = append(args, "--parent", spec.parent)
		}
		if spec.title != "" {
			args = append(args, "--title", spec.title)
		}
		for _, l := range spec.labels {
			args = append(args, "--add-label", l)
		}
		if len(args) > 2 {
			if err := c.Exec(args...); err != nil {
				return writeDoneMsg{desc + " as " + id + ", but setting parent/title/labels failed", err}
			}
		}
		return writeDoneMsg{desc + " as " + id, nil}
	}
}

func (v *workflowsView) Hints() []string {
	return []string{"p pour", "w wisp", "+ bond", "n new", "E edit", "t to TOML", "V seed check", "d distill", "Q squash", "B burn", "X wisp gc", "G resolve gate", "T check gates", "h/l J/K steps", "H closed", "M machine"}
}

func (v *workflowsView) Render(a *App, w, h int) string {
	if !v.loaded {
		return strings.Join(header(a, w), "\n") + "\n" + sDim.Render("  loading formulas…")
	}
	wisps := plural(v.nWisps, "wisp")
	switch {
	case a.cached && a.loadErr != nil:
		wisps = "wisps not loaded" // the disk cache leaves wisps out
	case a.cached:
		wisps = "wisps loading…"
	}
	chips := []string{sDim.Render(plural(len(v.formulas), "formula") + " · " + plural(v.nMols, "molecule") + " · " + wisps)}
	if v.nStale > 0 {
		chips = append(chips, sWarn.Render(fmt.Sprintf("%d stale", v.nStale)))
	}
	if v.nGates > 0 {
		chips = append(chips, sDim.Render(fmt.Sprintf("%d gates", v.nGates)))
	}
	if v.nResume > 0 {
		chips = append(chips, sAccent.Render(fmt.Sprintf("%d to resume", v.nResume)))
	}
	top := header(a, w, chips...)
	bar := strings.Join(top, "\n")
	h -= len(top)
	lw := min(max(w/4, 28), 40)
	left := v.renderList(a, lw, h)
	right := v.previewPane(a, w-lw-3, h)
	sep := strings.TrimRight(strings.Repeat(sRule.Render("│")+"\n", h), "\n")
	return bar + "\n" + lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(lw).Height(h).MaxHeight(h).Render(left), " ", sep, " ",
		lipgloss.NewStyle().Height(h).MaxHeight(h).Render(right))
}

func (v *workflowsView) renderList(a *App, w, h int) string {
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+h {
		v.offset = v.cursor - h + 1
	}
	var lines []string
	for i := v.offset; i < min(len(v.items), v.offset+h); i++ {
		it := v.items[i]
		sel := i == v.cursor
		marker := "  "
		if sel {
			marker = sAccent.Render("▸ ")
		}
		var line string
		switch {
		case it.header != "":
			line = " " + sSection.Render(it.header)
		case it.formula != nil:
			f := it.formula
			name := f.Name
			if sel {
				name = sSel.Render(name)
			}
			line = marker + name + sDim.Render(fmt.Sprintf("  %d steps · %d vars", f.Steps, f.Vars))
		case it.issue != nil:
			is := it.issue
			title := is.Title
			if sel {
				title = sSel.Render(title)
			}
			tag := sDim.Render(fmt.Sprintf(" %d/%d", it.st.done, it.st.total))
			if isGate(&it) {
				tag = sDim.Render(" " + is.AwaitType)
			}
			if it.stale {
				tag += sWarn.Render(" stale")
			}
			if len(it.st.resume) > 0 {
				tag += sAccent.Render(" resume")
			}
			line = marker + glyph(it.src, is) + " " + title + tag
		}
		lines = append(lines, ansi.Truncate(line, w, "…"))
	}
	if len(v.formulas) == 0 {
		lines = append(lines, sDim.Render("  no formulas yet, n creates one"))
	}
	return strings.Join(lines, "\n")
}

func (v *workflowsView) renderPreview(a *App, w, h int) string {
	it := v.current()
	if it == nil {
		return ""
	}
	if it.formula != nil {
		return v.formulaPreview(a, it.formula, w, h)
	}
	if isGate(it) {
		return gatePreview(a, it.issue, w, h)
	}
	g := v.preview
	if g.rootID != it.issue.ID {
		g.rootID, g.sel, g.offX, g.offY = it.issue.ID, "", 0, 0
	}
	g.fixed, g.showClosed = nil, true
	if it.issue.Ephemeral {
		g.fixed = it.src
	}
	g.Rebuild(a)
	return g.Render(a, w, h)
}

// gatePreview fits h rows: the waiter list gives way first, the action hint stays on the last row.
func gatePreview(a *App, g *bd.Issue, w, h int) string {
	s := a.snap.Gates
	lines := []string{glyph(s, g) + " " + sBold.Render(g.Title) + sDim.Render("  "+a.shortID(g.ID)+" · "+g.Status)}
	kv := func(k, val string) {
		if val != "" {
			lines = append(lines, " "+sKey.Render(k)+" "+val)
		}
	}
	kv("await", g.AwaitType)
	kv("on", g.AwaitID)
	if d := g.Timeout.Duration(); d > 0 {
		kv("timeout", d.String()+sDim.Render("  (expires "+g.CreatedAt.Add(d).Local().Format("Jan 2 15:04")+")"))
	}
	kv("created", age(g.CreatedAt)+" ago")
	kv("notify", strings.Join(g.Waiters, ", "))
	if d := strings.TrimSpace(g.Description); d != "" {
		desc := strings.Split(ansi.Wordwrap(d, w, " "), "\n")
		if len(desc) > 4 {
			desc = append(desc[:3], "…")
		}
		lines = append(append(lines, ""), desc...)
	}
	var tail []string
	if !g.Closed() {
		tail = []string{"", sDim.Render("G resolve · T check all gates · c close")}
	}
	ws := waiters(s, g)
	lines = append(lines, "", sSection.Render(fmt.Sprintf("Blocks (%d)", len(ws))))
	room := max(h-len(lines)-len(tail), 0)
	for i, x := range ws {
		if i == room-1 && len(ws) > room {
			lines = append(lines, sDim.Render(fmt.Sprintf(" … %d more", len(ws)-i)))
			break
		}
		if i == room {
			break
		}
		lines = append(lines, " "+glyph(s.Of(x), x)+" "+a.shortID(x.ID)+"  "+x.Title)
	}
	if len(lines)+len(tail) > h {
		lines = lines[:max(h-len(tail), 0)]
	}
	lines = append(lines, tail...)
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], w, "…")
	}
	return strings.Join(lines, "\n")
}

func (v *workflowsView) formulaPreview(a *App, s *bd.FormulaSummary, w, h int) string {
	f := v.details[s.Name]
	var b strings.Builder
	b.WriteString(sBold.Render(s.Name) + sDim.Render("  "+s.Type))
	if f != nil && f.Phase != "" {
		b.WriteString(sDim.Render(" · " + f.Phase))
	}
	b.WriteString("\n" + sDim.Render(shortHome(s.Source)) + "\n")
	if f == nil {
		return b.String() + sDim.Render("\nloading…")
	}
	desc := strings.Split(strings.TrimSpace(f.Description), "\n")
	if len(desc) > 4 {
		desc = append(desc[:4], "…")
	}
	b.WriteString("\n" + ansi.Wordwrap(strings.Join(desc, "\n"), w, " ") + "\n")
	if len(f.Vars) > 0 {
		b.WriteString("\n" + sSection.Render("Vars") + "\n")
		names := make([]string, 0, len(f.Vars))
		for n := range f.Vars {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fv := f.Vars[n]
			req := ""
			if fv.Required {
				req = sWarn.Render(" required")
			}
			if fv.Default != "" {
				req += sDim.Render(" = " + fv.Default)
			}
			b.WriteString(ansi.Truncate(" "+sKey.Render(n)+req+sDim.Render("  "+fv.Description), w, "…") + "\n")
		}
	}
	b.WriteString("\n" + sSection.Render(fmt.Sprintf("Steps (%d)", len(f.Steps))) + "\n")
	head := b.String()
	g := v.preview
	if g.fixed == nil || g.rootID != f.Name {
		g.fixed, g.showClosed = f.Snapshot(), true
		g.rootID, g.sel, g.offX, g.offY = f.Name, "", 0, 0
	}
	g.Rebuild(a)
	rest := h - strings.Count(head, "\n")
	if rest < 6 {
		return head
	}
	graph := g.Render(a, w, rest)
	if i := strings.Index(graph, "\n\n"); i >= 0 {
		graph = graph[i+2:]
	}
	return head + graph
}
