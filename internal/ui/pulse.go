package ui

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// stallAfter is how long a pending background command may go without writing output before it is flagged.
const stallAfter = 15 * time.Minute

// bgTask is a background Bash command or async subagent a session started and has not heard back from.
type bgTask struct {
	spawn bool // an async subagent, not a command
	start time.Time
	out   string    // output file
	outAt time.Time // its last write, refreshed on every scan
}

// pulse is what a live session's transcript says it is doing, read incrementally from off.
type pulse struct {
	path      string
	off       int64
	pending   map[string]bgTask    // by tool_use id, until its task-notification
	tasks     map[string]string    // task id -> tool_use id, for TaskStop
	waiters   map[string]bool      // background Bash tool_use ids that only wait on a condition
	calls     map[string]string    // tool_use id -> tool name of Bash and Agent calls awaiting their result
	enqueues  map[string]string    // tool_use id -> tsp queue ("cpu" or "gpu"), for Bash calls that queue a job
	jobs      map[string]time.Time // "<queue>:<job id>" -> queued at, until tsp lists it finished
	turnStart time.Time            // first entry of the current or latest turn, whatever started it
	turnEnd   time.Time            // latest finished turn
	last      time.Time            // latest entry
	tool      string               // latest tool call, e.g. "Edit sessions.go"
}

func (p pulse) clone() pulse {
	p.pending, p.tasks, p.waiters = maps.Clone(p.pending), maps.Clone(p.tasks), maps.Clone(p.waiters)
	p.enqueues, p.jobs, p.calls = maps.Clone(p.enqueues), maps.Clone(p.jobs), maps.Clone(p.calls)
	if p.pending == nil {
		p.pending, p.tasks, p.waiters, p.calls = map[string]bgTask{}, map[string]string{}, map[string]bool{}, map[string]string{}
	}
	if p.jobs == nil {
		p.enqueues, p.jobs = map[string]string{}, map[string]time.Time{}
	}
	return p
}

