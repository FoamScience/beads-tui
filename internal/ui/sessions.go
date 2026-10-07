package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

// session is a Claude session the herdr-bd-sessions hook tied to a bead when the agent claimed it.
type session struct {
	id   string // Claude session uuid
	pane string // herdr pane id, or "none"
}

// sessions decodes metadata keys of the form claude_session.<uuid with _ for ->=<pane>.
func sessions(is *bd.Issue) []session {
	var m map[string]any
	if len(is.Metadata) == 0 || json.Unmarshal(is.Metadata, &m) != nil {
		return nil
	}
	var out []session
	for k, v := range m {
		if id, ok := strings.CutPrefix(k, "claude_session."); ok {
			pane, _ := v.(string)
			out = append(out, session{strings.ReplaceAll(id, "_", "-"), pane})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// agentPane is a herdr pane running an agent, keyed in herdrState by the Claude session it holds now.
type agentPane struct {
	Pane      string
	Status    string // working, idle, blocked, …
	Title     string
	Workspace string
	WSLabel   string
	Tab       string
}

type herdrMsg struct {
	panes map[string]agentPane
	err   error
}

// pollHerdr lists live agent panes; herdr answers in milliseconds, so this runs on every tick.
func pollHerdr() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "herdr", "pane", "list").Output()
		if err != nil {
			return herdrMsg{err: err}
		}
		var pl struct {
			Result struct {
				Panes []struct {
					ID      string `json:"pane_id"`
					Status  string `json:"agent_status"`
					Title   string `json:"terminal_title_stripped"`
					WS      string `json:"workspace_id"`
					Tab     string `json:"tab_id"`
					Session *struct {
						Value string `json:"value"`
					} `json:"agent_session"`
				} `json:"panes"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &pl); err != nil {
			return herdrMsg{err: err}
		}
		labels := map[string]string{}
		if out, err := exec.CommandContext(ctx, "herdr", "workspace", "list").Output(); err == nil {
			var wl struct {
				Result struct {
					Workspaces []struct {
						ID    string `json:"workspace_id"`
						Label string `json:"label"`
					} `json:"workspaces"`
				} `json:"result"`
			}
			if json.Unmarshal(out, &wl) == nil {
				for _, w := range wl.Result.Workspaces {
					labels[w.ID] = w.Label
				}
			}
		}
		panes := map[string]agentPane{}
		for _, p := range pl.Result.Panes {
			if p.Session != nil && p.Session.Value != "" {
				panes[p.Session.Value] = agentPane{p.ID, p.Status, p.Title, p.WS, labels[p.WS], p.Tab}
			}
		}
		return herdrMsg{panes: panes}
	}
}

// agentOf returns the live pane of the newest session that claimed the bead, if any is still running.
func (a *App) agentOf(is *bd.Issue) (agentPane, bool) {
	for _, s := range sessions(is) {
		if p, ok := a.herdr[s.id]; ok {
			return p, true
		}
	}
	return agentPane{}, false
}

// agentNote is the Now-row badge for a claimed bead: live status and pane title, or gone.
func (a *App) agentNote(is *bd.Issue) string {
	if len(sessions(is)) == 0 || a.herdr == nil {
		return ""
	}
	p, ok := a.agentOf(is)
	if !ok {
		return sDim.Render("○ ended " + a.sessionLabel(a.latestSession(is)))
	}
	label := p.Title
	if label == "" {
		label = p.WSLabel
	}
	switch p.Status {
	case "working":
		return sWarn.Render("▶ " + label)
	case "idle":
		return sOK.Render("◦ " + label)
	}
	return sDim.Render("· " + label)
}

func transcriptPath(id string) string {
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", id+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// sessionInfo is what an ended session's transcript says about it.
type sessionInfo struct {
	Name string // the /rename title, else Claude's own title; "" when unknown
	Last time.Time
}

type sessionNamesMsg map[string]sessionInfo

// readSessionInfo scans a transcript for its latest custom or AI title; custom wins.
// Transcripts on another machine are not here, so those come back empty.
func readSessionInfo(id string) sessionInfo {
	path := transcriptPath(id)
	if path == "" {
		return sessionInfo{}
	}
	st, err := os.Stat(path)
	if err != nil {
		return sessionInfo{}
	}
	info := sessionInfo{Last: st.ModTime()}
	f, err := os.Open(path)
	if err != nil {
		return info
	}
	defer f.Close()
	var custom, ai string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		b := sc.Bytes()
		if !bytes.Contains(b, []byte(`-title"`)) {
			continue
		}
		var t struct {
			Type   string `json:"type"`
			Custom string `json:"customTitle"`
			AI     string `json:"aiTitle"`
		}
		if json.Unmarshal(b, &t) != nil {
			continue
		}
		switch {
		case t.Type == "custom-title" && t.Custom != "":
			custom = t.Custom
		case t.Type == "ai-title" && t.AI != "":
			ai = t.AI
		}
	}
	info.Name = custom
	if info.Name == "" {
		info.Name = ai
	}
	return info
}

// nameEndedSessions looks up the claimed sessions herdr no longer runs and that bt has not named yet.
func (a *App) nameEndedSessions() tea.Cmd {
	var ids []string
	for _, is := range a.snap.Issues {
		if is.Closed() {
			continue
		}
		for _, s := range sessions(is) {
			if _, live := a.herdr[s.id]; !live {
				if _, known := a.sessionNames[s.id]; !known {
					a.sessionNames[s.id] = sessionInfo{} // in flight; avoids a second scan
					ids = append(ids, s.id)
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		out := sessionNamesMsg{}
		for _, id := range ids {
			out[id] = readSessionInfo(id)
		}
		return out
	}
}

// transcriptCwd finds the directory a session ran in, from its transcript under ~/.claude/projects.
func transcriptCwd(id string) string {
	path := transcriptPath(id)
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for i := 0; sc.Scan() && i < 50; i++ {
		var line struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Cwd != "" {
			return line.Cwd
		}
	}
	return ""
}

// resume jumps to the herdr pane still working the bead, or reopens the session with claude --resume.
func (a *App) resume(is *bd.Issue) tea.Cmd {
	ss := sessions(is)
	switch len(ss) {
	case 0:
		return flash("no agent session recorded on " + a.shortID(is.ID))
	case 1:
		return a.resumeSession(ss[0])
	}
	opts := make([]option, len(ss))
	for i, s := range ss {
		opts[i] = option{s.id, s.id[:8] + "  pane " + s.pane}
	}
	a.modal = newPicker("Resume which session?", opts, func(id string) tea.Cmd {
		for _, s := range ss {
			if s.id == id {
				return a.resumeSession(s)
			}
		}
		return nil
	})
	return nil
}

// resumeSession focuses the herdr pane running the session, or reopens it with claude --resume.
func (a *App) resumeSession(s session) tea.Cmd {
	p, ok := a.herdr[s.id]
	if !ok {
		return resumeClaude(s)
	}
	return func() tea.Msg {
		if err := exec.Command("herdr", "workspace", "focus", p.Workspace).Run(); err != nil {
			return flashMsg{text: "herdr: " + err.Error(), err: true}
		}
		_ = exec.Command("herdr", "tab", "focus", p.Tab).Run()
		return flashMsg{text: "focused " + p.WSLabel + " / " + p.Title}
	}
}

func resumeClaude(s session) tea.Cmd {
	c := exec.Command("claude", "--resume", s.id)
	if cwd := transcriptCwd(s.id); cwd != "" {
		c.Dir = cwd
	}
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return flashMsg{text: "claude --resume: " + err.Error(), err: true}
		}
		return nil
	})
}

// alert reports what changed since the previous snapshot that the user would want to hear about:
// new human beads, and in-progress work that closed or got blocked.
func (a *App) alert(old *bd.Snapshot) tea.Cmd {
	events := a.alertEvents(old)
	if len(events) == 0 {
		return nil
	}
	msg := strings.Join(events, ", ")
	return tea.Batch(flash(msg), func() tea.Msg {
		if path, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command(path, "-a", "bt", "bt", msg).Run()
			return nil
		}
		if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
			_, _ = tty.WriteString("\a")
			tty.Close()
		}
		return nil
	})
}

