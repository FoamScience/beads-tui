package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	gansi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
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
	pg        pager
	id        string
	key       string
	comments  map[string][]bd.Comment
	collapsed map[string]bool
	md        *glamour.TermRenderer
	mdWidth   int
	inl       map[int]*glamour.TermRenderer // marginless renderers for inline text, by width
	dark      bool

	// links are the issues listed in the pane (children, deps), in screen order; tab walks them.
	cand      []string // issue ids in build order, addressed by the markers left in the text
	links     []string
	linkLines []int
	linkSel   int // index into links, -1 for none
}

func newDetail() *detail {
	return &detail{comments: map[string][]bd.Comment{}, collapsed: map[string]bool{}, dark: true, linkSel: -1}
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
		r, err := glamour.NewTermRenderer(glamour.WithStyles(markdownStyle(d.dark)), glamour.WithWordWrap(w))
		if err != nil {
			return s
		}
		d.md, d.mdWidth = r, w
	}
	out, err := d.md.Render(escapeTags(s))
	if err != nil {
		return s
	}
	return strings.Trim(out, "\n")
}

func (d *detail) render(a *App, is *bd.Issue, w, h int) string {
	if is == nil {
		return sDim.Render("\n  nothing selected")
	}
	d.pg.SetSize(w, h)
	cs, fetched := d.comments[is.ID]
	if is.ID != d.id {
		d.linkSel = -1
	}
	key := fmt.Sprintf("%s|%s|%s|%d|%s|%d|%d|%d|%v|%v|%v|%v|%d", is.ID, is.UpdatedAt, is.Status, is.Priority, is.ExternalRef, is.EstimatedMinutes,
		w, len(cs), fetched && cs != nil, a.snap.Loaded, d.collapsed, d.dark, d.linkSel)
	if key != d.key {
		reset := is.ID != d.id
		d.id, d.key = is.ID, key
		d.pg.SetContent(d.resolveLinks(d.content(a, is, w)), reset)
	}
	return d.pg.View()
}

var detailSections = []string{"description", "design", "acceptance", "notes", "comments", "deps", "children", "refs", "sessions"}

func (d *detail) content(a *App, is *bd.Issue, w int) string {
	d.cand = d.cand[:0]
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
		line(sDim.Render("labels"), labelChips(is.Labels))
	}
	if !is.Closed() {
		if chain := s.BlockChain(is); len(chain) > 0 {
			parts := make([]string, len(chain))
			for i, c := range chain {
				parts[i] = glyph(s, c) + " " + a.shortID(c.ID)
			}
			line(sErr.Render("waits on"), strings.Join(parts, sDim.Render(" → ")))
		}
	}
	if t := timeVsEstimate(s, is); t != "" {
		line(sDim.Render("time"), t)
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
	section("sessions", "Agent sessions", sessionsBody(a, is))
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
		l := fmt.Sprintf("%s%-16s %s %s %s", d.mark(id), sDim.Render(kind), g, a.shortID(id), title)
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
		l := fmt.Sprintf("%s%s %s %s %s", d.mark(c.ID), glyph(a.snap, c), a.shortID(c.ID), prio(c.Priority), c.Title)
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

// timeVsEstimate compares elapsed time (started to closed) with the estimate; for a parent it rolls up
// the closed leaf descendants that have both, so nested estimates are not counted twice.
// Elapsed is wall-clock time, not effort.
func timeVsEstimate(s *bd.Snapshot, is *bd.Issue) string {
	if is.StartedAt != nil && is.ClosedAt != nil && is.EstimatedMinutes > 0 {
		el := int(is.ClosedAt.Sub(*is.StartedAt).Minutes())
		return fmt.Sprintf("elapsed %s vs est %s (%.1f×)", minutes(el), minutes(is.EstimatedMinutes), float64(el)/float64(is.EstimatedMinutes))
	}
	if len(s.Children[is.ID]) == 0 {
		return ""
	}
	var el, est, n int
	var walk func(string)
	walk = func(id string) {
		for _, c := range s.Children[id] {
			if len(s.Children[c.ID]) == 0 && c.StartedAt != nil && c.ClosedAt != nil && c.EstimatedMinutes > 0 {
				el += int(c.ClosedAt.Sub(*c.StartedAt).Minutes())
				est += c.EstimatedMinutes
				n++
			}
			walk(c.ID)
		}
	}
	walk(is.ID)
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d closed with estimates: elapsed %s vs est %s (%.1f×)", n, minutes(el), minutes(est), float64(el)/float64(est))
}

func sessionsBody(a *App, is *bd.Issue) string {
	var b strings.Builder
	for _, ss := range sessions(is) {
		where := sDim.Render("claimed it, no longer running (was pane " + ss.pane + ")")
		if info := a.sessionNames[ss.id]; info.Name != "" || !info.Last.IsZero() {
			where = fmt.Sprintf("%q %s", info.Name, where)
			if !info.Last.IsZero() {
				where += sDim.Render(", last active " + info.Last.Format("2006-01-02 15:04"))
			}
		}
		if p, ok := a.herdr[ss.id]; ok {
			where = p.Status + "  " + p.WSLabel + " / " + p.Title + sDim.Render("  pane "+p.Pane)
		}
		fmt.Fprintf(&b, " %s  %s\n", ss.id[:min(8, len(ss.id))], where)
	}
	if b.Len() > 0 {
		b.WriteString(sDim.Render(" R resumes it, or focuses the pane if it is still open"))
	}
	return b.String()
}

// Link placeholders are private-use runes, one cell wide like the cursor that replaces them,
// so truncation measures link rows the same as any other row.
const linkBase = 0xF0000

var linkMarker = regexp.MustCompile("[\U000F0000-\U000FFFFD]")

// mark leaves a one-cell placeholder that resolveLinks turns into the link cursor or a space.
func (d *detail) mark(id string) string {
	d.cand = append(d.cand, id)
	return string(rune(linkBase + len(d.cand) - 1))
}

// resolveLinks records which links made it on screen (collapsed sections drop theirs) and draws the cursor.
func (d *detail) resolveLinks(content string) string {
	d.links, d.linkLines = d.links[:0], d.linkLines[:0]
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = linkMarker.ReplaceAllStringFunc(l, func(m string) string {
			n := int([]rune(m)[0]) - linkBase
			d.links = append(d.links, d.cand[n])
			d.linkLines = append(d.linkLines, i)
			if len(d.links)-1 == d.linkSel {
				return sAccent.Render("▸")
			}
			return " "
		})
	}
	if d.linkSel >= len(d.links) {
		d.linkSel = -1
	}
	return strings.Join(lines, "\n")
}

