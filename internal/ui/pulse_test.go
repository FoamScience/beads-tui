package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

func toolUse(ts, id, name, input string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`, ts, id, name, input)
}

func toolResult(ts, id, text string) string {
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%q}]}}`, ts, id, text)
}

func notification(ts, task, tu string) string {
	note := fmt.Sprintf("<task-notification>\n<task-id>%s</task-id>\n<tool-use-id>%s</tool-use-id>\n<status>completed</status>\n</task-notification>", task, tu)
	return fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":%q,"content":%q}`, ts, note)
}

func TestPulseTracksBackgroundWork(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "b1.output")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","timestamp":"2026-11-18T10:00:00Z","message":{"content":"run the gate"}}`,
		toolUse("2026-11-18T10:00:01Z", "tu1", "Bash", `{"command":"make test","run_in_background":true}`),
		toolResult("2026-11-18T10:00:02Z", "tu1", "Command running in background with ID: b1. Output is being written to: "+out+". You will be notified when it completes."),
		toolUse("2026-11-18T10:00:03Z", "tu2", "Bash", `{"command":"until grep -q Ready log; do sleep 1; done","run_in_background":true}`),
		toolResult("2026-11-18T10:00:04Z", "tu2", "Command running in background with ID: b2. Output is being written to: /x/b2.output."),
		toolUse("2026-11-18T10:00:05Z", "tu3", "Monitor", `{"command":"tail -f log"}`),
		toolResult("2026-11-18T10:00:06Z", "tu3", "Monitor started (task m1, expires in 30m unless the source ends first)"),
		toolUse("2026-11-18T10:00:06Z", "tu9", "Bash", `{"command":"jq . other.jsonl"}`),
		toolResult("2026-11-18T10:00:06Z", "tu9", "Async agent launched successfully.\nagentId: a9 (quoted from a log)"),
		toolUse("2026-11-18T10:00:07Z", "tu4", "Agent", `{"description":"scan","run_in_background":true}`),
		toolResult("2026-11-18T10:00:08Z", "tu4", "Async agent launched successfully.\nagentId: a1 (internal ID)"),
		toolUse("2026-11-18T10:00:09Z", "tu5", "Bash", `{"command":"make build","run_in_background":true}`),
		toolResult("2026-11-18T10:00:10Z", "tu5", "Command running in background with ID: b3. Output is being written to: /x/b3.output."),
		toolUse("2026-11-18T10:00:10Z", "tu8", "Agent", `{"description":"review","run_in_background":true}`),
		toolResult("2026-11-18T10:00:10Z", "tu8", "Async agent launched successfully.\nagentId: a2 (internal ID)"),
		toolUse("2026-11-18T10:00:11Z", "tu6", "Edit", `{"file_path":"/r/internal/ui/pulse.go"}`),
		`{"type":"system","subtype":"turn_duration","timestamp":"2026-11-18T10:00:12Z"}`,
	}
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := pulse{path: path}.clone()
	p.scan()
	if bg, spawn, _, _, _ := p.counts(); bg != 2 || spawn != 2 {
		t.Fatalf("after start: bg=%d spawn=%d, want 2 and 2 (waiter and monitor excluded); pending %v", bg, spawn, p.pending)
	}
	if p.tool != "Edit pulse.go" || p.turnEnd.Format(time.TimeOnly) != "10:00:12" || p.turnStart.Format(time.TimeOnly) != "10:00:00" {
		t.Fatalf("tool %q turnStart %v turnEnd %v", p.tool, p.turnStart, p.turnEnd)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	more := notification("2026-11-18T10:05:00Z", "b1", "tu1") + "\n" +
		toolUse("2026-11-18T10:05:01Z", "tu7", "TaskStop", `{"task_id":"b3"}`) + "\n" +
		`{"type":"user","timestamp":"2026-11-18T10:06:00Z","message":{"content":"<task-notification>\n<task-id>a1</task-id>\n<status>completed</status>"}}` + "\n" +
		`{"type":"queue-operation","operation":"remove","timestamp":"2026-11-18T10:06:01Z","content":"<agent-message from=\"a2\">\n[Subagent hand-back] report"}` + "\n" +
		`{"type":"system","subtype":"turn_dur` // partial line, completed by a later write
	if _, err := f.WriteString(more); err != nil {
		t.Fatal(err)
	}
	f.Close()
	p.scan()
	if len(p.pending) != 0 {
		t.Fatalf("pending after notifications and TaskStop: %v", p.pending)
	}
	if !p.turnStart.Equal(time.Date(2026, 11, 18, 10, 5, 1, 0, time.UTC)) {
		t.Fatalf("the turn a notification wakes must start at its first entry: turnStart %v", p.turnStart)
	}
	off := p.off
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`ation","timestamp":"2026-11-18T10:07:00Z"}` + "\n")
	f.Close()
	p.scan()
	if p.off <= off || p.turnEnd.Format(time.TimeOnly) != "10:07:00" {
		t.Fatalf("partial line not resumed: off %d -> %d, turnEnd %v", off, p.off, p.turnEnd)
	}
}

