package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

type commentsMsg struct {
	id       string
	comments []bd.Comment
	err      error
}

// detail renders one issue; content is rebuilt when the issue, its comments or the width change.
type detail struct {
	vp        viewport.Model
	id        string
	key       string
	comments  map[string][]bd.Comment
	collapsed map[string]bool
	md        *glamour.TermRenderer
	mdWidth   int
	dark      bool
}

func newDetail() *detail {
	vp := viewport.New()
	return &detail{vp: vp, comments: map[string][]bd.Comment{}, collapsed: map[string]bool{}, dark: true}
}

func (d *detail) loadComments(a *App, is *bd.Issue) tea.Cmd {
	if is == nil || is.CommentCount == 0 {
		return nil
	}
	if cached, ok := d.comments[is.ID]; ok && (cached == nil || len(cached) == is.CommentCount) {
		return nil
	}
	d.comments[is.ID] = nil
	id, c := is.ID, a.client
	return func() tea.Msg {
		cs, err := c.Comments(id)
		return commentsMsg{id, cs, err}
	}
}

func (d *detail) markdown(s string, w int) string {
	if d.md == nil || d.mdWidth != w {
		style := "dark"
		if !d.dark {
			style = "light"
		}
		r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(w))
		if err != nil {
			return s
		}
		d.md, d.mdWidth = r, w
	}
	out, err := d.md.Render(s)
	if err != nil {
		return s
	}
	return strings.Trim(out, "\n")
}

func (d *detail) render(a *App, is *bd.Issue, w, h int) string {
	if is == nil {
		return sDim.Render("\n  nothing selected")
	}
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
	cs, fetched := d.comments[is.ID]
	key := fmt.Sprintf("%s|%s|%s|%d|%s|%d|%d|%d|%v|%v|%v|%v", is.ID, is.UpdatedAt, is.Status, is.Priority, is.ExternalRef, is.EstimatedMinutes,
		w, len(cs), fetched && cs != nil, a.snap.Loaded, d.collapsed, d.dark)
	if key != d.key {
		if is.ID != d.id {
			d.vp.GotoTop()
		}
		d.id, d.key = is.ID, key
		d.vp.SetContent(d.content(a, is, w))
	}
	return d.vp.View()
}

var detailSections = []string{"description", "design", "acceptance", "notes", "comments", "deps", "children", "refs"}

func (d *detail) content(a *App, is *bd.Issue, w int) string {
	s := a.snap
	var b strings.Builder
	line := func(parts ...string) { b.WriteString(" " + strings.Join(parts, "  ") + "\n") }

	head := fmt.Sprintf("%s %s  %s  %s  %s", glyph(s, is), sBold.Render(is.ID), is.IssueType, prio(is.Priority), is.Status)
	if is.Status == "open" && s.IsBlocked(is) {
		head += sErr.Render(" (blocked)")
	}
	line(head)
	for _, l := range strings.Split(ansi.Wordwrap(is.Title, w-2, " "), "\n") {
		line(sBold.Render(l))
	}
	var meta []string
	if p := s.ByID[is.Parent]; p != nil {
		meta = append(meta, sDim.Render("parent ")+a.shortID(p.ID)+" "+ansi.Truncate(p.Title, 40, "…"))
	}
	if is.ExternalRef != "" {
		meta = append(meta, sDim.Render("ext ")+is.ExternalRef)
	}
	meta = append(meta, sDim.Render("est ")+minutes(is.EstimatedMinutes))
	line(meta...)
	var who []string
	if is.Assignee != "" {
		who = append(who, sDim.Render("assignee ")+is.Assignee)
	}
	who = append(who, sDim.Render("updated ")+age(is.UpdatedAt))
	if is.StartedAt != nil {
		who = append(who, sDim.Render("started ")+age(*is.StartedAt))
	}
	if is.DeferUntil != nil {
		who = append(who, sDim.Render("defer until ")+is.DeferUntil.Format("2006-01-02"))
	}
	if is.ClosedAt != nil {
		who = append(who, sDim.Render("closed ")+age(*is.ClosedAt))
	}
	line(who...)
	if len(is.Labels) > 0 {
		line(sDim.Render("labels"), strings.Join(is.Labels, "  "))
	}
	if is.CloseReason != "" {
		line(sDim.Render("reason"), is.CloseReason)
	}

	section := func(name, title, body string) {
		if strings.TrimSpace(body) == "" {
			return
		}
		mark := "▾"
		if d.collapsed[name] {
			mark = "▸"
		}
		hdr := " " + sSection.Render(mark+" "+title) + " "
		b.WriteString("\n" + hdr + rule(w-ansi.StringWidth(hdr)) + "\n")
		if !d.collapsed[name] {
			b.WriteString(body + "\n")
		}
	}
	section("description", "Description", d.markdown(is.Description, w-2))
	section("design", "Design", d.markdown(is.Design, w-2))
	section("acceptance", "Acceptance", d.markdown(is.AcceptanceCriteria, w-2))
	section("notes", "Notes", d.notes(is.Notes, w))
	section("comments", fmt.Sprintf("Comments (%d)", is.CommentCount), d.commentsBody(is, w))
	section("deps", "Dependencies", d.deps(a, is, w))
	section("children", "Children", d.children(a, is, w))
	section("refs", "Refs", refsBody(is))
	return b.String()
}

