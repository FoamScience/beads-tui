package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/FoamScience/beads-tui/internal/bd"
	"github.com/charmbracelet/x/ansi"
)

type memDataMsg struct {
	mems map[string]string
	err  error
	desc string
}

// memoriesView lists the bd remember store: keys on the left, the selected memory wrapped on the right.
type memoriesView struct {
	mems    map[string]string
	keys    []string
	filter  string
	cursor  int
	offset  int
	loading bool
	loaded  bool
}

// memMachine is the host a memory is tagged for: keys "@<host>:<rest>" belong to one machine, others to all.
func memMachine(key string) (host, rest string) {
	if h, r, ok := strings.Cut(strings.TrimPrefix(key, "@"), ":"); ok && strings.HasPrefix(key, "@") {
		return h, r
	}
	return "", key
}

// memTags splits a key into its machine, its scope (a repo or "global", "" when unprefixed) and the slug.
func memTags(key string) (host, scope, slug string) {
	host, slug = memMachine(key)
	if sc, sl, ok := strings.Cut(slug, ":"); ok {
		scope, slug = sc, sl
	}
	return host, scope, slug
}

// memChips renders a key's machine and scope as labels.
func memChips(a *App, key string) string {
	host, scope, _ := memTags(key)
	var ls []string
	if scope != "" {
		ls = append(ls, scope)
	}
	if host != "" {
		ls = append(ls, "@"+strings.TrimPrefix(host, a.user+"-"))
	}
	return labelChips(ls)
}

var slugWord = regexp.MustCompile(`[a-z0-9]+`)

// memKey tags content for host with a key shaped like bd's own (first 8 words), avoiding existing keys.
func (v *memoriesView) memKey(host, content string) string {
	w := slugWord.FindAllString(strings.ToLower(content), 8)
	base := "@" + host + ":" + strings.Join(w, "-")
	key := base
	for i := 2; v.mems[key] != ""; i++ {
		key = fmt.Sprintf("%s-%d", base, i)
	}
	return key
}

func newMemories() *memoriesView { return &memoriesView{mems: map[string]string{}} }

func (v *memoriesView) Name() string { return "Memories" }

// Activate loads memories the first time the tab opens.
func (v *memoriesView) Activate(a *App) tea.Cmd {
	if v.loaded || v.loading {
		return nil
	}
	return v.load(a, "", nil)
}

// load runs an optional bd write, then reads the memories back.
func (v *memoriesView) load(a *App, desc string, write []string) tea.Cmd {
	v.loading = true
	c := a.client
	return func() tea.Msg {
		if write != nil {
			if err := c.Exec(write...); err != nil {
				return memDataMsg{err: err}
			}
		}
		m, err := c.Memories()
		return memDataMsg{mems: m, err: err, desc: desc}
	}
}

func (v *memoriesView) Rebuild(a *App) {
	keep := v.current()
	q := strings.ToLower(v.filter)
	v.keys = v.keys[:0]
	f := a.machineFilter()
	for k, c := range v.mems {
		if m, _ := memMachine(k); f != allMachines && m != "" && m != f {
			continue
		}
		if q == "" || strings.Contains(strings.ToLower(k+" "+c), q) {
			v.keys = append(v.keys, k)
		}
	}
	sort.Strings(v.keys)
	v.cursor = clamp(v.cursor, 0, max(len(v.keys)-1, 0))
	for i, k := range v.keys {
		if k == keep {
			v.cursor = i
		}
	}
}

func (v *memoriesView) current() string {
	if v.cursor < 0 || v.cursor >= len(v.keys) {
		return ""
	}
	return v.keys[v.cursor]
}

func (v *memoriesView) Selected() *bd.Issue { return nil }

func (v *memoriesView) Msg(a *App, msg tea.Msg) tea.Cmd {
	m, ok := msg.(memDataMsg)
	if !ok {
		return nil
	}
	v.loading = false
	if m.err != nil {
		return flashErr(m.err)
	}
	v.loaded, v.mems = true, m.mems
	v.Rebuild(a)
	if m.desc != "" {
		return flash(m.desc)
	}
	return nil
}

