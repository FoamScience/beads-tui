package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type option struct {
	value string
	label string
}

// modal is a text prompt, a filterable picker (single or multi) or a yes/no confirm.
type modal struct {
	title    string
	input    textinput.Model
	picker   bool
	options  []option
	visible  []int
	cursor   int
	multi    bool
	checked  map[string]bool
	confirm  bool
	onSubmit func(values []string) tea.Cmd
}

func newPrompt(title, initial string, onSubmit func(string) tea.Cmd) *modal {
	m := &modal{title: title, input: newInput(initial)}
	m.onSubmit = func(v []string) tea.Cmd { return onSubmit(v[0]) }
	return m
}

func newPicker(title string, opts []option, onPick func(string) tea.Cmd) *modal {
	m := &modal{title: title, input: newInput(""), options: opts, picker: true}
	m.input.Placeholder = "filter"
	m.onSubmit = func(v []string) tea.Cmd { return onPick(v[0]) }
	m.filter()
	return m
}

func newMultiPicker(title string, opts []option, checked map[string]bool, onDone func([]string) tea.Cmd) *modal {
	m := newPicker(title, opts, nil)
	m.multi, m.checked, m.onSubmit = true, checked, onDone
	return m
}

func newConfirm(title string, onYes func() tea.Cmd) *modal {
	return &modal{title: title, confirm: true, onSubmit: func([]string) tea.Cmd { return onYes() }}
}

func newInput(v string) textinput.Model {
	ti := textinput.New()
	st := ti.Styles()
	st.Cursor.Blink = false
	ti.SetStyles(st)
	ti.Prompt = "› "
	ti.SetValue(v)
	ti.CursorEnd()
	ti.Focus()
	return ti
}

func (m *modal) filter() {
	q := strings.ToLower(m.input.Value())
	m.visible = m.visible[:0]
	for i, o := range m.options {
		if q == "" || strings.Contains(strings.ToLower(o.label+" "+o.value), q) {
			m.visible = append(m.visible, i)
		}
	}
	m.cursor = clamp(m.cursor, 0, max(len(m.visible)-1, 0))
}

// update returns done=true when the modal should close, with the cmd to run.
func (m *modal) update(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	k := msg.String()
	if m.confirm {
		switch k {
		case "y", "Y", "enter":
			return true, m.onSubmit(nil)
		case "n", "N", "esc", "q":
			return true, nil
		}
		return false, nil
	}
	switch k {
	case "esc", "ctrl+c":
		return true, nil
	case "enter":
		if !m.picker {
			return true, m.onSubmit([]string{strings.TrimSpace(m.input.Value())})
		}
		if m.multi {
			var out []string
			for _, o := range m.options {
				if m.checked[o.value] {
					out = append(out, o.value)
				}
			}
			return true, m.onSubmit(out)
		}
		if len(m.visible) == 0 {
			if v := strings.TrimSpace(m.input.Value()); v != "" {
				return true, m.onSubmit([]string{v})
			}
			return false, nil
		}
		return true, m.onSubmit([]string{m.options[m.visible[m.cursor]].value})
	}
	if m.picker {
		switch k {
		case "down", "ctrl+n", "ctrl+j":
			m.cursor = min(m.cursor+1, max(len(m.visible)-1, 0))
			return false, nil
		case "up", "ctrl+p", "ctrl+k":
			m.cursor = max(m.cursor-1, 0)
			return false, nil
		case "space", "tab":
			if m.multi && len(m.visible) > 0 {
				v := m.options[m.visible[m.cursor]].value
				m.checked[v] = !m.checked[v]
				return false, nil
			}
		}
	}
	return false, m.forward(msg)
}

// forward hands keys, pastes and cursor blinks to the text input.
func (m *modal) forward(msg tea.Msg) tea.Cmd {
	if m.confirm {
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.picker {
		m.filter()
	}
	return cmd
}

func (m *modal) view(w, h int) string {
	bw := min(max(w*2/3, 40), 90)
	inner := bw - 4
	var b strings.Builder
	b.WriteString(sBold.Render(m.title))
	b.WriteString("\n\n")
	if m.confirm {
		b.WriteString(sKey.Render("y") + " yes   " + sKey.Render("n") + " no")
	} else {
		m.input.SetWidth(inner - 3)
		b.WriteString(m.input.View())
		if m.picker {
			b.WriteString("\n")
			maxRows := max(h-12, 3)
			start := clamp(m.cursor-maxRows+1, 0, max(len(m.visible)-maxRows, 0))
			for i := start; i < min(start+maxRows, len(m.visible)); i++ {
				o := m.options[m.visible[i]]
				prefix := "  "
				if i == m.cursor {
					prefix = sAccent.Render("▸ ")
				}
				if m.multi {
					box := "[ ] "
					if m.checked[o.value] {
						box = sAccent.Render("[x] ")
					}
					prefix += box
				}
				line := ansi.Truncate(o.label, inner-lipgloss.Width(prefix), "…")
				if i == m.cursor {
					line = sSel.Render(line)
				}
				b.WriteString("\n" + prefix + line)
			}
			if len(m.visible) == 0 {
				b.WriteString("\n" + sDim.Render("  no match, enter uses the typed value"))
			}
			hint := "enter pick · esc cancel"
			if m.multi {
				hint = "space toggle · enter apply · esc cancel"
			}
			b.WriteString("\n\n" + sDim.Render(hint))
		} else {
			b.WriteString("\n\n" + sDim.Render("enter submit · esc cancel"))
		}
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(0, 1).Width(bw)
	return box.Render(b.String())
}
