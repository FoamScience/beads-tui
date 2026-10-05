package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

var issueTypes = []string{"task", "bug", "feature", "chore", "epic", "decision", "spike"}

// action handles the one-key mutations that work on the selected issue from any view.
func (a *App) action(k string) (tea.Cmd, bool) {
	switch k {
	case "S":
		return a.sync(), true
	case "a":
		return a.createChild(a.selected()), true
	}
	is := a.selected()
	if is == nil {
		return nil, false
	}
	id := is.ID
	switch k {
	case "s":
		opts := make([]option, 0, len(statuses))
		for _, s := range statuses {
			opts = append(opts, option{s, statusGlyph[s] + " " + s})
		}
		a.modal = newPicker("Status of "+a.shortID(id), opts, func(st string) tea.Cmd {
			if st == "closed" {
				a.closeFlow(id)
				return nil
			}
			run := func() tea.Cmd {
				return a.write(a.shortID(id)+" → "+st, a.mut(id, func(i *bd.Issue) { i.Status = st }), "update", id, "--status", st)
			}
			if is.IssueType == "epic" {
				a.modal = newConfirm(fmt.Sprintf("Set epic %s to %s?", a.shortID(id), st), run)
				return nil
			}
			return run()
		})
	case "C":
		return a.write("claim "+a.shortID(id), a.mut(id, func(i *bd.Issue) { i.Status = "in_progress" }), "update", id, "--claim"), true
	case "c":
		a.closeFlow(id)
	case "n":
		a.modal = newPrompt("Note on "+a.shortID(id), "", func(t string) tea.Cmd {
			if t == "" {
				return nil
			}
			return a.write("note "+a.shortID(id), a.mut(id, func(i *bd.Issue) { i.Notes = appendNote(i.Notes, t) }), "note", id, "--", t)
		})
	case "p":
		var opts []option
		for p := 0; p <= 4; p++ {
			opts = append(opts, option{strconv.Itoa(p), fmt.Sprintf("P%d", p)})
		}
		a.modal = newPicker("Priority of "+a.shortID(id), opts, func(v string) tea.Cmd {
			n, _ := strconv.Atoi(v)
			return a.write(a.shortID(id)+" → P"+v, a.mut(id, func(i *bd.Issue) { i.Priority = n }), "update", id, "--priority", v)
		})
	case "l":
		var opts []option
		for _, l := range a.knownLabels("") {
			mark := "  "
			if is.HasLabel(l) {
				mark = "✓ "
			}
			opts = append(opts, option{l, mark + l})
		}
		a.modal = newPicker("Toggle label on "+a.shortID(id), opts, func(l string) tea.Cmd {
			if cur := a.snap.ByID[id]; cur != nil && cur.HasLabel(l) {
				return a.write("-"+l, nil, "update", id, "--remove-label", l)
			}
			return a.write("+"+l, nil, "update", id, "--add-label", l)
		})
	case "m":
		var opts []option
		for _, l := range a.knownLabels("machine:") {
			opts = append(opts, option{l, strings.TrimPrefix(l, "machine:")})
		}
		a.modal = newPicker("Machine for "+a.shortID(id), opts, func(l string) tea.Cmd {
			if !strings.HasPrefix(l, "machine:") {
				l = "machine:" + l
			}
			args := []string{"update", id, "--add-label", l}
			if cur := a.snap.ByID[id]; cur != nil {
				if old := cur.LabelWithPrefix("machine:"); old != "" && "machine:"+old != l {
					args = append(args, "--remove-label", "machine:"+old)
				}
			}
			return a.write(a.shortID(id)+" on "+strings.TrimPrefix(l, "machine:"), nil, args...)
		})
		a.modal.input.Placeholder = "filter or type a host"
	case "x":
		m := newPicker("External ref for "+a.shortID(id), []option{{"", "(clear)"}}, func(v string) tea.Cmd {
			return a.write(a.shortID(id)+" ref "+v, a.mut(id, func(i *bd.Issue) { i.ExternalRef = v }), "update", id, "--external-ref", v)
		})
		m.input.Placeholder = "filter wl keys or type a ref"
		a.modal = m
		return func() tea.Msg { return wlKeysMsg{m, wlKeys()} }, true
	case "e":
		cur := ""
		if is.EstimatedMinutes > 0 {
			cur = strconv.Itoa(is.EstimatedMinutes)
		}
		a.modal = newPrompt("Estimate for "+a.shortID(id)+" (minutes, or 2h / 1h30m)", cur, func(v string) tea.Cmd {
			m, err := parseMinutes(v)
			if err != nil {
				return flashErr(err)
			}
			return a.write(a.shortID(id)+" est "+minutes(m), a.mut(id, func(i *bd.Issue) { i.EstimatedMinutes = m }), "update", id, "-e", strconv.Itoa(m))
		})
	case "d":
		a.modal = newPrompt("Defer "+a.shortID(id)+" until (YYYY-MM-DD, +3d, empty clears)", "", func(v string) tea.Cmd {
			v, err := parseDate(v)
			if err != nil {
				return flashErr(err)
			}
			return a.write(a.shortID(id)+" deferred "+v, nil, "update", id, "--defer", v)
		})
	case "y":
		return tea.Batch(tea.SetClipboard(id), flash("copied "+id)), true
	case "o":
		return a.openRefs(is), true
	default:
		return nil, false
	}
	return nil, true
}

