package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// pickSkillTargets opens an interactive multi-select picker for skill
// install targets. Returns the user's selection on enter, or nil if
// cancelled (Esc / Ctrl-C / q).
func pickSkillTargets(targets []skillTarget) ([]skillTarget, error) {
	installed := map[string]bool{}
	for _, k := range installedSkillTargets() {
		installed[k] = true
	}
	m := &skillPickModel{
		targets:   targets,
		installed: installed,
		selected:  make([]bool, len(targets)),
		width:     80, height: 24,
	}
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, err
	}
	mm := final.(*skillPickModel)
	if mm.cancelled {
		return nil, nil
	}
	out := []skillTarget{}
	for i, t := range mm.targets {
		if mm.selected[i] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

type skillPickModel struct {
	targets   []skillTarget
	installed map[string]bool
	selected  []bool
	cursor    int
	cancelled bool
	width     int
	height    int
}

func (m *skillPickModel) Init() tea.Cmd { return nil }

func (m *skillPickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.targets)-1 {
				m.cursor++
			}
		case " ", "x":
			m.selected[m.cursor] = !m.selected[m.cursor]
		case "a":
			for i := range m.selected {
				m.selected[i] = true
			}
		case "n":
			for i := range m.selected {
				m.selected[i] = false
			}
		case "i":
			// Toggle "all currently-installed" — handy when re-installing
			// after an alien update.
			for i, t := range m.targets {
				m.selected[i] = m.installed[t.Key]
			}
		}
	}
	return m, nil
}

var (
	skStyleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	skStyleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	skStyleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	skStyleSel    = lipgloss.NewStyle().Background(lipgloss.Color("236")).Bold(true)
)

func (m *skillPickModel) View() string {
	var b strings.Builder
	b.WriteString(skStyleAccent.Render("👽 alien skill install") + "\n")
	b.WriteString(skStyleDim.Render("Pick which agent tools should learn about your aliases.") + "\n\n")

	maxKey := 0
	for _, t := range m.targets {
		if len(t.Key) > maxKey {
			maxKey = len(t.Key)
		}
	}

	for i, t := range m.targets {
		mark := "[ ]"
		if m.selected[i] {
			mark = skStyleOK.Render("[x]")
		}
		installed := ""
		if m.installed[t.Key] {
			installed = " " + skStyleOK.Render("●") + skStyleDim.Render(" installed")
		}
		row := fmt.Sprintf("%s  %s  %s%s",
			mark,
			skStyleAccent.Render(padRight(t.Key, maxKey)),
			t.Label,
			installed,
		)
		if i == m.cursor {
			b.WriteString(skStyleSel.Render("› "+row) + "\n")
			b.WriteString(skStyleDim.Render("    "+t.Path) + "\n")
			if t.Comment != "" {
				b.WriteString(skStyleDim.Render("    "+t.Comment) + "\n")
			}
		} else {
			b.WriteString("  " + row + "\n")
		}
	}

	b.WriteString("\n")
	hints := []string{
		"space:toggle",
		"a:all",
		"n:none",
		"i:installed",
		"enter:install",
		"esc:cancel",
	}
	b.WriteString(skStyleDim.Render(strings.Join(hints, "  ")))
	count := 0
	for _, sel := range m.selected {
		if sel {
			count++
		}
	}
	if count > 0 {
		b.WriteString("    " + skStyleAccent.Render(fmt.Sprintf("%d selected", count)))
	}
	return b.String()
}
