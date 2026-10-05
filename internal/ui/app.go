// Package ui is the Bubble Tea front end of bt.
package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/elwardi/beads-tui/internal/bd"
)

// View is one tab of the app; list-shaped views embed issueList.
type View interface {
	Name() string
	Rebuild(a *App)
	Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd)
	Render(a *App, w, h int) string
	Selected() *bd.Issue
	Hints() []string
}

type (
	snapshotMsg struct {
		issues []bd.Issue
		err    error
		gen    int
		stamp  string
	}
	tickMsg      time.Time
	writeDoneMsg struct {
		desc string
		err  error
	}
	flashMsg struct {
		text string
		err  bool
	}
)

const (
	splitMinWidth = 140
	minW, minH    = 60, 15
	changedFor    = 90 * time.Second
	fullReload    = 2 * time.Minute
)

type App struct {
	client  bd.Client
	snap    *bd.Snapshot
	prefix  string
	host    string
	user    string
	idWidth int

	views  []View
	active int
	w, h   int

	detail      *detail
	detailOpen  bool      // full-screen detail in narrow terminals
	focusDetail bool      // keys go to the detail pane
	override    *bd.Issue // detail target picked from search, outside the current view

	modal   *modal
	help    bool
	state   *state
	changed map[string]time.Time

	stamp    string
	gen      int // id of the newest load; older results are dropped
	retryAt  time.Time
	loading  bool
	pending  int
	lastLoad time.Time
	loadErr  error
	flash    string
	flashErr bool
	flashAt  time.Time
	lastSync time.Time
	cached   bool // showing the on-disk snapshot until the first bd load lands
}

func New(client bd.Client) *App {
	host, _ := os.Hostname()
	a := &App{
		client:  client,
		snap:    bd.NewSnapshot(nil),
		host:    host,
		user:    os.Getenv("USER"),
		detail:  newDetail(),
		changed: map[string]time.Time{},
		state:   loadState(),
		idWidth: 8,
		loading: true,
	}
	a.views = []View{newNow(), newReady(), newEpics(), newTriage(), newActivity(), newGraph(), newMolecules()}
	if cached := loadCache(client.Dir); len(cached) > 0 {
		a.setSnapshot(bd.NewSnapshot(cached))
		a.cached = true
	}
	return a
}

