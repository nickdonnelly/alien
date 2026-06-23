package main

// Interactive checklist for `alien scan -i`: toggle suggestions on/off and
// install the selected ones as user aliases. Same interaction model as the
// chain picker (space toggles, enter commits, q cancels).

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type scanItem struct {
	sug      scanSuggestion
	selected bool
}

type scanModel struct {
	items     []scanItem
	cursor    int
	width     int
	height    int
	cancelled bool
}

func runScanTUI(suggestions []scanSuggestion) {
	items := make([]scanItem, len(suggestions))
	for i, sg := range suggestions {
		items[i] = scanItem{sug: sg}
	}
	m := &scanModel{items: items, width: 100, height: 28}
	prog := tea.NewProgram(m, tea.WithAltScreen())
	final, err := prog.Run()
	if err != nil {
		errorf("tui: %v", err)
		os.Exit(1)
	}
	mm := final.(*scanModel)
	if mm.cancelled {
		infof("scan cancelled")
		return
	}
	picked := []scanSuggestion{}
	for _, it := range mm.items {
		if it.selected {
			picked = append(picked, it.sug)
		}
	}
	if len(picked) == 0 {
		warnf("nothing selected")
		return
	}

	added := 0
	if err := updateStore(func(s *Store) error {
		for _, sg := range picked {
			if _, exists := s.Aliases[sg.Name]; exists {
				warnf("skipping %s — name was taken since the scan", bold(sg.Name))
				continue
			}
			s.Aliases[sg.Name] = Alias{
				Command:   sg.Command,
				Comment:   fmt.Sprintf("from alien scan (×%d in history)", sg.Count),
				Enabled:   true,
				CreatedAt: time.Now(),
			}
			added++
		}
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("added %d alias(es) from history", added)
	for _, sg := range picked {
		fmt.Fprintf(os.Stderr, "  %s %s %s\n", bold(brcyan(sg.Name)), dim("→"), sg.Command)
	}
}

func (m *scanModel) Init() tea.Cmd { return nil }

func (m *scanModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = len(m.items) - 1
		case " ", "x":
			m.items[m.cursor].selected = !m.items[m.cursor].selected
		case "a":
			all := true
			for _, it := range m.items {
				if !it.selected {
					all = false
					break
				}
			}
			for i := range m.items {
				m.items[i].selected = !all
			}
		}
	}
	return m, nil
}

func (m *scanModel) View() string {
	header := tStyleAccent.Render("👽 alien › scan") + " " +
		tStyleDim.Render("— pick suggestions to install as aliases")

	rows := m.height - 6
	if rows < 4 {
		rows = 4
	}
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	end := start + rows
	if end > len(m.items) {
		end = len(m.items)
	}

	maxName := 0
	for _, it := range m.items {
		if len(it.sug.Name) > maxName {
			maxName = len(it.sug.Name)
		}
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		it := m.items[i]
		mark := "[ ]"
		if it.selected {
			mark = tStyleOK.Render("[✓]")
		}
		row := fmt.Sprintf("%s %s  %s  %s", mark,
			padRight(it.sug.Name, maxName),
			tStyleDim.Render(fmt.Sprintf("×%-4d", it.sug.Count)),
			truncate(it.sug.Command, m.width-maxName-16))
		if i == m.cursor {
			b.WriteString(tStyleSelected.Render("› " + row))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteString("\n")
	}

	footer := tStyleDim.Render("space:toggle  a:all  enter:install  q:quit")
	return lipgloss.JoinVertical(lipgloss.Left, header+"\n", b.String(), footer)
}