func (v *memoriesView) Update(a *App, k tea.KeyPressMsg) (bool, tea.Cmd) {
	key := v.current()
	switch k.String() {
	case "j", "down":
		v.cursor = min(v.cursor+1, max(len(v.keys)-1, 0))
	case "k", "up":
		v.cursor = max(v.cursor-1, 0)
	case "home":
		v.cursor = 0
	case "G", "end":
		v.cursor = max(len(v.keys)-1, 0)
	case "f":
		a.modal = newPrompt("Filter memories (key or content; empty clears)", v.filter, func(s string) tea.Cmd {
			v.filter = s
			v.Rebuild(a)
			return nil
		})
	case "a":
		a.modal = newPrompt("Remember (bd remember)", "", func(s string) tea.Cmd {
			if s = strings.TrimSpace(s); s == "" {
				return flash("empty memory, nothing stored")
			}
			return v.load(a, "remembered", []string{"remember", "--", s})
		})
	case "A":
		host := a.host
		a.modal = newPrompt("Remember for this machine ("+strings.TrimPrefix(host, a.user+"-")+")", "", func(s string) tea.Cmd {
			if s = strings.TrimSpace(s); s == "" {
				return flash("empty memory, nothing stored")
			}
			key := v.memKey(host, s)
			return v.load(a, "remembered "+key, []string{"remember", "--key", key, "--", s})
		})
	case "D":
		if key == "" {
			return true, nil
		}
		a.modal = newConfirm("Forget "+key+"?", func() tea.Cmd {
			return v.load(a, "forgot "+key, []string{"forget", key})
		})
	case "y":
		if key == "" {
			return true, nil
		}
		return true, tea.Batch(tea.SetClipboard(key), flash("copied "+key))
	case "r":
		return true, tea.Batch(v.load(a, "", nil), a.load())
	default:
		return false, nil
	}
	return true, nil
}

func (v *memoriesView) Render(a *App, w, h int) string {
	if !v.loaded {
		return sDim.Render("\n  loading memories…")
	}
	f := v.filter
	if f == "" {
		f = sDim.Render("none")
	}
	bar := ansi.Truncate(fmt.Sprintf(" %s %s   %s %s   %s", sDim.Render("filter"), f, sDim.Render("machine"), machineHint(a), sDim.Render(fmt.Sprintf("%d of %d memories", len(v.keys), len(v.mems)))), w, "…")
	h -= 2
	if len(v.keys) == 0 {
		return bar + "\n\n" + sDim.Render("  no memories, a remembers one")
	}
	lw := min(max(w/3, 28), 50)
	right := ansi.Wrap(v.mems[v.current()], max(w-lw-3, 10), " ")
	if chips := memChips(a, v.current()); chips != "" {
		right = ansi.Truncate(chips, max(w-lw-3, 10), "…") + "\n\n" + right
	}
	sep := strings.TrimRight(strings.Repeat(sRule.Render("│")+"\n", h), "\n")
	return bar + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(lw).Height(h).MaxHeight(h).Render(v.renderList(lw, h)), " ", sep, " ",
		lipgloss.NewStyle().Height(h).MaxHeight(h).Render(right))
}

func (v *memoriesView) renderList(w, h int) string {
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+h {
		v.offset = v.cursor - h + 1
	}
	var lines []string
	for i := v.offset; i < min(len(v.keys), v.offset+h); i++ {
		_, _, slug := memTags(v.keys[i])
		line := "  " + ansi.Truncate(slug, w-2, "…")
		if i == v.cursor {
			line = sAccent.Render("▸ ") + sSel.Render(ansi.Truncate(slug, w-2, "…"))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (v *memoriesView) Hints() []string {
	return []string{"f filter", "a remember", "A for this machine", "M machine", "D forget", "y yank key", "r reload"}
}