func TestAgentStripShowsLiveState(t *testing.T) {
	issues := fixture()
	issues[0].Metadata = []byte(`{"claude_session.orch":"p"}`)
	issues[1].Metadata = []byte(`{"claude_session.k1":"p"}`)
	issues = append(issues, bd.Issue{ID: "t-z", IssueType: "task", Status: "open", UpdatedAt: time.Now(),
		Metadata: []byte(`{"claude_session.bgs":"p"}`)})
	a := New(bd.Client{Dir: t.TempDir()})
	a.state, a.host = defaultState(), "u-host"
	a.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	a.Update(snapshotMsg{issues: issues, gen: a.gen})
	a.active = a.viewIndex("Activity")
	a.Update(herdrMsg{panes: map[string]agentPane{
		"orch": {Status: "idle", Title: "boss"},
		"k1":   {Status: "working", Title: "t-a.1"},
		"bgs":  {Status: "idle", Title: "builder"},
	}})
	now := time.Now()
	a.Update(pulseMsg{
		"orch": {turnEnd: now.Add(-20 * time.Minute)},
		"k1":   {turnStart: now.Add(-3 * time.Minute)},
		"bgs": {turnEnd: now.Add(-9 * time.Minute), pending: map[string]bgTask{
			"tu1": {start: now.Add(-12 * time.Minute), outAt: now},
			"tu2": {start: now.Add(-5 * time.Minute), outAt: now},
		}},
	})
	out := ansi.Strip(a.render())
	for _, want := range []string{icSpawn + " boss", "sp1 20m", icWaitBg + " builder", "bg2 12m", icWorking + " 1 working", "waiting on 2 bg"} {
		if !strings.Contains(out, want) {
			t.Errorf("activity lacks %q:\n%s", want, out)
		}
	}

	a.Update(herdrMsg{panes: map[string]agentPane{"orch": {Status: "idle", Title: "boss"}, "bgs": {Status: "idle", Title: "builder"}}})
	a.Update(pulseMsg{
		"orch": {turnEnd: now.Add(-20 * time.Minute)},
		"bgs":  {turnEnd: now.Add(-9 * time.Minute), pending: map[string]bgTask{"tu1": {start: now.Add(-40 * time.Minute), outAt: now.Add(-30 * time.Minute)}}},
	})
	if out := ansi.Strip(a.render()); !strings.Contains(out, "bg1? 40m") {
		t.Errorf("silent background command not flagged as stalled:\n%s", out)
	}
	a.Update(pulseMsg{"orch": {turnEnd: now.Add(-20 * time.Minute)}, "bgs": {turnEnd: now.Add(-9 * time.Minute)}})
	if out := ansi.Strip(a.render()); !strings.Contains(out, "all idle 9m") || !strings.Contains(out, icIdle+" boss") {
		t.Errorf("idle machine not summed up:\n%s", out)
	}
}

func TestPulseTracksQueuedJobs(t *testing.T) {
	for cmd, want := range map[string]bool{
		"tspn 4 make test":                         true,
		"id=$(tspn 16 ninja -C build) && echo $id": true,
		"tspg python train.py":                     true,
		"tsp -N 2 make":                            true,
		"tspn 4 -f make":                           true,
		"tsp -w 12":                                false,
		"tsp -l | tail -3":                         false,
		"until tsp -s 11 | grep -q finished; do sleep 30; done": false,
	} {
		if got := reEnqueue.MatchString(cmd); got != want {
			t.Errorf("enqueue %q = %v, want %v", cmd, got, want)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tsp"), []byte("#!/bin/sh\nprintf 'ID State\\n7 running\\n8 finished\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	out := filepath.Join(dir, "b9.output")
	os.WriteFile(out, []byte("8\n"), 0o644)
	running, gone := filepath.Join(dir, "b10.output"), filepath.Join(dir, "b11.output")
	os.WriteFile(gone, nil, 0o644)
	held, err := os.Create(running) // stands in for the shell still writing it
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	lines := []string{
		toolUse("2026-11-18T10:00:00Z", "tu1", "Bash", `{"command":"tspn 4 make test"}`),
		toolResult("2026-11-18T10:00:01Z", "tu1", "7"),
		toolUse("2026-11-18T10:00:02Z", "tu2", "Bash", `{"command":"tspn 4 make build","run_in_background":true}`),
		toolResult("2026-11-18T10:00:03Z", "tu2", "Command running in background with ID: b9. Output is being written to: "+out),
		notification("2026-11-18T10:00:04Z", "b9", "tu2"),
		toolUse("2026-11-18T10:00:05Z", "tu3", "Bash", `{"command":"until tsp -s 7 | grep -q finished; do sleep 30; done","run_in_background":true}`),
		toolResult("2026-11-18T10:00:06Z", "tu3", "Command was manually backgrounded by user with ID: b10. Output is being written to: "+running),
		toolUse("2026-11-18T10:00:07Z", "tu4", "Bash", `{"command":"make long"}`),
		toolResult("2026-11-18T10:00:08Z", "tu4", "Command did not complete within its 900s timeout and was moved to the background (ID: b11). Output is being written to: "+gone),
	}
	path := filepath.Join(dir, "s.jsonl")
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	ps := map[string]pulse{"s": pulse{path: path}.clone()}
	ps = scanPulses(ps)().(pulseMsg)
	p := ps["s"]
	if _, ok := p.jobs["cpu:7"]; !ok || len(p.jobs) != 1 {
		t.Fatalf("jobs %v, want only cpu:7 (8 is finished)", p.jobs)
	}
	if bg, _, _, _, stalled := p.counts(); bg != 2 || stalled {
		t.Fatalf("bg=%d stalled=%v, want 2 (job 7, tsp poll loop; the timed-out command's shell is gone) not stalled; pending %v", bg, stalled, p.pending)
	}
}

func TestOrphanWithDeletedOutputIsDropped(t *testing.T) {
	ps := map[string]pulse{"s": {pending: map[string]bgTask{"tu1": {out: filepath.Join(t.TempDir(), "gone.output")}}}}
	dropOrphans(ps)
	if len(ps["s"].pending) != 0 {
		t.Fatalf("command whose output file was deleted still pending: %v", ps["s"].pending)
	}
}
