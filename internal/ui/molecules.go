package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

type (
	molDataMsg struct {
		formulas []bd.FormulaSummary
		wisps    []bd.Wisp
		stale    []bd.StaleMolecule
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
)

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
	wisp    *bd.Wisp
}

type moleculesView struct {
	items      []molItem
	cursor     int
	offset     int
	formulas   []bd.FormulaSummary
	wisps      []bd.Wisp
	stale      map[string]bool
	details    map[string]*bd.Formula
	loading    bool
	loaded     bool
	showClosed bool
	preview    *graphView
}

func newMolecules() *moleculesView {
	return &moleculesView{stale: map[string]bool{}, details: map[string]*bd.Formula{}, preview: newGraph()}
}

func (v *moleculesView) Name() string { return "Molecules" }

func (v *moleculesView) formulaDir(a *App) string { return filepath.Join(a.client.Dir, "formulas") }

// Activate loads formulas and wisps the first time the tab opens.
func (v *moleculesView) Activate(a *App) tea.Cmd {
	if v.loaded || v.loading {
		return nil
	}
	return v.load(a)
}

func (v *moleculesView) load(a *App) tea.Cmd {
	v.loading = true
	c := a.client
	return func() tea.Msg {
		var m molDataMsg
		m.formulas, m.err = c.Formulas()
		if m.err != nil {
			return m
		}
		m.wisps, _ = c.Wisps()
		m.stale, _ = c.Stale()
		return m
	}
}

func (v *moleculesView) loadFormula(a *App, name string) tea.Cmd {
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

func (v *moleculesView) isMol(a *App, id string) bool {
	return strings.HasPrefix(id, a.prefix+"mol-") || strings.HasPrefix(id, a.prefix+"wisp-")
}

// isMolRoot matches the root of a poured molecule or wisp; children share the id family.
func (v *moleculesView) isMolRoot(a *App, is *bd.Issue) bool {
	if is.IssueType == "molecule" {
		return true
	}
	if !v.isMol(a, is.ID) {
		return false
	}
	p := a.snap.ByID[is.Parent]
	return p == nil || !v.isMol(a, p.ID)
}

func (v *moleculesView) Rebuild(a *App) {
	var keep string
	if it := v.current(); it != nil {
		keep = v.itemKey(*it)
	}
	v.items = v.items[:0]
	v.items = append(v.items, molItem{header: "Formulas"})
	for i := range v.formulas {
		v.items = append(v.items, molItem{formula: &v.formulas[i]})
	}
	var mols []*bd.Issue
	for _, is := range a.snap.Issues {
		if v.isMolRoot(a, is) && (v.showClosed || !is.Closed() || v.stale[is.ID]) {
			mols = append(mols, is)
		}
	}
	sort.Slice(mols, func(i, j int) bool { return mols[i].UpdatedAt.After(mols[j].UpdatedAt) })
	v.items = append(v.items, molItem{header: "Molecules"})
	for _, is := range mols {
		v.items = append(v.items, molItem{issue: is})
	}
	if len(v.wisps) > 0 {
		v.items = append(v.items, molItem{header: "Wisps"})
		for i := range v.wisps {
			v.items = append(v.items, molItem{wisp: &v.wisps[i]})
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

func (v *moleculesView) itemKey(it molItem) string {
	switch {
	case it.formula != nil:
		return "f:" + it.formula.Name
	case it.issue != nil:
		return "i:" + it.issue.ID
	case it.wisp != nil:
		return "w:" + it.wisp.ID
	}
	return ""
}

func (v *moleculesView) current() *molItem {
	if v.cursor < 0 || v.cursor >= len(v.items) || v.items[v.cursor].header != "" {
		return nil
	}
	return &v.items[v.cursor]
}

func (v *moleculesView) move(d int) {
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

func (v *moleculesView) Selected() *bd.Issue {
	it := v.current()
	if it == nil {
		return nil
	}
	return v.itemIssue(it)
}

func (v *moleculesView) itemIssue(it *molItem) *bd.Issue {
	switch {
	case it.issue != nil:
		return it.issue
	case it.wisp != nil:
		return &bd.Issue{ID: it.wisp.ID, Title: it.wisp.Title, Status: it.wisp.Status, IssueType: "epic"}
	}
	return nil
}

func (v *moleculesView) Msg(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case molDataMsg:
		v.loading, v.loaded = false, true
		if m.err != nil {
			return flashErr(m.err)
		}
		v.formulas, v.wisps = m.formulas, m.wisps
		v.stale = map[string]bool{}
		for _, s := range m.stale {
			v.stale[s.ID] = true
		}
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
		delete(v.details, m.name)
		v.loaded = false
		return tea.Batch(v.load(a), v.cook(a, m.name))
	case dryRunMsg:
		if m.err != nil {
			return flashErr(m.err)
		}
		v.confirmSpawn(a, m.spec, m.out)
		return nil
	}
	return nil
}

func (v *moleculesView) selectionCmd(a *App) tea.Cmd {
	if it := v.current(); it != nil && it.formula != nil {
		return v.loadFormula(a, it.formula.Name)
	}
	return nil
}

// cook validates a formula after editing; bd reports the parse error with its location.
func (v *moleculesView) cook(a *App, name string) tea.Cmd {
	c := a.client
	return func() tea.Msg {
		if _, err := c.Output("cook", name); err != nil {
			return flashMsg{text: "cook " + name + ": " + err.Error(), err: true}
		}
		return flashMsg{text: "cook " + name + ": ok"}
	}
}

func (v *moleculesView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
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
	case "H":
		v.showClosed = !v.showClosed
		v.Rebuild(a)
		return true, nil
	case "r":
		v.loaded, v.details = false, map[string]*bd.Formula{}
		return true, tea.Batch(v.load(a), a.load())
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
	case "Q", "B":
		if it == nil || it.formula != nil {
			return true, flash("select a molecule")
		}
		is := v.itemIssue(it)
		if k.String() == "Q" {
			a.modal = newConfirm("Squash "+a.shortID(is.ID)+" into a digest?", func() tea.Cmd {
				return v.after(a, a.write("squash "+a.shortID(is.ID), nil, "mol", "squash", is.ID))
			})
		} else {
			a.modal = newConfirm("Burn "+a.shortID(is.ID)+"? This deletes the molecule and cannot be undone.", func() tea.Cmd {
				return v.after(a, a.write("burn "+a.shortID(is.ID), nil, "mol", "burn", is.ID, "--force"))
			})
		}
		return true, nil
	}
	return false, nil
}

// after chains a molecule-list reload behind a write.
func (v *moleculesView) after(a *App, cmd tea.Cmd) tea.Cmd {
	v.loaded = false
	return tea.Sequence(cmd, v.load(a))
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

func (v *moleculesView) newFormula(a *App, name string) tea.Cmd {
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

func (v *moleculesView) distillModal(a *App) *modal {
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
func (v *moleculesView) askVars(a *App, spec spawnSpec, f *bd.Formula) tea.Cmd {
	names := make([]string, 0, len(f.Vars))
	for n := range f.Vars {
		names = append(names, n)
	}
	sort.Strings(names)
	var ask func(i int) tea.Cmd
	ask = func(i int) tea.Cmd {
		if i == len(names) {
			c := a.client
			return func() tea.Msg {
				out, err := c.DryRun(spec.formula, spec.wisp, spec.vars)
				return dryRunMsg{spec, out, err}
			}
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
				spec.vars[n] = val
			}
			return ask(i + 1)
		})
		return nil
	}
	return ask(0)
}

// confirmSpawn shows the dry run, then asks where the new root goes so it never lands as a stray.
func (v *moleculesView) confirmSpawn(a *App, spec spawnSpec, preview string) {
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
					return v.after(a, v.spawn(a, spec))
				})
				return nil
			})
			return nil
		})
		return nil
	})
}