// notes are appended by `bd note`, one per line; newest first reads like a log.
func (d *detail) notes(n string, w int) string {
	if strings.TrimSpace(n) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(n), "\n")
	var b strings.Builder
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		wrapped := ansi.Wordwrap(lines[i], w-4, " ")
		for j, l := range strings.Split(wrapped, "\n") {
			p := "   "
			if j == 0 {
				p = sAccent.Render(" • ")
			}
			b.WriteString(p + l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *detail) commentsBody(is *bd.Issue, w int) string {
	if is.CommentCount == 0 {
		return ""
	}
	cs := d.comments[is.ID]
	if cs == nil {
		return sDim.Render("   loading…")
	}
	cs = append([]bd.Comment(nil), cs...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].CreatedAt.After(cs[j].CreatedAt) })
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(" " + sAccent.Render(c.Author) + sDim.Render(" "+age(c.CreatedAt)) + "\n")
		for _, l := range strings.Split(ansi.Wordwrap(c.Text, w-4, " "), "\n") {
			b.WriteString("   " + l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *detail) deps(a *App, is *bd.Issue, w int) string {
	s := a.snap
	var b strings.Builder
	item := func(kind, id string) {
		t := s.ByID[id]
		title := ""
		g := " "
		if t != nil {
			title, g = t.Title, glyph(s, t)
		}
		l := fmt.Sprintf(" %-16s %s %s %s", sDim.Render(kind), g, a.shortID(id), title)
		b.WriteString(ansi.Truncate(l, w, "…") + "\n")
	}
	for _, dep := range is.Dependencies {
		if dep.Type != "parent-child" {
			item("needs ("+dep.Type+")", dep.DependsOnID)
		}
	}
	for _, dep := range s.Dependents(is.ID) {
		item("needed by", dep.IssueID)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (d *detail) children(a *App, is *bd.Issue, w int) string {
	var b strings.Builder
	for _, c := range a.snap.Children[is.ID] {
		l := fmt.Sprintf(" %s %s %s %s", glyph(a.snap, c), a.shortID(c.ID), prio(c.Priority), c.Title)
		b.WriteString(ansi.Truncate(l, w, "…") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func refsBody(is *bd.Issue) string {
	var b strings.Builder
	for i, r := range is.Refs() {
		loc := filepath.Join(r.Repo, r.File)
		if r.Line > 0 {
			loc += ":" + strconv.Itoa(r.Line)
		}
		extra := r.Symbol
		fmt.Fprintf(&b, " %s %s %s\n", sKey.Render(strconv.Itoa(i+1)), loc, sDim.Render(extra))
	}
	return strings.TrimRight(b.String(), "\n")
}

// openRef opens a ref in $EDITOR at its line, suspending the TUI.
func openRef(r bd.Ref) tea.Cmd {
	path := filepath.Join(expandHome(r.Repo), r.File)
	args := []string{path}
	if r.Line > 0 {
		args = []string{"+" + strconv.Itoa(r.Line), path}
	}
	c := editorCmd(args...)
	c.Dir = filepath.Dir(path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return flashMsg{text: "editor: " + err.Error(), err: true}
		}
		return nil
	})
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// editorCmd runs $EDITOR, which may carry its own flags (e.g. "code --wait").
func editorCmd(args ...string) *exec.Cmd {
	ed := strings.Fields(os.Getenv("EDITOR"))
	if len(ed) == 0 {
		ed = []string{"vi"}
	}
	return exec.Command(ed[0], append(ed[1:], args...)...)
}