func (a *App) alertEvents(old *bd.Snapshot) []string {
	var events []string
	for _, is := range a.snap.Issues {
		prev := old.ByID[is.ID]
		if prev == nil {
			if isHuman(is) && !is.Closed() {
				events = append(events, "needs you: "+a.shortID(is.ID))
			}
			continue
		}
		switch {
		case isHuman(is) && !is.Closed() && !isHuman(prev):
			events = append(events, "needs you: "+a.shortID(is.ID))
		case prev.Status == "in_progress" && is.Closed() && a.machineMatch(is):
			events = append(events, "closed: "+a.shortID(is.ID))
		case prev.Status == "in_progress" && !old.IsBlocked(prev) && a.snap.IsBlocked(is) && a.machineMatch(is):
			events = append(events, "blocked: "+a.shortID(is.ID))
		}
	}
	return events
}

// sessionLabel names a session by its title, falling back to the short id.
func (a *App) sessionLabel(id string) string {
	if n := a.sessionNames[id].Name; n != "" {
		return n
	}
	return id[:min(8, len(id))]
}

// latestSession is the claiming session with the most recent transcript activity.
func (a *App) latestSession(is *bd.Issue) string {
	ss := sessions(is)
	best := ss[0].id
	for _, s := range ss[1:] {
		if a.sessionNames[s.id].Last.After(a.sessionNames[best].Last) {
			best = s.id
		}
	}
	return best
}
