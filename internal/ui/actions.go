package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
)

var issueTypes = []string{"task", "bug", "feature", "chore", "epic", "decision", "spike"}

// targets are the marked issues, or the selected one when nothing is marked.
func (a *App) targets() []*bd.Issue {
	var out []*bd.Issue
	for id := range a.marks {
		if is := a.snap.ByID[id]; is != nil {
			out = append(out, is)
		}
	}
	if len(out) == 0 {
		if is := a.selected(); is != nil {
			out = append(out, is)
		}
	}
	sort.Slice(out, func(i, j int) bool { return bd.IDLess(out[i].ID, out[j].ID) })
	return out
}

func ids(is []*bd.Issue) []string {
	out := make([]string, len(is))
	for i, x := range is {
		out[i] = x.ID
	}
	return out
}

// label names the targets in prompts and flashes: one id, or a count.
func (a *App) label(ts []*bd.Issue) string {
	if len(ts) == 1 {
		return a.shortID(ts[0].ID)
	}
	return fmt.Sprintf("%d issues", len(ts))
}

// update runs one bd update over every target, applies f optimistically and clears the marks.
func (a *App) update(ts []*bd.Issue, desc string, f func(*bd.Issue), flags ...string) tea.Cmd {
	a.marks = map[string]bool{}
	mutate := func() {
		for _, t := range ts {
			if f != nil {
				a.mut(t.ID, f)()
			}
		}
	}
	return a.write(desc, mutate, append(append([]string{"update"}, ids(ts)...), flags...)...)
}

