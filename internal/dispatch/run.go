package dispatch

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/FoamScience/beads-tui/internal/bd"
)

const Tick = 3 * time.Second

func cacheFile(name string) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bt", name)
}

// Engine carries bt dispatch from tick to tick; Step runs one tick.
type Engine struct {
	c        bd.Client
	DryRun   bool
	st       *State
	sessions map[string]string
	tried    map[string]bool
	snap     *bd.Snapshot
	stamp    string
	log      io.Writer
	unlock   func()
}

// Report is what one tick saw and did.
type Report struct {
	Cfg     *Config
	Allow   Allow
	Snap    *bd.Snapshot
	Lanes   map[string]*LaneView
	Events  []string
	Pending map[string]string // lane -> bead sent but not claimed yet; a copy, safe to read off the engine goroutine
	Err     error             // config, allow-list, bd or herdr failure that stopped the tick
}

// NewEngine takes the single-instance lock unless dryRun; call Close when done.
func NewEngine(c bd.Client, dryRun bool) (*Engine, error) {
	e := &Engine{c: c, DryRun: dryRun, st: NewState(), sessions: loadSessions(), tried: map[string]bool{}, log: io.Discard}
	if dryRun {
		return e, nil
	}
	unlock, err := lock(cacheFile("dispatch.lock"))
	if err != nil {
		return nil, err
	}
	e.unlock = unlock
	if f, err := os.OpenFile(cacheFile("dispatch.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		e.log = f
	}
	return e, nil
}

func (e *Engine) Close() {
	if f, ok := e.log.(*os.File); ok {
		f.Close()
	}
	if e.unlock != nil {
		e.unlock()
	}
}

// Step reads lanes.toml, dispatch.toml, bd and herdr, then plans and (unless DryRun) acts.
func (e *Engine) Step() Report {
	var r Report
	logf := func(format string, a ...any) {
		line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, a...)
		r.Events = append(r.Events, line)
		fmt.Fprintln(e.log, line)
	}
	cfg, err := LoadConfig(ConfigPath())
	if err != nil {
		r.Err = fmt.Errorf("lanes.toml: %w", err)
		return r
	}
	r.Cfg = cfg
	// a dispatch.toml that does not parse keeps the previous allow-list, so a typo never widens it
	if a, err := LoadAllow(AllowPath()); err != nil {
		logf("dispatch.toml: %v (keeping the previous allow-list)", err)
	} else {
		e.st.Allow = a
	}
	r.Allow = e.st.Allow
	if s := e.c.ChangeStamp(); e.snap == nil || s != e.stamp {
		issues, err := e.c.List()
		if err != nil {
			r.Err = err
			return r
		}
		e.snap, e.stamp = bd.NewSnapshot(issues), s
	}
	r.Snap = e.snap
	panes, err := listPanes()
	if err != nil {
		r.Err = fmt.Errorf("herdr: %w", err)
		return r
	}
	if !e.DryRun {
		recordSessions(cfg, panes, e.sessions)
	}
	if ask := Unrouted(cfg, e.snap, e.st.Allow, e.tried); len(ask) > 0 {
		e.route(cfg, ask, logf)
	}
	r.Lanes = Lanes(cfg, e.snap, panes, e.st.Allow) // after route: it may have opened a lane
	for _, a := range Plan(cfg, e.snap, panes, e.st, time.Now()) {
		logf("%s", a)
		if e.DryRun {
			continue
		}
		if err := execute(cfg, a); err != nil {
			logf("  failed: %v", err)
			delete(e.st.Sent, a.Lane)
		}
	}
	r.Pending = map[string]string{}
	for lane, s := range e.st.Sent {
		r.Pending[lane] = s.ID
	}
	return r
}

// Run is bt dispatch --dry-run: one tick, printed.
func Run(c bd.Client, out io.Writer) error {
	e, err := NewEngine(c, true)
	if err != nil {
		return err
	}
	r := e.Step()
	if r.Err != nil {
		return r.Err
	}
	printLanes(out, r.Cfg, r.Lanes)
	for _, ev := range r.Events {
		fmt.Fprintln(out, ev)
	}
	return nil
}