func (v *moleculesView) spawn(a *App, spec spawnSpec) tea.Cmd {
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

func (v *moleculesView) Hints() []string {
	return []string{"p pour", "w wisp", "n new", "E edit", "d distill", "Q squash", "B burn", "h/l steps", "H closed"}
}

func (v *moleculesView) Render(a *App, w, h int) string {
	if !v.loaded {
		return sDim.Render("\n  loading formulas…")
	}
	lw := min(max(w/3, 34), 52)
	left := v.renderList(a, lw, h)
	right := v.renderPreview(a, w-lw-3, h)
	sep := strings.TrimRight(strings.Repeat(sRule.Render("│")+"\n", h), "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(lw).Height(h).MaxHeight(h).Render(left), " ", sep, " ",
		lipgloss.NewStyle().Height(h).MaxHeight(h).Render(right))
}

func (v *moleculesView) renderList(a *App, w, h int) string {
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
			done, total := a.snap.Progress(is.ID)
			title := is.Title
			if sel {
				title = sSel.Render(title)
			}
			tag := sDim.Render(fmt.Sprintf(" %d/%d", done, total))
			if v.stale[is.ID] {
				tag += sWarn.Render(" stale")
			}
			line = marker + glyph(a.snap, is) + " " + title + tag
		case it.wisp != nil:
			title := it.wisp.Title
			if sel {
				title = sSel.Render(title)
			}
			line = marker + sDim.Render("~ ") + title
		}
		lines = append(lines, ansi.Truncate(line, w, "…"))
	}
	if len(v.formulas) == 0 {
		lines = append(lines, sDim.Render("  no formulas yet, n creates one"))
	}
	return strings.Join(lines, "\n")
}

func (v *moleculesView) renderPreview(a *App, w, h int) string {
	it := v.current()
	if it == nil {
		return ""
	}
	if it.formula != nil {
		return v.formulaPreview(a, it.formula, w, h)
	}
	is := v.itemIssue(it)
	if a.snap.ByID[is.ID] == nil {
		return sDim.Render("\n  " + is.ID + " is not in the local snapshot")
	}
	g := v.preview
	if g.fixed != nil || g.rootID != is.ID {
		g.fixed, g.showClosed = nil, true
		g.rootID, g.sel, g.offX, g.offY = is.ID, "", 0, 0
	}
	g.Rebuild(a)
	return g.Render(a, w, h)
}

func (v *moleculesView) formulaPreview(a *App, s *bd.FormulaSummary, w, h int) string {
	f := v.details[s.Name]
	var b strings.Builder
	b.WriteString(sBold.Render(s.Name) + sDim.Render("  "+s.Type))
	if f != nil && f.Phase != "" {
		b.WriteString(sDim.Render(" · " + f.Phase))
	}
	b.WriteString("\n" + sDim.Render(strings.Replace(s.Source, os.Getenv("HOME"), "~", 1)) + "\n")
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