// action handles the one-key mutations that work on the marked issues or the selection, from any view.
func (a *App) action(k string) (tea.Cmd, bool) {
	switch k {
	case "S":
		return a.sync(), true
	case "a":
		return a.createChild(a.selected()), true
	}
	ts := a.targets()
	if len(ts) == 0 {
		return nil, false
	}
	is, id, who := ts[0], ts[0].ID, a.label(ts)
	switch k {
	case "s":
		opts := make([]option, 0, len(statuses))
		for _, s := range statuses {
			opts = append(opts, option{s, statusGlyph[s] + " " + s})
		}
		a.modal = newPicker("Status of "+who, opts, func(st string) tea.Cmd {
			if st == "closed" {
				a.closeFlow(ts)
				return nil
			}
			run := func() tea.Cmd {
				return a.update(ts, who+" → "+st, func(i *bd.Issue) { i.Status = st }, "--status", st)
			}
			if slices.ContainsFunc(ts, func(t *bd.Issue) bool { return t.IssueType == "epic" }) {
				a.modal = newConfirm(fmt.Sprintf("Set %s (includes an epic) to %s?", who, st), run)
				return nil
			}
			return run()
		})
	case "C":
		return a.update(ts, "claim "+who, func(i *bd.Issue) { i.Status = "in_progress" }, "--claim"), true
	case "c":
		a.closeFlow(ts)
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
		a.modal = newPicker("Priority of "+who, opts, func(v string) tea.Cmd {
			n, _ := strconv.Atoi(v)
			return a.update(ts, who+" → P"+v, func(i *bd.Issue) { i.Priority = n }, "--priority", v)
		})
	case "l":
		all := func(l string) bool {
			return !slices.ContainsFunc(ts, func(t *bd.Issue) bool { return !t.HasLabel(l) })
		}
		var opts []option
		for _, l := range a.knownLabels("") {
			mark := "  "
			if all(l) {
				mark = "✓ "
			}
			opts = append(opts, option{l, mark + l})
		}
		a.modal = newPicker("Toggle label on "+who, opts, func(l string) tea.Cmd {
			if all(l) {
				return a.update(ts, "-"+l+" on "+who, nil, "--remove-label", l)
			}
			return a.update(ts, "+"+l+" on "+who, nil, "--add-label", l)
		})
	case "m":
		var opts []option
		for _, l := range a.knownLabels("machine:") {
			opts = append(opts, option{l, strings.TrimPrefix(l, "machine:")})
		}
		a.modal = newPicker("Machine for "+who, opts, func(l string) tea.Cmd {
			if !strings.HasPrefix(l, "machine:") {
				l = "machine:" + l
			}
			flags := []string{"--add-label", l}
			seen := map[string]bool{l: true}
			for _, t := range ts {
				if old := t.LabelWithPrefix("machine:"); old != "" && !seen["machine:"+old] {
					seen["machine:"+old] = true
					flags = append(flags, "--remove-label", "machine:"+old)
				}
			}
			return a.update(ts, who+" on "+strings.TrimPrefix(l, "machine:"), nil, flags...)
		})
		a.modal.input.Placeholder = "filter or type a host"
	case "x":
		m := newPicker("External ref for "+who, []option{{"", "(clear)"}}, func(v string) tea.Cmd {
			return a.update(ts, who+" ref "+v, func(i *bd.Issue) { i.ExternalRef = v }, "--external-ref", v)
		})
		m.input.Placeholder = "filter wl keys or type a ref"
		a.modal = m
		return func() tea.Msg { return wlKeysMsg{m, wlKeys()} }, true
	case "e":
		cur := ""
		if len(ts) == 1 && is.EstimatedMinutes > 0 {
			cur = strconv.Itoa(is.EstimatedMinutes)
		}
		a.modal = newPrompt("Estimate for "+who+" (minutes, or 2h / 1h30m)", cur, func(v string) tea.Cmd {
			m, err := parseMinutes(v)
			if err != nil {
				return flashErr(err)
			}
			return a.update(ts, who+" est "+minutes(m), func(i *bd.Issue) { i.EstimatedMinutes = m }, "-e", strconv.Itoa(m))
		})
	case "d":
		a.modal = newPrompt("Defer "+who+" until (YYYY-MM-DD, +3d, empty clears)", "", func(v string) tea.Cmd {
			v, err := parseDate(v)
			if err != nil {
				return flashErr(err)
			}
			return a.update(ts, who+" deferred "+v, nil, "--defer", v)
		})
	case "y":
		s := strings.Join(ids(ts), " ")
		return tea.Batch(tea.SetClipboard(s), flash("copied "+s)), true
	case "o":
		return a.openRefs(is), true
	case "R":
		return a.resume(is), true
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

// closeFlow closes issues; bd refuses a parent with open children or a live blocker, so those cases ask first.
func (a *App) closeFlow(ts []*bd.Issue) {
	who := a.label(ts)
	targets := ids(ts)
	inSet := map[string]bool{}
	for _, id := range targets {
		inSet[id] = true
	}
	var kids []string
	var blocked bool
	for _, t := range ts {
		for _, k := range a.openDescendants(t.ID) {
			if !inSet[k] {
				inSet[k] = true
				kids = append(kids, k)
			}
		}
		blocked = blocked || len(a.snap.Blockers(t)) > 0
	}
	ask := func(closing []string, force bool) {
		label := who
		if len(closing) > len(targets) {
			label = fmt.Sprintf("%s and %d descendant(s)", who, len(closing)-len(targets))
		}
		a.modal = newPrompt("Close "+label+": reason (optional)", "", func(r string) tea.Cmd {
			args := append([]string{"close"}, closing...)
			if r != "" {
				args = append(args, "--reason", r)
			}
			if force {
				args = append(args, "--force")
			}
			a.marks = map[string]bool{}
			return a.write("close "+label, func() {
				for _, x := range closing {
					a.mut(x, func(i *bd.Issue) { i.Status = "closed" })()
				}
			}, args...)
		})
	}
	if len(kids) == 0 && !blocked {
		if slices.ContainsFunc(ts, func(t *bd.Issue) bool { return t.IssueType == "epic" }) {
			a.modal = newConfirm("Close "+who+" (includes an epic)?", func() tea.Cmd { ask(targets, false); return nil })
			return
		}
		ask(targets, false)
		return
	}
	var why []string
	if len(kids) > 0 {
		why = append(why, fmt.Sprintf("%d open descendant(s)", len(kids)))
	}
	if blocked {
		why = append(why, "open blockers")
	}
	var opts []option
	if len(kids) > 0 {
		opts = append(opts, option{"cascade", fmt.Sprintf("Close all: %d descendant(s), then %s", len(kids), who)})
	}
	opts = append(opts, option{"force", "Force-close only " + who + " (--force)"}, option{"cancel", "Cancel"})
	a.modal = newPicker(who+" has "+strings.Join(why, " and "), opts, func(choice string) tea.Cmd {
		switch choice {
		case "cascade":
			ask(append(kids, targets...), blocked)
		case "force":
			ask(targets, true)
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