func (a *App) Init() tea.Cmd {
	return tea.Batch(a.load(), tick(), tea.RequestBackgroundColor)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (a *App) load() tea.Cmd {
	a.loading = true
	a.gen++
	gen, c := a.gen, a.client
	return func() tea.Msg {
		stamp := c.ChangeStamp()
		is, err := c.List()
		if err == nil {
			saveCache(c.Dir, is)
		}
		return snapshotMsg{is, err, gen, stamp}
	}
}

func flash(s string) tea.Cmd { return func() tea.Msg { return flashMsg{text: s} } }

func flashErr(err error) tea.Cmd {
	return func() tea.Msg { return flashMsg{text: err.Error(), err: true} }
}

// write runs a bd mutation; mutate applies the expected change locally so the UI does not wait for the reload.
func (a *App) write(desc string, mutate func(), args ...string) tea.Cmd {
	if mutate != nil {
		mutate()
		a.rebuild()
	}
	a.pending++
	a.flash, a.flashErr, a.flashAt = "⟳ "+desc, false, time.Now()
	c := a.client
	return func() tea.Msg { return writeDoneMsg{desc, c.Exec(args...)} }
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		return a, nil
	case tea.BackgroundColorMsg:
		a.detail.dark = msg.IsDark()
		a.detail.md = nil
		return a, nil
	case snapshotMsg:
		if msg.gen != a.gen {
			return a, nil
		}
		a.loading = false
		a.loadErr = msg.err
		if msg.err != nil {
			a.retryAt = time.Now().Add(5 * time.Second)
			return a, nil
		}
		// a snapshot read before an in-flight write would undo its optimistic change; the post-write load follows
		if a.pending == 0 {
			a.stamp, a.lastLoad, a.cached = msg.stamp, time.Now(), false
			a.setSnapshot(bd.NewSnapshot(msg.issues))
		}
		return a, nil
	case tickMsg:
		cmds := []tea.Cmd{tick()}
		if !a.loading && a.pending == 0 {
			retry := a.loadErr != nil && time.Now().After(a.retryAt)
			changed := a.loadErr == nil && a.client.ChangeStamp() != a.stamp
			if retry || changed || time.Since(a.lastLoad) > fullReload {
				cmds = append(cmds, a.load())
			}
		}
		return a, tea.Batch(cmds...)
	case writeDoneMsg:
		a.pending--
		if msg.err != nil {
			a.flash, a.flashErr = msg.err.Error(), true
		} else {
			a.flash, a.flashErr = "✓ "+msg.desc, false
			if msg.desc == "sync" {
				a.lastSync = time.Now()
			}
		}
		a.flashAt = time.Now()
		if a.pending == 0 {
			return a, a.load()
		}
		return a, nil
	case wlKeysMsg:
		// the picker may have been closed or replaced while wl ran
		if a.modal == msg.m {
			msg.m.options = append(msg.m.options, msg.opts...)
			msg.m.filter()
		}
		return a, nil
	case flashMsg:
		a.flash, a.flashErr, a.flashAt = msg.text, msg.err, time.Now()
		return a, nil
	case commentsMsg:
		if msg.err != nil {
			delete(a.detail.comments, msg.id)
			return a, flashErr(msg.err)
		}
		if msg.comments == nil {
			msg.comments = []bd.Comment{}
		}
		a.detail.comments[msg.id] = msg.comments
		return a, nil
	case tea.KeyPressMsg:
		return a, a.key(msg)
	case tea.PasteMsg:
		if a.modal != nil {
			return a, a.modal.forward(msg)
		}
		return a, nil
	}
	if a.modal != nil {
		if cmd := a.modal.forward(msg); cmd != nil {
			return a, cmd
		}
	}
	for _, v := range a.views {
		if h, ok := v.(interface {
			Msg(*App, tea.Msg) tea.Cmd
		}); ok {
			if cmd := h.Msg(a, msg); cmd != nil {
				return a, cmd
			}
		}
	}
	return a, nil
}

func (a *App) setSnapshot(s *bd.Snapshot) {
	now := time.Now()
	if len(a.snap.Issues) > 0 {
		for _, is := range s.Issues {
			if old := a.snap.ByID[is.ID]; old == nil || !old.UpdatedAt.Equal(is.UpdatedAt) {
				a.changed[is.ID] = now
			}
		}
	}
	for id, t := range a.changed {
		if now.Sub(t) > changedFor {
			delete(a.changed, id)
		}
	}
	a.snap = s
	if len(s.Issues) > 0 {
		a.prefix = strings.SplitN(s.Issues[0].ID, "-", 2)[0] + "-"
	}
	a.idWidth = 6
	for _, is := range s.Issues {
		if !is.Closed() {
			a.idWidth = max(a.idWidth, min(len(a.shortID(is.ID)), 14))
		}
	}
	if a.override != nil {
		a.override = s.ByID[a.override.ID]
	}
	a.rebuild()
}

func (a *App) rebuild() {
	for _, v := range a.views {
		v.Rebuild(a)
	}
}

func (a *App) recentlyChanged(id string) bool {
	t, ok := a.changed[id]
	return ok && time.Since(t) < changedFor
}

func (a *App) shortID(id string) string { return strings.TrimPrefix(id, a.prefix) }

func (a *App) machine(is *bd.Issue) string {
	m := is.LabelWithPrefix("machine:")
	if a.user != "" {
		m = strings.TrimPrefix(m, a.user+"-")
	}
	return m
}

func (a *App) selected() *bd.Issue {
	if a.override != nil {
		return a.override
	}
	return a.views[a.active].Selected()
}

// split shows the detail pane beside list views; Graph and Molecules use the full width themselves.
func (a *App) split() bool {
	switch a.views[a.active].(type) {
	case *graphView, *moleculesView:
		return false
	}
	return a.w >= splitMinWidth
}