func (a *App) createChild(sel *bd.Issue) tea.Cmd {
	parent := ""
	if sel != nil {
		if sel.IssueType == "epic" {
			parent = sel.ID
		} else if e := a.snap.Epic(sel); e != nil {
			parent = e.ID
		}
	}
	title := "New issue"
	if parent != "" {
		title = "New child of " + a.shortID(parent)
	}
	a.modal = newPrompt(title+": title", "", func(t string) tea.Cmd {
		if t == "" {
			return nil
		}
		var opts []option
		for _, ty := range issueTypes {
			opts = append(opts, option{ty, ty})
		}
		a.modal = newPicker("Type", opts, func(ty string) tea.Cmd {
			args := []string{"create", "-t", ty, "--silent", "-l", "machine:" + a.host}
			if parent != "" {
				args = append(args, "--parent", parent)
			}
			return a.write("create "+t, nil, append(args, "--", t)...)
		})
		return nil
	})
	return nil
}

// sync runs bd sync: pull, conflict check, is_blocked repair, push with retry.
func (a *App) sync() tea.Cmd {
	return a.write("sync", nil, "sync")
}

func (a *App) knownLabels(prefix string) []string {
	seen := map[string]bool{}
	for _, is := range a.snap.Issues {
		for _, l := range is.Labels {
			if strings.HasPrefix(l, prefix) {
				seen[l] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// wlKeys lists today's worklog keys when the wl CLI is installed.
func wlKeys() []option {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wl", "list", "--json").Output()
	if err != nil {
		return nil
	}
	var res struct {
		Tasks []struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	if json.Unmarshal(out, &res) != nil {
		return nil
	}
	var opts []option
	seen := map[string]bool{}
	for _, t := range res.Tasks {
		if t.Key != "" && !seen[t.Key] {
			seen[t.Key] = true
			opts = append(opts, option{t.Key, t.Key + "  " + t.Title})
		}
	}
	return opts
}

func parseMinutes(v string) (int, error) {
	v = strings.TrimSpace(v)
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("estimate %q: use minutes or a duration like 1h30m", v)
	}
	return int(d.Minutes()), nil
}

var relDate = regexp.MustCompile(`^\+(\d+)([dw])$`)

func parseDate(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if m := relDate.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		if m[2] == "w" {
			n *= 7
		}
		return time.Now().AddDate(0, 0, n).Format("2006-01-02"), nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", fmt.Errorf("date %q: use YYYY-MM-DD or +3d / +2w", v)
	}
	return v, nil
}

// mut applies an optimistic change to the issue as it is in the snapshot when the write starts.
func (a *App) mut(id string, f func(*bd.Issue)) func() {
	return func() {
		if is := a.snap.ByID[id]; is != nil {
			f(is)
		}
	}
}

// openDescendants lists unclosed descendants, deepest first, so one bd close call can take them before the parent.
func (a *App) openDescendants(id string) []string {
	var out []string
	var walk func(string)
	walk = func(p string) {
		for _, c := range a.snap.Children[p] {
			walk(c.ID)
			if !c.Closed() {
				out = append(out, c.ID)
			}
		}
	}
	walk(id)
	return out
}

// closeFlow closes an issue; bd refuses a parent with open children or a live blocker, so those cases ask first.
func (a *App) closeFlow(id string) {
	is := a.snap.ByID[id]
	if is == nil {
		return
	}
	short := a.shortID(id)
	kids := a.openDescendants(id)
	blockers := a.snap.Blockers(is)
	ask := func(ids []string, force bool) {
		label := short
		if len(ids) > 1 {
			label = fmt.Sprintf("%s and %d descendant(s)", short, len(ids)-1)
		}
		a.modal = newPrompt("Close "+label+": reason (optional)", "", func(r string) tea.Cmd {
			args := append([]string{"close"}, ids...)
			if r != "" {
				args = append(args, "--reason", r)
			}
			if force {
				args = append(args, "--force")
			}
			return a.write("close "+label, func() {
				for _, x := range ids {
					a.mut(x, func(i *bd.Issue) { i.Status = "closed" })()
				}
			}, args...)
		})
	}
	if len(kids) == 0 && len(blockers) == 0 {
		if is.IssueType == "epic" {
			a.modal = newConfirm("Close epic "+short+"?", func() tea.Cmd { ask([]string{id}, false); return nil })
			return
		}
		ask([]string{id}, false)
		return
	}
	var why []string
	if len(kids) > 0 {
		why = append(why, fmt.Sprintf("%d open descendant(s)", len(kids)))
	}
	if len(blockers) > 0 {
		why = append(why, fmt.Sprintf("%d open blocker(s)", len(blockers)))
	}
	var opts []option
	if len(kids) > 0 {
		opts = append(opts, option{"cascade", fmt.Sprintf("Close all: %d descendant(s), then %s", len(kids), short)})
	}
	opts = append(opts, option{"force", "Force-close only " + short + " (--force)"}, option{"cancel", "Cancel"})
	a.modal = newPicker(short+" has "+strings.Join(why, " and "), opts, func(choice string) tea.Cmd {
		switch choice {
		case "cascade":
			ask(append(kids, id), len(blockers) > 0)
		case "force":
			ask([]string{id}, true)
		}
		return nil
	})
}

type wlKeysMsg struct {
	m    *modal
	opts []option
}

// openRefs opens the only ref directly, or asks which one when there are several.
func (a *App) openRefs(is *bd.Issue) tea.Cmd {
	refs := is.Refs()
	switch len(refs) {
	case 0:
		return flash("no refs on " + is.ID)
	case 1:
		return openRef(refs[0])
	}
	opts := make([]option, len(refs))
	for i, r := range refs {
		label := filepath.Join(r.Repo, r.File)
		if r.Line > 0 {
			label += ":" + strconv.Itoa(r.Line)
		}
		opts[i] = option{strconv.Itoa(i), label + "  " + r.Symbol}
	}
	a.modal = newPicker("Open ref", opts, func(v string) tea.Cmd {
		i, _ := strconv.Atoi(v)
		return openRef(refs[i])
	})
	return nil
}

// appendNote mirrors bd note: the new note goes on its own line after the existing ones.
func appendNote(notes, n string) string {
	if strings.TrimSpace(notes) == "" {
		return n
	}
	return strings.TrimRight(notes, "\n") + "\n" + n
}