// route asks the decider about unrouted beads and labels the ones it places; each bead is asked once per run.
func (e *Engine) route(cfg *Config, ask []*bd.Issue, logf func(string, ...any)) {
	for _, is := range ask {
		e.tried[is.ID] = true
	}
	out, err := askDecider(cfg.Decider.Model, DeciderPrompt(cfg, e.snap, ask))
	if err != nil {
		logf("%v; %d beads stay unrouted until bt dispatch restarts", err, len(ask))
		return
	}
	for _, v := range ParseVerdicts(cfg, ask, out) {
		if v.New && cfg.Lanes[v.Lane] == nil {
			l, err := cfg.NewAutoLane(v.Lane, v.About)
			if err != nil {
				logf("no lane for %s: decider wanted new lane %s, %v", v.ID, v.Lane, err)
				continue
			}
			logf("lane  %-16s opened for %s: %s", l.Name, v.ID, l.About)
			if !e.DryRun {
				if err := SaveAutoLane(AutoPath(ConfigPath()), l); err != nil {
					logf("  failed: %v", err)
					continue
				}
			}
			cfg.Lanes[l.Name] = l
		}
		logf("route %-16s %s (%s)", v.Lane, v.ID, v.Why)
		if e.DryRun {
			continue
		}
		if err := e.c.Exec("update", v.ID, "--add-label", LabelLane+v.Lane); err != nil {
			logf("  failed: %v", err)
			continue
		}
		_ = e.c.Exec("note", v.ID, fmt.Sprintf("routed to lane %s by the %s decider: %s", v.Lane, cfg.Decider.Model, v.Why))
	}
}

func printLanes(out io.Writer, cfg *Config, vs map[string]*LaneView) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LANE\tPANE\tSTATUS\tACTIVE\tNEXT\tQUEUED\tWAITING\tPARTIAL\tDONE")
	for _, name := range cfg.Names() {
		v := vs[name]
		pane, status, next := "-", "gone", "-"
		if v.Live {
			pane, status = v.Pane.ID, v.Pane.Status
		}
		if len(v.Queue) > 0 {
			next = v.Queue[0].ID
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\n", name, pane, status, cmp.Or(v.Active, "-"), next,
			v.Count[Queued], v.Count[Waiting], v.Count[Partial], v.Count[Completed])
	}
	w.Flush()
}

func execute(cfg *Config, a Action) error {
	switch a.Kind {
	case Send:
		return herdr("pane", "run", a.Pane, a.Text)
	case Spawn:
		return spawn(cfg.Lanes[a.Lane])
	}
	return nil
}

// spawn opens a tab in the lane's workspace and starts its Claude session there.
func spawn(l *Lane) error {
	ws, err := workspaceID(l.Workspace)
	if err != nil {
		return err
	}
	out, err := herdrOut("tab", "create", "--workspace", ws, "--cwd", l.Cwd, "--label", l.Name, "--no-focus")
	if err != nil {
		return fmt.Errorf("herdr tab create: %w", err)
	}
	var r struct {
		Result struct {
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &r); err != nil || r.Result.Pane.ID == "" {
		return fmt.Errorf("herdr tab create: no pane in %s", out)
	}
	cmd := "claude -n " + l.Name
	if l.Revive == "resume" {
		if id := loadSessions()[l.Name]; id != "" {
			cmd = "claude --resume " + id
		}
	}
	if l.Model != "" {
		cmd += " --model " + l.Model
	}
	return herdr("pane", "run", r.Result.Pane.ID, cmd)
}

// sessions.json maps a lane to the last Claude session uuid seen running it, for revive = "resume".
func loadSessions() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(cacheFile("sessions.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func recordSessions(cfg *Config, panes []Pane, m map[string]string) {
	changed := false
	for name := range cfg.Lanes {
		if p, ok := LanePane(panes, name); ok && p.Session != "" && m[name] != p.Session {
			m[name], changed = p.Session, true
		}
	}
	if changed {
		b, _ := json.MarshalIndent(m, "", "  ")
		_ = os.WriteFile(cacheFile("sessions.json"), b, 0o644)
	}
}

const herdrTimeout = 10 * time.Second

func herdrOut(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "herdr", args...).Output()
}

func herdr(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), herdrTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "herdr", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("herdr %s %s: %w: %s", args[0], args[1], err, out)
	}
	return nil
}

func listPanes() ([]Pane, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "herdr", "pane", "list").Output()
	if err != nil {
		return nil, err
	}
	var pl struct {
		Result struct {
			Panes []struct {
				ID     string `json:"pane_id"`
				Agent  string `json:"agent"`
				Status string `json:"agent_status"`
				Title  string `json:"terminal_title_stripped"`
				WS     string `json:"workspace_id"`
				Tab    string `json:"tab_id"`
				Sess   *struct {
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &pl); err != nil {
		return nil, err
	}
	panes := make([]Pane, len(pl.Result.Panes))
	for i, p := range pl.Result.Panes {
		panes[i] = Pane{p.ID, p.Agent, p.Status, p.Title, p.WS, "", p.Tab}
		if p.Sess != nil {
			panes[i].Session = p.Sess.Value
		}
	}
	return panes, nil
}

func workspaceID(label string) (string, error) {
	out, err := herdrOut("workspace", "list")
	if err != nil {
		return "", err
	}
	var wl struct {
		Result struct {
			Workspaces []struct {
				ID    string `json:"workspace_id"`
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &wl); err != nil {
		return "", err
	}
	for _, w := range wl.Result.Workspaces {
		if w.Label == label || w.ID == label {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("no herdr workspace %q", label)
}

func lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another bt dispatch is running")
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}