func (a *App) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+c" {
		return tea.Quit
	}
	if a.modal != nil {
		m := a.modal
		done, cmd := m.update(msg)
		if done && a.modal == m {
			a.modal = nil
		}
		return cmd
	}
	if a.help {
		a.help = false
		return nil
	}
	if a.focusDetail || a.detailOpen || a.override != nil {
		switch k {
		case "esc", "q", "h", "left":
			a.focusDetail, a.detailOpen, a.override = false, false, nil
			return nil
		case "j", "down", "k", "up", "ctrl+d", "ctrl+u", "pgdown", "pgup", "space":
			a.scrollDetail(k)
			return nil
		case "home":
			a.detail.vp.GotoTop()
			return nil
		case "end":
			a.detail.vp.GotoBottom()
			return nil
		case "Z":
			a.toggleCollapse()
			return nil
		}
		if cmd, ok := a.action(k); ok {
			return cmd
		}
		return nil
	}
	v := a.views[a.active]
	if ok, cmd := v.Update(a, msg); ok {
		return tea.Batch(cmd, a.detail.loadComments(a, a.selected()))
	}
	switch k {
	case "q":
		return tea.Quit
	case "?":
		a.help = true
		return nil
	case "1", "2", "3", "4", "5", "6", "7":
		return a.switchTo(int(k[0] - '1'))
	case "tab":
		return a.switchTo(a.active + 1)
	case "shift+tab":
		return a.switchTo(a.active + len(a.views) - 1)
	case "enter", "l", "right":
		if a.selected() == nil {
			return nil
		}
		if a.split() {
			a.focusDetail = true
		} else {
			a.detailOpen = true
		}
		return a.detail.loadComments(a, a.selected())
	case "g":
		if is := a.selected(); is != nil {
			for i, v := range a.views {
				if g, ok := v.(*graphView); ok {
					g.setRoot(a, is)
					return a.switchTo(i)
				}
			}
		}
		return nil
	case "r":
		return a.load()
	case "/":
		a.modal = a.searchModal()
		return nil
	}
	if cmd, ok := a.action(k); ok {
		return cmd
	}
	return nil
}

func (a *App) switchTo(i int) tea.Cmd {
	a.active = i % len(a.views)
	cmd := a.detail.loadComments(a, a.selected())
	if v, ok := a.views[a.active].(interface{ Activate(*App) tea.Cmd }); ok {
		cmd = tea.Batch(cmd, v.Activate(a))
	}
	return cmd
}

func (a *App) scrollDetail(k string) {
	vp := &a.detail.vp
	switch k {
	case "j", "down":
		vp.ScrollDown(1)
	case "k", "up":
		vp.ScrollUp(1)
	case "ctrl+d", "pgdown", "space":
		vp.HalfPageDown()
	case "ctrl+u", "pgup":
		vp.HalfPageUp()
	}
}

func (a *App) toggleCollapse() {
	all := !a.detail.collapsed["description"]
	for _, s := range []string{"description", "design", "acceptance"} {
		a.detail.collapsed[s] = all
	}
}

func (a *App) searchModal() *modal {
	opts := make([]option, 0, len(a.snap.Issues))
	for _, is := range a.snap.Issues {
		opts = append(opts, option{value: is.ID, label: fmt.Sprintf("%s %s  %s  %s", statusGlyph[is.Status], a.shortID(is.ID), is.Title, strings.Join(is.Labels, " "))})
	}
	m := newPicker("Search all issues", opts, func(id string) tea.Cmd {
		a.override = a.snap.ByID[id]
		if a.override == nil {
			return nil
		}
		a.focusDetail = true
		return a.detail.loadComments(a, a.override)
	})
	m.input.Placeholder = "id, title or label"
	return m
}

func (a *App) View() tea.View {
	v := tea.NewView(a.render())
	v.AltScreen = true
	v.WindowTitle = "bt"
	return v
}

