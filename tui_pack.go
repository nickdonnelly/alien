package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Color palette mirrors the picker.
var (
	tStyleAccent   = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)         // bright cyan
	tStyleHeader   = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	tStyleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tStyleWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))                    // yellow
	tStyleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))                     // red
	tStyleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))                    // green
	tStyleSelected = lipgloss.NewStyle().Background(lipgloss.Color("236")).Bold(true)
	tStyleBorder   = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("14"))
)

type packModel struct {
	pack  *Pack
	items []InstallDecision

	cursor int
	width  int
	height int

	renameMode  bool
	renameInput textinput.Model

	cancelled bool
	done      bool
}

// runPackTUI is the entry point invoked by `alien ufo install`. Returns the
// (possibly modified) decisions to apply, or (nil, nil) if the user aborted.
func runPackTUI(p *Pack, decisions []InstallDecision) ([]InstallDecision, error) {
	return newPackModel(p, decisions).run()
}

func newPackModel(p *Pack, decisions []InstallDecision) *packModel {
	in := textinput.New()
	in.Placeholder = "new name"
	in.CharLimit = 64
	in.Width = 40
	return &packModel{
		pack:        p,
		items:       decisions,
		renameInput: in,
		// 40x20 is a reasonable initial guess; we resize on the first WindowSizeMsg.
		width:  80,
		height: 24,
	}
}

func (m *packModel) run() ([]InstallDecision, error) {
	prog := tea.NewProgram(m, tea.WithAltScreen())
	final, err := prog.Run()
	if err != nil {
		return nil, err
	}
	mm := final.(*packModel)
	if mm.cancelled {
		return nil, nil
	}
	return mm.items, nil
}

func (m *packModel) Init() tea.Cmd { return nil }

func (m *packModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.renameMode {
			return m.updateRename(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m *packModel) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		m.cancelled = true
		m.done = true
		return m, tea.Quit
	case "enter":
		m.done = true
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
		m.items[m.cursor].Skip = !m.items[m.cursor].Skip
	case "a":
		for i := range m.items {
			m.items[i].Skip = false
		}
	case "n":
		for i := range m.items {
			m.items[i].Skip = true
		}
	case "r":
		m.enterRename()
	}
	return m, nil
}

func (m *packModel) updateRename(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.renameMode = false
		m.renameInput.Blur()
		return m, nil
	case "enter":
		newName := strings.TrimSpace(m.renameInput.Value())
		if newName == "" || !validAliasName(newName) {
			// Bell-style nudge: leave the user in rename mode with a small
			// inline marker shown via View(). We can't beep, so just stay.
			return m, nil
		}
		m.items[m.cursor].TargetName = newName
		m.items[m.cursor].Skip = false
		m.renameMode = false
		m.renameInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.renameInput, cmd = m.renameInput.Update(msg)
	return m, cmd
}

func (m *packModel) enterRename() {
	d := m.items[m.cursor]
	suggestion := d.TargetName
	if d.Conflict != NoConflict && d.TargetName == d.OriginalName {
		// Conflict + still on the original name: propose a prefixed name.
		suggestion = m.pack.UFO.Name + "-" + d.OriginalName
	}
	m.renameInput.SetValue(suggestion)
	m.renameInput.CursorEnd()
	m.renameInput.Focus()
	m.renameMode = true
}

// ---------- View ----------

func (m *packModel) View() string {
	header := m.renderHeader()
	body := m.renderBody()
	footer := m.renderFooter()
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m *packModel) renderHeader() string {
	title := tStyleAccent.Render("👽 alien ufo › install: " + m.pack.UFO.Name)
	if m.pack.UFO.Version != "" {
		title += " " + tStyleDim.Render("v"+m.pack.UFO.Version)
	}
	desc := ""
	if m.pack.UFO.Description != "" {
		desc = "\n" + tStyleDim.Render(m.pack.UFO.Description)
	}
	return title + desc + "\n"
}

