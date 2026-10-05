package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

// row is a group header, a blank spacer between groups, or an issue line (issue != nil).
type row struct {
	spacer bool
	header string
	right  string
	issue  *bd.Issue
	indent int
	note   string // replaces the machine column when set (e.g. triage reason)
}

// issueList is the shared scrolling list used by every list-shaped view.
type issueList struct {
	rows   []row
	cursor int
	offset int
	height int
	noteW  int // overrides the adaptive note column width
}

func (l *issueList) SetRows(rows []row) {
	var keep string
	if is := l.Selected(); is != nil {
		keep = is.ID
	}
	old := l.cursor
	l.rows = rows
	l.cursor = -1
	best := -1
	for i, r := range rows {
		if r.issue != nil && r.issue.ID == keep && (best < 0 || abs(i-old) < abs(best-old)) {
			best = i
		}
	}
	if best >= 0 {
		l.cursor = best
		return
	}
	// the selected issue left this view: stay near where it was
	l.cursor = clamp(old, 0, max(len(rows)-1, 0))
	if len(rows) > 0 && rows[l.cursor].issue == nil {
		l.Move(1)
		if rows[l.cursor].issue == nil {
			l.Move(-1)
		}
	}
}

func (l *issueList) Selected() *bd.Issue {
	if l.cursor < 0 || l.cursor >= len(l.rows) {
		return nil
	}
	return l.rows[l.cursor].issue
}

// Move shifts the cursor by d issue rows, skipping headers.
func (l *issueList) Move(d int) {
	if len(l.rows) == 0 {
		return
	}
	step := 1
	if d < 0 {
		step = -1
	}
	n := d * step
	i := l.cursor
	if n == 0 && l.rows[clamp(i, 0, len(l.rows)-1)].issue == nil {
		n = 1
	}
	for ; n > 0; n-- {
		j := i + step
		for j >= 0 && j < len(l.rows) && l.rows[j].issue == nil {
			j += step
		}
		if j < 0 || j >= len(l.rows) {
			break
		}
		i = j
	}
	l.cursor = clamp(i, 0, len(l.rows)-1)
}

func (l *issueList) HandleKey(k string) bool {
	page := max(l.height-2, 1)
	switch k {
	case "j", "down":
		l.Move(1)
	case "k", "up":
		l.Move(-1)
	case "ctrl+d", "pgdown":
		l.Move(page / 2)
	case "ctrl+u", "pgup":
		l.Move(-page / 2)
	case "home":
		l.cursor = 0
		l.Move(0)
	case "G", "end":
		l.cursor = len(l.rows) - 1
		for l.cursor > 0 && l.rows[l.cursor].issue == nil {
			l.cursor--
		}
	case "]":
		l.jumpGroup(1)
	case "[":
		l.jumpGroup(-1)
	default:
		return false
	}
	return true
}

// isHeader marks a group header; an epic header also carries its issue and is selectable.
func (r row) isHeader() bool { return r.header != "" }

// jumpGroup moves to the first issue of the next or previous group.
func (l *issueList) jumpGroup(dir int) {
	cur := l.cursor
	for cur > 0 && !l.rows[cur].isHeader() {
		cur--
	}
	for i := cur + dir; i >= 0 && i < len(l.rows); i += dir {
		if l.rows[i].isHeader() {
			l.cursor = i
			l.Move(0)
			return
		}
	}
}

func (l *issueList) Render(a *App, w, h int) string {
	l.height = h
	if len(l.rows) == 0 {
		return ""
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+h {
		l.offset = l.cursor - h + 1
	}
	// keep the group header visible when the first issue of a group is selected
	if l.cursor == l.offset && l.offset > 0 && l.rows[l.offset-1].isHeader() {
		l.offset--
	}
	l.offset = clamp(l.offset, 0, max(len(l.rows)-h, 0))
	var b strings.Builder
	end := min(l.offset+h, len(l.rows))
	for i := l.offset; i < end; i++ {
		b.WriteString(l.renderRow(a, l.rows[i], i == l.cursor, w))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (l *issueList) renderRow(a *App, r row, sel bool, w int) string {
	if r.spacer {
		return ""
	}
	if r.isHeader() {
		right := r.right
		if right != "" {
			right = " " + right
		}
		marker := "  "
		if sel {
			marker = sAccent.Render("▸ ")
		} else if r.issue != nil && a.recentlyChanged(r.issue.ID) {
			marker = sAccent.Render("• ")
		}
		title := ansi.Truncate(r.header, max(w-lipgloss.Width(right)-6, 10), "…") + " "
		gap := max(w-2-lipgloss.Width(title)-lipgloss.Width(right), 1)
		return marker + title + rule(gap) + right
	}
	is := r.issue
	marker := "  "
	if sel {
		marker = sAccent.Render("▸ ")
	} else if a.recentlyChanged(is.ID) {
		marker = sAccent.Render("• ")
	}
	indent := strings.Repeat("  ", r.indent)
	id := a.shortID(is.ID)
	typ := ""
	if is.IssueType != "task" && is.IssueType != "" {
		typ = sDim.Render(is.IssueType) + " "
	}
	note := r.note
	if note == "" {
		note = a.machine(is)
	}
	ag := age(is.UpdatedAt)
	if is.Status == "in_progress" && time.Since(is.UpdatedAt) > 48*time.Hour {
		ag = sWarn.Render(ag)
	} else {
		ag = sDim.Render(ag)
	}
	noteW := 18
	if w < 110 {
		noteW = 10
	}
	if l.noteW > 0 {
		noteW = l.noteW
	}
	right := fmt.Sprintf(" %s %s %s",
		sDim.Render(padRight(ansi.Truncate(note, noteW, "…"), noteW)),
		padLeft(ag, 4),
		padLeft(sDim.Render(minutes(is.EstimatedMinutes)), 5))
	if w < 75 {
		right = " " + padLeft(ag, 4)
	}
	left := fmt.Sprintf("%s%s%s %s %s ", marker, indent, glyph(a.snap, is), padRight(sDim.Render(id), a.idWidth), prio(is.Priority))
	titleW := max(w-lipgloss.Width(left)-lipgloss.Width(right), 8)
	title := is.Title
	if sel {
		title = sSel.Render(title)
	}
	title = padRight(ansi.Truncate(typ+title, titleW, "…"), titleW)
	return left + title + right
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func padLeft(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }
