package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

// liveView lists the live ledgers (pinned + "live") and renders the selected one as a ledger:
// summary, a current-state grid, a newest-first log and the design field as its runbook.
type liveView struct {
	ledgers []*bd.Issue
	cursor  int
	vp      viewport.Model
	key     string // what the pane was last rendered for
}

type ledgerEditMsg struct {
	id, before, path string
	err              error
}

func newLive() *liveView { return &liveView{vp: viewport.New()} }

func (v *liveView) Name() string { return "Live" }

func (v *liveView) Rebuild(a *App) {
	keep := ""
	if is := v.Selected(); is != nil {
		keep = is.ID
	}
	v.ledgers = v.ledgers[:0]
	for _, is := range a.snap.Issues {
		if is.IsLedger() && a.machineMatch(is) {
			v.ledgers = append(v.ledgers, is)
		}
	}
	sort.SliceStable(v.ledgers, func(i, j int) bool {
		return bd.ParseLedger(v.ledgers[i].Description).Last().After(bd.ParseLedger(v.ledgers[j].Description).Last())
	})
	v.cursor = clamp(v.cursor, 0, max(len(v.ledgers)-1, 0))
	for i, is := range v.ledgers {
		if is.ID == keep {
			v.cursor = i
		}
	}
}

func (v *liveView) Selected() *bd.Issue {
	if len(v.ledgers) == 0 {
		return nil
	}
	return v.ledgers[v.cursor]
}

func (v *liveView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	is := v.Selected()
	switch k.String() {
	case "j", "down":
		v.cursor = min(v.cursor+1, max(len(v.ledgers)-1, 0))
	case "k", "up":
		v.cursor = max(v.cursor-1, 0)
	case "J":
		v.vp.ScrollDown(1)
	case "K":
		v.vp.ScrollUp(1)
	case "ctrl+d", "pgdown":
		v.vp.HalfPageDown()
	case "ctrl+u", "pgup":
		v.vp.HalfPageUp()
	case "+":
		if is == nil {
			return true, nil
		}
		id := is.ID
		a.modal = newPrompt("Log entry for "+a.shortID(id)+" (dated today)", "", func(t string) tea.Cmd {
			if t == "" {
				return nil
			}
			day := time.Now()
			a.mut(id, func(i *bd.Issue) { i.Description = bd.AppendLog(i.Description, day, t) })()
			a.rebuild()
			a.pending++
			c, desc := a.client, "logged on "+a.shortID(id)
			// agents rewrite ledgers while work runs: append to the description bd holds now,
			// not the copy this screen loaded, so a concurrent update is never overwritten
			return func() tea.Msg {
				cur, err := c.Show(id)
				if err != nil {
					return writeDoneMsg{desc, err}
				}
				return writeDoneMsg{desc, c.ExecInput(bd.AppendLog(cur.Description, day, t), "update", id, "--body-file", "-")}
			}
		})
	case "E":
		if is == nil {
			return true, nil
		}
		return true, editLedger(is)
	default:
		return false, nil
	}
	return true, nil
}

// editLedger opens the description in $EDITOR and writes it back when it changed.
func editLedger(is *bd.Issue) tea.Cmd {
	f, err := os.CreateTemp("", "bt-ledger-*.md")
	if err != nil {
		return flashErr(err)
	}
	_, err = f.WriteString(is.Description)
	f.Close()
	if err != nil {
		return flashErr(err)
	}
	id, before, path := is.ID, is.Description, f.Name()
	return tea.ExecProcess(editorCmd(path), func(err error) tea.Msg { return ledgerEditMsg{id, before, path, err} })
}

func (v *liveView) Msg(a *App, msg tea.Msg) tea.Cmd {
	m, ok := msg.(ledgerEditMsg)
	if !ok {
		return nil
	}
	if m.err != nil {
		os.Remove(m.path)
		return flashErr(m.err)
	}
	b, err := os.ReadFile(m.path)
	if err != nil {
		return flashErr(err)
	}
	after := string(b)
	if after == m.before {
		os.Remove(m.path)
		return flash("ledger unchanged")
	}
	warn := ""
	if p := bd.ParseLedger(after).Problems; len(p) > 0 {
		warn = " (not in ledger format: " + strings.Join(p, "; ") + ")"
	}
	a.pending++
	c, id, path, desc := a.client, m.id, m.path, "ledger "+a.shortID(m.id)+" saved"
	return func() tea.Msg {
		cur, err := c.Show(id)
		if err != nil {
			return writeDoneMsg{desc, err}
		}
		if cur.Description != m.before {
			return writeDoneMsg{desc, fmt.Errorf("%s changed while you edited it; not saved, your version is in %s", id, path)}
		}
		if err := c.ExecInput(after, "update", id, "--body-file", "-"); err != nil {
			return writeDoneMsg{desc, fmt.Errorf("%w; your version is in %s", err, path)}
		}
		os.Remove(path)
		return writeDoneMsg{desc + warn, nil}
	}
}

func (v *liveView) Hints() []string {
	return []string{"+ log entry", "E edit", "J/K scroll", "M machine"}
}

func (v *liveView) Render(a *App, w, h int) string {
	bar := ansi.Truncate(fmt.Sprintf(" %s %s   %s", sDim.Render("machine"), machineHint(a), sDim.Render(fmt.Sprintf("%d live ledgers", len(v.ledgers)))), w, "…")
	h -= 2
	if len(v.ledgers) == 0 {
		return bar + "\n\n" + sDim.Render("  no live ledgers (pinned beads labelled live) for this filter")
	}
	lw := min(max(w/4, 26), 38)
	left := v.renderList(a, lw, h)
	rw := w - lw - 3
	v.vp.SetWidth(rw)
	v.vp.SetHeight(h)
	is := v.Selected()
	if key := fmt.Sprintf("%s|%s|%d|%v", is.ID, is.UpdatedAt, rw, a.snap.Loaded); key != v.key {
		if !strings.HasPrefix(v.key, is.ID+"|") {
			v.vp.GotoTop()
		}
		v.key = key
		v.vp.SetContent(renderLedger(a, is, rw))
	}
	sep := strings.TrimRight(strings.Repeat(sRule.Render("│")+"\n", h), "\n")
	return bar + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(lw).Height(h).MaxHeight(h).Render(left), " ", sep, " ", v.vp.View())
}

func (v *liveView) renderList(a *App, w, h int) string {
	var lines []string
	for i, is := range v.ledgers {
		marker := "  "
		title := is.Title
		if i == v.cursor {
			marker, title = sAccent.Render("▸ "), sSel.Render(title)
		}
		lines = append(lines, ansi.Truncate(marker+title, w, "…"))
		meta := "   " + a.shortID(is.ID)
		if e := a.snap.Epic(is); e != nil {
			meta += " · " + a.shortID(e.ID)
		}
		if last := bd.ParseLedger(is.Description).Last(); !last.IsZero() {
			meta += " · " + dayAge(last)
		}
		lines = append(lines, sDim.Render(ansi.Truncate(meta, w, "…")), "")
	}
	return strings.Join(lines[:min(len(lines), h)], "\n")
}

// dayAge reads a log date as days ago, since entries carry no time of day.
func dayAge(d time.Time) string {
	y, m, dd := time.Now().Date()
	today := time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
	switch n := int(today.Sub(d).Hours() / 24); {
	case n <= 0:
		return "today"
	case n == 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%dd ago", n)
	}
}

func renderLedger(a *App, is *bd.Issue, w int) string {
	l := bd.ParseLedger(is.Description)
	var b strings.Builder
	b.WriteString(sBold.Render(ansi.Wordwrap(is.Title, w, " ")) + "\n")
	meta := []string{sDim.Render(a.shortID(is.ID))}
	if e := a.snap.Epic(is); e != nil {
		meta = append(meta, sDim.Render("epic ")+a.shortID(e.ID)+" "+ansi.Truncate(e.Title, 40, "…"))
	}
	if last := l.Last(); !last.IsZero() {
		meta = append(meta, sDim.Render("last entry ")+dayAge(last))
	}
	b.WriteString(strings.Join(meta, sDim.Render("  ·  ")) + "\n")
	for _, p := range l.Problems {
		b.WriteString(sWarn.Render("⚠ "+p) + "\n")
	}
	if l.Summary != "" {
		b.WriteString("\n" + ansi.Wordwrap(l.Summary, w, " ") + "\n")
	}

	section := func(title string) {
		hdr := sSection.Render(title) + " "
		b.WriteString("\n" + hdr + rule(w-ansi.StringWidth(hdr)) + "\n")
	}
	if len(l.State) > 0 {
		section("Current state")
		kw := 0
		for _, r := range l.State {
			kw = max(kw, ansi.StringWidth(r.Key))
		}
		kw = min(kw, w/3)
		vw := max(w-kw-3, 10)
		for _, r := range l.State {
			keyLines := strings.Split(ansi.Wordwrap(r.Key, kw, " "), "\n")
			valLines := a.detail.inline(r.Value, vw)
			for i := range max(len(keyLines), len(valLines)) {
				k, val := "", ""
				if i < len(keyLines) {
					k = keyLines[i]
				}
				if i < len(valLines) {
					val = valLines[i]
				}
				b.WriteString(" " + padRight(sDim.Render(k), kw) + "  " + val + "\n")
			}
		}
	}
	if len(l.Log) > 0 {
		section(fmt.Sprintf("Log (%d)", len(l.Log)))
		prev := ""
		for i := len(l.Log) - 1; i >= 0; i-- {
			e := l.Log[i]
			d := e.Date.Format("2006-01-02")
			chip := strings.Repeat(" ", 12)
			if d != prev {
				st := sLabel
				if dayAge(e.Date) == "today" {
					st = sTabOn
				}
				chip = st.Render(d)
			}
			prev = d
			for j, l := range a.detail.inline(e.Text, max(w-14, 10)) {
				if j > 0 {
					chip = strings.Repeat(" ", 12)
				}
				b.WriteString(" " + chip + " " + l + "\n")
			}
		}
	}
	if strings.TrimSpace(is.Design) != "" {
		section("Runbook")
		b.WriteString(a.detail.markdown(is.Design, w-2) + "\n")
	}
	return b.String()
}
