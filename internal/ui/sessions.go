package ui

import (
	"bufio"
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

type paneInfo struct {
	Workspace string `json:"workspace_id"`
	Tab       string `json:"tab_id"`
	Agent     string `json:"agent"`
}

// livePane asks herdr whether the pane still runs an agent.
func livePane(pane string) (paneInfo, bool) {
	if pane == "" || pane == "none" {
		return paneInfo{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "herdr", "pane", "get", pane).Output()
	if err != nil {
		return paneInfo{}, false
	}
	var r struct {
		Result struct {
			Pane paneInfo `json:"pane"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil || r.Result.Pane.Agent == "" {
		return paneInfo{}, false
	}
	return r.Result.Pane, true
}

// transcriptCwd finds the directory a session ran in, from its transcript under ~/.claude/projects.
func transcriptCwd(id string) string {
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", id+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	f, err := os.Open(matches[0])
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
		return resumeSession(ss[0])
	}
	opts := make([]option, len(ss))
	for i, s := range ss {
		opts[i] = option{s.id, s.id[:8] + "  pane " + s.pane}
	}
	a.modal = newPicker("Resume which session?", opts, func(id string) tea.Cmd {
		for _, s := range ss {
			if s.id == id {
				return resumeSession(s)
			}
		}
		return nil
	})
	return nil
}

type resumeMsg struct{ s session }

// resumeSession checks herdr off the UI goroutine; resumeMsg then reopens the session when no pane holds it.
func resumeSession(s session) tea.Cmd {
	return func() tea.Msg {
		p, ok := livePane(s.pane)
		if !ok {
			return resumeMsg{s}
		}
		if err := exec.Command("herdr", "workspace", "focus", p.Workspace).Run(); err != nil {
			return flashMsg{text: "herdr: " + err.Error(), err: true}
		}
		_ = exec.Command("herdr", "tab", "focus", p.Tab).Run()
		return flashMsg{text: "focused herdr pane " + s.pane}
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