func (a *App) render() string {
	if a.w == 0 {
		return ""
	}
	if a.w < minW || a.h < minH {
		return lipgloss.Place(a.w, a.h, lipgloss.Center, lipgloss.Center,
			sWarn.Render(fmt.Sprintf("bt needs at least %dx%d (now %dx%d)", minW, minH, a.w, a.h)))
	}
	bodyH := a.h - 4
	var body string
	v := a.views[a.active]
	switch {
	case a.help:
		body = a.helpView(bodyH)
	case (a.detailOpen || a.override != nil) && !a.split():
		body = a.detail.render(a, a.selected(), a.w, bodyH)
	case a.split():
		lw := a.w * 11 / 20
		left := v.Render(a, lw, bodyH)
		sep := strings.TrimRight(strings.Repeat(sRule.Render("│")+"\n", bodyH), "\n")
		right := a.detail.render(a, a.selected(), a.w-lw-2, bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(lw).Height(bodyH).MaxHeight(bodyH).Render(left), sep, " ",
			lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(right))
	default:
		body = v.Render(a, a.w, bodyH)
	}
	body = lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).MaxWidth(a.w).Render(body)
	if a.modal != nil {
		body = lipgloss.NewStyle().MaxHeight(bodyH).MaxWidth(a.w).Render(
			lipgloss.Place(a.w, bodyH, lipgloss.Center, lipgloss.Center, a.modal.view(a.w, bodyH)))
	}
	return a.header() + "\n" + rule(a.w) + "\n" + body + "\n" + rule(a.w) + "\n" + a.footer()
}

func (a *App) header() string {
	left := " " + sBold.Foreground(cAccent).Render("bt") + "  " + sDim.Render(strings.Replace(a.client.Dir, os.Getenv("HOME"), "~", 1))
	var tabs []string
	for i, v := range a.views {
		label := fmt.Sprintf("%d %s", i+1, v.Name())
		if i == a.active {
			tabs = append(tabs, sTabOn.Render(label))
		} else {
			tabs = append(tabs, sTabOff.Render(label))
		}
	}
	tabStr := strings.Join(tabs, "  ")
	var st string
	switch {
	case a.loadErr != nil:
		st = sErr.Render("● load failed")
	case a.loading && a.cached:
		st = sWarn.Render("● cached, refreshing")
	case a.loading:
		st = sWarn.Render("● refreshing")
	default:
		st = sOK.Render("●") + sDim.Render(" "+age(a.lastLoad))
	}
	if !a.lastSync.IsZero() {
		st += sDim.Render("  ⇅ " + age(a.lastSync))
	}
	right := st + " "
	if a.w < 110 {
		left = " " + sBold.Foreground(cAccent).Render("bt")
	}
	gap := a.w - lipgloss.Width(left) - lipgloss.Width(tabStr) - lipgloss.Width(right)
	if gap < 2 {
		tabStr = sTabOn.Render(fmt.Sprintf("%d %s", a.active+1, a.views[a.active].Name()))
		gap = max(a.w-lipgloss.Width(left)-lipgloss.Width(tabStr)-lipgloss.Width(right), 1)
	}
	l := gap / 2
	return left + strings.Repeat(" ", l) + tabStr + strings.Repeat(" ", gap-l) + right
}

func (a *App) footer() string {
	if a.flash != "" && time.Since(a.flashAt) < 6*time.Second {
		if a.flashErr {
			return " " + sErr.Render(ansi.Truncate(a.flash, a.w-2, "…"))
		}
		return " " + sAccent.Render(ansi.Truncate(a.flash, a.w-2, "…"))
	}
	if a.loadErr != nil {
		return " " + sErr.Render(ansi.Truncate(a.loadErr.Error(), a.w-2, "…"))
	}
	var hints []string
	if a.modal != nil {
		return ""
	}
	if a.focusDetail || a.detailOpen || a.override != nil {
		hints = []string{"j/k scroll", "Z fold", "o open ref", "s status", "n note", "c close", "esc back"}
	} else {
		hints = append([]string{"enter detail"}, a.views[a.active].Hints()...)
		if _, own := a.views[a.active].(*moleculesView); !own {
			hints = append(hints, "s status", "n note", "c close")
		}
		hints = append(hints, "/ search", "? keys")
	}
	return " " + ansi.Truncate(renderHints(hints), a.w-2, "…")
}

func renderHints(hs []string) string {
	out := make([]string, len(hs))
	for i, h := range hs {
		k, rest, _ := strings.Cut(h, " ")
		out[i] = sKey.Render(k) + " " + sDim.Render(rest)
	}
	return strings.Join(out, "  ")
}