func (m *packModel) renderBody() string {
	leftWidth := 28
	if m.width > 90 {
		leftWidth = 32
	}
	rightWidth := m.width - leftWidth - 6
	if rightWidth < 30 {
		rightWidth = 30
	}
	bodyHeight := m.height - 6
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	left := m.renderList(leftWidth, bodyHeight)
	right := m.renderDetail(rightWidth, bodyHeight)

	leftBox := tStyleBorder.Width(leftWidth).Height(bodyHeight).Render(left)
	rightBox := tStyleBorder.Width(rightWidth).Height(bodyHeight).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, rightBox)
}

func (m *packModel) renderList(w, h int) string {
	var b strings.Builder
	// Visible window over the items, centered on the cursor.
	rows := h
	if rows < 1 {
		rows = 1
	}
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	end := start + rows
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := start; i < end; i++ {
		d := m.items[i]
		mark := "[ ]"
		if !d.Skip {
			mark = tStyleOK.Render("[x]")
		}
		warn := "  "
		if d.Conflict != NoConflict {
			warn = tStyleWarn.Render(" ⚠")
		}
		name := d.TargetName
		if d.TargetName != d.OriginalName {
			name = d.TargetName + tStyleDim.Render(" ←"+d.OriginalName)
		}
		row := fmt.Sprintf("%s %s%s", mark, name, warn)
		if i == m.cursor {
			b.WriteString(tStyleSelected.Render("› " + row))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m *packModel) renderDetail(w, h int) string {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return ""
	}
	d := m.items[m.cursor]
	var b strings.Builder
	field := func(label, val string) {
		b.WriteString(tStyleDim.Render(label) + " " + val + "\n")
	}
	field("name    :", tStyleAccent.Render(d.OriginalName))
	field("command :", d.Entry.Command)
	if d.Entry.Comment != "" {
		field("comment :", d.Entry.Comment)
	}
	if len(d.Entry.Tags) > 0 {
		field("tags    :", strings.Join(d.Entry.Tags, ", "))
	}

	b.WriteString("\n")

	// Conflict / status block
	switch d.Conflict {
	case NoConflict:
		field("status  :", tStyleOK.Render("ok — clean install"))
	case ConflictUser:
		field("status  :", tStyleWarn.Render("conflicts with your alias"))
		if d.Existing != nil {
			field("existing:", d.Existing.Command)
		}
		field("install :", colorTarget(d.TargetName, d.OriginalName))
	case ConflictPack:
		field("status  :", tStyleWarn.Render("conflicts with another pack alias"))
		if d.Existing != nil {
			field("existing:", d.Existing.Command+" "+tStyleDim.Render("("+d.Existing.Source+")"))
		}
		field("install :", colorTarget(d.TargetName, d.OriginalName))
	case ConflictShell:
		field("status  :", tStyleWarn.Render("shadows your shell-config alias"))
		if d.Existing != nil {
			field("existing:", d.Existing.Command+" "+tStyleDim.Render("[shell]"))
		}
		field("install :", colorTarget(d.TargetName, d.OriginalName))
	}

	if m.renameMode {
		b.WriteString("\n")
		b.WriteString(tStyleDim.Render("rename to: ") + m.renameInput.View() + "\n")
		b.WriteString(tStyleDim.Render("(enter to confirm, esc to cancel)\n"))
	}

	if d.Skip {
		b.WriteString("\n")
		b.WriteString(tStyleDim.Render("× this entry is skipped\n"))
	}

	return b.String()
}

func colorTarget(target, original string) string {
	if target == original {
		return target
	}
	return tStyleAccent.Render(target) + " " + tStyleDim.Render("(renamed from "+original+")")
}

func (m *packModel) renderFooter() string {
	hints := []string{
		"space:toggle",
		"a:all",
		"n:none",
		"r:rename",
		"enter:install",
		"q:quit",
	}
	if m.renameMode {
		hints = []string{"enter:confirm", "esc:cancel"}
	}
	selected := 0
	for _, d := range m.items {
		if !d.Skip {
			selected++
		}
	}
	left := fmt.Sprintf("%d / %d selected", selected, len(m.items))
	return tStyleDim.Render(strings.Join(hints, "  ")) + "    " + tStyleAccent.Render(left)
}