// moveLink steps the link cursor and scrolls it into view.
func (d *detail) moveLink(step int) {
	if len(d.links) == 0 {
		return
	}
	switch {
	case d.linkSel < 0 && step < 0:
		d.linkSel = len(d.links) - 1
	case d.linkSel < 0:
		d.linkSel = 0
	default:
		d.linkSel = (d.linkSel + step + len(d.links)) % len(d.links)
	}
	d.pg.GotoLine(d.linkLines[d.linkSel])
}

// selectedLink is the link picked with tab, else the one on the cursor's line.
func (d *detail) selectedLink() string {
	if d.linkSel >= 0 && d.linkSel < len(d.links) {
		return d.links[d.linkSel]
	}
	for i, l := range d.linkLines {
		if l == d.pg.row {
			return d.links[i]
		}
	}
	return ""
}

// escapeTags backslash-escapes "<" where markdown would start an HTML tag, and "_" where it would
// start emphasis, so placeholders such as runs/<sim>_<attempt>/ survive rendering. Underscores
// inside words were never emphasis; inline code and fenced blocks are left alone.
func escapeTags(s string) string {
	var b strings.Builder
	fence := ""
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		t := strings.TrimSpace(line)
		if fence == "" && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			fence = t[:3]
		} else if fence != "" && strings.HasPrefix(t, fence) {
			fence = ""
			b.WriteString(line)
			continue
		}
		if fence != "" {
			b.WriteString(line)
			continue
		}
		inCode := false
		for j := 0; j < len(line); j++ {
			c := line[j]
			switch {
			case c == '`':
				inCode = !inCode
			case c == '<' && !inCode && j+1 < len(line) && tagStart(line[j+1]):
				b.WriteByte('\\')
			case c == '_' && !inCode && !(j > 0 && alnum(line[j-1]) && j+1 < len(line) && alnum(line[j+1])):
				// an underscore next to punctuation would open _emphasis_ and vanish (runs/<sim>_<attempt>)
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

func tagStart(c byte) bool {
	return c == '/' || c == '!' || c == '?' || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// markdownStyle is glamour's standard style without the literal "##" heading prefixes;
// headings keep their bold colour.
func markdownStyle(dark bool) gansi.StyleConfig {
	st := styles.LightStyleConfig
	if dark {
		st = styles.DarkStyleConfig
	}
	for _, h := range []*gansi.StyleBlock{&st.H2, &st.H3, &st.H4, &st.H5, &st.H6} {
		h.Prefix = ""
	}
	return st
}

func alnum(c byte) bool { return c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'z' }

// inline renders a short markdown fragment (a log entry, a table value) wrapped to w, without the
// document margin and blank lines a full render adds, so it can sit next to a label or chip.
func (d *detail) inline(s string, w int) []string {
	if d.inl == nil {
		d.inl = map[int]*glamour.TermRenderer{}
	}
	r := d.inl[w]
	if r == nil {
		st := markdownStyle(d.dark)
		zero := uint(0)
		st.Document.Margin = &zero
		st.Document.BlockPrefix, st.Document.BlockSuffix = "", ""
		var err error
		if r, err = glamour.NewTermRenderer(glamour.WithStyles(st), glamour.WithWordWrap(w)); err != nil {
			return strings.Split(ansi.Wordwrap(s, w, " "), "\n")
		}
		d.inl[w] = r
	}
	out, err := r.Render(escapeTags(s))
	if err != nil {
		return strings.Split(ansi.Wordwrap(s, w, " "), "\n")
	}
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}