// counts is how many background commands (tsp jobs included) and subagents are pending, and when the
// oldest of each started. Queued jobs write no output the session owns, so they never count as stalled.
func (p pulse) counts() (bg, spawn int, bgSince, spawnSince time.Time, stalled bool) {
	for _, at := range p.jobs {
		bg++
		if bgSince.IsZero() || at.Before(bgSince) {
			bgSince = at
		}
	}
	jobs := bg
	var fresh time.Time
	for _, t := range p.pending {
		if t.spawn {
			spawn++
			if spawnSince.IsZero() || t.start.Before(spawnSince) {
				spawnSince = t.start
			}
			continue
		}
		bg++
		if bgSince.IsZero() || t.start.Before(bgSince) {
			bgSince = t.start
		}
		fresh = latest(fresh, latest(t.outAt, t.start))
	}
	return bg, spawn, bgSince, spawnSince, jobs == 0 && bg > 0 && time.Since(fresh) > stallAfter
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

type pulseMsg map[string]pulse

// scanPulses tails the transcripts of the live sessions in ps and returns their updated pulses.
func scanPulses(ps map[string]pulse) tea.Cmd {
	return func() tea.Msg {
		for id, p := range ps {
			if p.path == "" {
				p.path = transcriptPath(id)
			}
			if p.path != "" {
				p.scan()
			}
			ps[id] = p
		}
		settleJobs(ps)
		dropOrphans(ps)
		return pulseMsg(ps)
	}
}

// scan reads the transcript from off up to its last complete line, then stamps pending output files.
func (p *pulse) scan() {
	f, err := os.Open(p.path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(p.off, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break // a partial last line is read again next time
		}
		p.off += int64(len(line))
		p.line(line)
	}
	for id, t := range p.pending {
		if t.out == "" {
			continue
		}
		if st, err := os.Stat(t.out); err == nil {
			t.outAt = st.ModTime()
			p.pending[id] = t
		}
	}
}

type tEntry struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	Operation string          `json:"operation"`
	Timestamp time.Time       `json:"timestamp"`
	Content   json.RawMessage `json:"content"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type tBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	Input     struct {
		Command string `json:"command"`
		Bg      bool   `json:"run_in_background"`
		File    string `json:"file_path"`
		Pattern string `json:"pattern"`
		Desc    string `json:"description"`
		Skill   string `json:"skill"`
		TaskID  string `json:"task_id"`
		ShellID string `json:"shell_id"`
	} `json:"input"`
}

var (
	reBgStart = regexp.MustCompile(`^(?:Command running in background with ID: |Command was manually backgrounded by user with ID: |` +
		`Command did not complete within its \d+s timeout and was moved to the background \(ID: )(\w+)\)?\. Output is being written to: (\S+?)\.?(\s|$)`)
	reAgentStart = regexp.MustCompile(`^Async agent launched[^\n]*\nagentId: (\w+)`)
	reNoteTool   = regexp.MustCompile(`<tool-use-id>([^<]+)</tool-use-id>`)
	reNoteTask   = regexp.MustCompile(`<task-id>([^<]+)</task-id>`)
	reHandBack   = regexp.MustCompile(`^<agent-message from="(\w+)">\s*\[Subagent hand-back\]`)
	reNoteStatus = regexp.MustCompile(`<status>[a-z]+</status>`)
	// a background shell that loops until something else is ready, e.g. `until grep -q Ready log; do sleep 1; done`;
	// one polling a tsp job waits on real work, so it is not a waiter
	reWaiter = regexp.MustCompile(`^\s*(until|while)\b[\s\S]*\bsleep\b`)
	reTsp    = regexp.MustCompile(`\btsp\b`)
	// a tsp call that queues a job: tspn <slots>, or tsp/tspg with a command or queueing flags, not a query
	reEnqueue = regexp.MustCompile(`(?:^|[^\w-])(?:tspn \d+|tsp|(tspg))\s+(?:-[NLnEgDBzdf]\b\S*\s+(?:\S+\s+)?)*[^-\s]`)
	reJobID   = regexp.MustCompile(`^\s*(\d+)\s*(\n|$)`)
)

const notePrefix = "<task-notification>"

func (p *pulse) line(b []byte) {
	if !bytes.Contains(b, []byte(`"type":"assistant"`)) && !bytes.Contains(b, []byte(`"type":"user"`)) &&
		!bytes.Contains(b, []byte(`"turn_duration"`)) && !bytes.Contains(b, []byte(`"type":"queue-operation"`)) {
		return
	}
	var e tEntry
	if json.Unmarshal(b, &e) != nil {
		return
	}
	if !e.Timestamp.IsZero() {
		p.last = latest(p.last, e.Timestamp)
		if (e.Type == "user" || e.Type == "assistant") && !p.turnStart.After(p.turnEnd) {
			p.turnStart = e.Timestamp
		}
	}
	switch e.Type {
	case "system":
		if e.Subtype == "turn_duration" {
			p.turnEnd = e.Timestamp
		}
	case "queue-operation":
		var s string
		if json.Unmarshal(e.Content, &s) == nil {
			p.notified(s)
		}
	case "assistant":
		var blocks []tBlock
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return
		}
		for _, bl := range blocks {
			if bl.Type != "tool_use" {
				continue
			}
			p.tool = toolLabel(bl)
			switch bl.Name {
			case "Agent":
				p.calls[bl.ID] = bl.Name
			case "Bash":
				p.calls[bl.ID] = bl.Name
				c := bl.Input.Command
				if bl.Input.Bg && reWaiter.MatchString(c) && !reTsp.MatchString(c) {
					p.waiters[bl.ID] = true
				}
				if m := reEnqueue.FindStringSubmatch(c); m != nil {
					p.enqueues[bl.ID] = "cpu"
					if m[1] != "" {
						p.enqueues[bl.ID] = "gpu"
					}
				}
			case "TaskStop":
				if tu, ok := p.tasks[cmp.Or(bl.Input.TaskID, bl.Input.ShellID)]; ok {
					delete(p.pending, tu)
				}
			}
		}
	case "user":
		var s string
		if json.Unmarshal(e.Message.Content, &s) == nil {
			if strings.HasPrefix(s, notePrefix) || reHandBack.MatchString(s) {
				p.notified(s)
			}
			return
		}
		var blocks []tBlock
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return
		}
		for _, bl := range blocks {
			if bl.Type == "tool_result" {
				p.started(bl.ToolUseID, resultText(bl.Content), e.Timestamp)
			}
		}
	}
}

// started records a background command or async agent from its tool result; a waiter shell is dropped.
// A foreground call that queued a tsp job leaves the job pending instead.
func (p *pulse) started(tu, text string, at time.Time) {
	name := p.calls[tu]
	delete(p.calls, tu)
	if q, ok := p.enqueues[tu]; ok && !reBgStart.MatchString(text) {
		delete(p.enqueues, tu)
		p.queued(q, text, at)
	}
	if m := reBgStart.FindStringSubmatch(text); m != nil && name == "Bash" {
		if p.waiters[tu] {
			delete(p.waiters, tu)
			return
		}
		p.pending[tu] = bgTask{start: at, out: m[2]}
		p.tasks[m[1]] = tu
	} else if m := reAgentStart.FindStringSubmatch(text); m != nil && name == "Agent" {
		p.pending[tu] = bgTask{spawn: true, start: at}
		p.tasks[m[1]] = tu
	}
}

// notified ends the task a final task-notification or a subagent's hand-back names, by its tool_use id or,
// when the notification carries none, its task id. A monitor's progress events carry no status.
func (p *pulse) notified(s string) {
	var tu string
	if m := reHandBack.FindStringSubmatch(s); m != nil {
		tu = p.tasks[m[1]]
	} else if strings.HasPrefix(s, notePrefix) && reNoteStatus.MatchString(s) {
		if m := reNoteTool.FindStringSubmatch(s); m != nil {
			tu = m[1]
		} else if m := reNoteTask.FindStringSubmatch(s); m != nil {
			tu = p.tasks[m[1]]
		}
	}
	if tu == "" {
		return
	}
	if q, ok := p.enqueues[tu]; ok {
		delete(p.enqueues, tu)
		if out, err := os.ReadFile(p.pending[tu].out); err == nil {
			p.queued(q, string(out), p.pending[tu].start)
		}
	}
	delete(p.pending, tu)
	delete(p.waiters, tu)
}

// queued records the tsp job whose id a queueing call printed first.
func (p *pulse) queued(q, out string, at time.Time) {
	if m := reJobID.FindStringSubmatch(out); m != nil {
		p.jobs[q+":"+m[1]] = at
	}
}

// settleJobs drops the tsp jobs the queues list as finished or no longer list; a queue that cannot be
// listed keeps its jobs.
func settleJobs(ps map[string]pulse) {
	lists := map[string]map[string]string{}
	for _, p := range ps {
		for k := range p.jobs {
			q, _, _ := strings.Cut(k, ":")
			if _, ok := lists[q]; !ok {
				lists[q] = tspList(map[string]string{"cpu": "tsp", "gpu": "tspg"}[q])
			}
		}
	}
	for _, p := range ps {
		for k := range p.jobs {
			q, id, _ := strings.Cut(k, ":")
			if l := lists[q]; l != nil && (l[id] == "" || l[id] == "finished") {
				delete(p.jobs, k)
			}
		}
	}
}

// tspList maps each job id in a tsp queue to its state; nil when the queue cannot be listed.
func tspList(bin string) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-l").Output()
	if err != nil {
		return nil
	}
	states := map[string]string{}
	for _, l := range strings.Split(string(out), "\n")[1:] {
		if f := strings.Fields(l); len(f) >= 2 {
			states[f[0]] = f[1]
		}
	}
	return states
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil && len(parts) > 0 {
		return parts[0].Text
	}
	return ""
}

// toolLabel names a tool call by its tool and its most telling argument.
func toolLabel(b tBlock) string {
	arg := b.Input.Desc
	switch {
	case b.Input.File != "":
		arg = filepath.Base(b.Input.File)
	case b.Input.Pattern != "":
		arg = b.Input.Pattern
	case b.Input.Skill != "":
		arg = b.Input.Skill
	}
	if arg == "" {
		return b.Name
	}
	return b.Name + " " + strings.Join(strings.Fields(arg), " ")
}

// pulseScan tails the transcripts of every live session, unless a scan is still running.
func (a *App) pulseScan() tea.Cmd {
	if a.herdr == nil || a.scanning {
		return nil
	}
	ps := map[string]pulse{}
	for id := range a.herdr {
		ps[id] = a.pulses[id].clone()
	}
	if len(ps) == 0 {
		return nil
	}
	a.scanning = true
	return scanPulses(ps)
}

// liveState is where a live session stands, ranked from busiest to most forgotten.
type liveState int

const (
	lsWorking liveState = iota
	lsWaitBg
	lsWaitSpawn
	lsBlocked
	lsIdle
)

// liveOf is the state of a live session, how many tasks it waits on, and since when; kids counts its
// dispatched bead sessions still busy.
func (a *App) liveOf(id string, kids int) (st liveState, n int, since time.Time, stalled bool) {
	p := a.pulses[id]
	bg, spawn, bgSince, spawnSince, stalled := p.counts()
	switch a.herdr[id].Status {
	case "working":
		return lsWorking, 0, cmp.Or(p.turnStart, p.last), false
	case "blocked":
		return lsBlocked, 0, p.last, false
	}
	idle := cmp.Or(p.turnEnd, p.last)
	switch {
	case bg > 0:
		return lsWaitBg, bg, bgSince, stalled
	case spawn > 0:
		return lsWaitSpawn, spawn + kids, spawnSince, false
	case kids > 0:
		return lsWaitSpawn, kids, idle, false
	}
	return lsIdle, 0, idle, false
}

// busy tells whether a live session is working or waiting on work it started.
func (a *App) busy(id string) bool {
	if _, live := a.herdr[id]; !live {
		return false
	}
	st, _, _, _ := a.liveOf(id, 0)
	return st <= lsWaitSpawn
}

// liveBadge draws a live session's state for the agent strip: a glyph and style, and a tag with its age.
func (a *App) liveBadge(id string, kids int) (glyph string, st int, tail string) {
	s, n, since, stalled := a.liveOf(id, kids)
	ago := age(since)
	switch s {
	case lsWorking:
		return icWorking, stWarn, ago
	case lsWaitBg:
		tag := fmt.Sprintf("bg%d", n)
		if stalled {
			return icWaitBg, stErr, tag + "? " + ago
		}
		return icWaitBg, stBlue, tag + " " + ago
	case lsWaitSpawn:
		return icSpawn, stBlue, fmt.Sprintf("sp%d %s", n, ago)
	case lsBlocked:
		return icBlocked, stErr, ago
	}
	return icIdle, stOK, ago
}

// pulseHeader sums up every live session on this machine: all idle and for how long, or what is
// still moving.
func (a *App) pulseHeader() string {
	if len(a.herdr) == 0 {
		return ""
	}
	var working, blocked, bg, spawn int
	var quiet time.Time
	for id := range a.herdr {
		b, s, _, _, _ := a.pulses[id].counts()
		bg, spawn = bg+b, spawn+s
		st, _, since, _ := a.liveOf(id, 0)
		switch st {
		case lsWorking:
			working++
		case lsBlocked:
			blocked++
		}
		if st == lsIdle || st == lsBlocked {
			quiet = latest(quiet, since)
		}
	}
	var parts []string
	if working == 0 && bg == 0 && spawn == 0 {
		parts = append(parts, sOK.Render(icIdle+" all idle "+age(quiet)))
	} else {
		if working > 0 {
			parts = append(parts, sWarn.Render(fmt.Sprintf(icWorking+" %d working", working)))
		}
		var wait []string
		if bg > 0 {
			wait = append(wait, fmt.Sprintf("%d bg", bg))
		}
		if spawn > 0 {
			wait = append(wait, fmt.Sprintf("%d spawn", spawn))
		}
		if len(wait) > 0 {
			parts = append(parts, sDim.Foreground(cBlue).Render(icWaitBg+" waiting on "+strings.Join(wait, " / ")))
		}
	}
	if blocked > 0 {
		parts = append(parts, sErr.Render(fmt.Sprintf(icBlocked+" %d blocked", blocked)))
	}
	return strings.Join(parts, sDim.Render(" · "))
}

// dropOrphans ends the background commands whose shell is gone without a notification, as when the
// claude process that ran them exited: a running shell holds its output file open. Off Linux nothing
// is dropped.
func dropOrphans(ps map[string]pulse) {
	want := map[string]string{} // resolved output path -> as the transcript gave it
	pending := false
	for _, p := range ps {
		for _, t := range p.pending {
			if t.out == "" {
				continue
			}
			pending = true
			if real, err := filepath.EvalSymlinks(t.out); err == nil {
				want[real] = t.out
			}
		}
	}
	if !pending {
		return
	}
	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	if len(fds) == 0 {
		return
	}
	open := map[string]bool{}
	for _, fd := range fds {
		if l, err := os.Readlink(fd); err == nil {
			if out, ok := want[l]; ok {
				open[out] = true
			}
		}
	}
	for _, p := range ps {
		for tu, t := range p.pending {
			if t.out != "" && !open[t.out] {
				delete(p.pending, tu)
			}
		}
	}
}
