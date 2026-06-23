package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------- cmdChain ----------

func cmdChain(args []string) {
	var force bool
	positional := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--force":
			force = true
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) == 0 {
		errorf("alias name required — usage: alien chain <name>")
		os.Exit(1)
	}
	name := positional[0]
	if !validAliasName(name) {
		errorf("invalid alias name %q", name)
		os.Exit(1)
	}

	hist := readHistoryLines()
	if len(hist) == 0 {
		errorf("no shell history found — run a few commands first, or invoke `alien chain` from inside a shell that has the alien hook installed")
		os.Exit(1)
	}

	picked, err := runChainTUI(hist)
	if err != nil {
		errorf("tui: %v", err)
		os.Exit(1)
	}
	if len(picked) == 0 {
		warnf("nothing selected")
		return
	}

	command := strings.Join(picked, " && ")

	err = updateStore(func(s *Store) error {
		if existing, ok := s.Aliases[name]; ok && !force {
			errorf("alias %s already exists → %s", bold(name), existing.Command)
			fmt.Fprintf(os.Stderr, "  use %s to overwrite\n",
				cyan("alien chain "+name+" --force"))
			os.Exit(1)
		}
		s.Aliases[name] = Alias{
			Command:   command,
			Enabled:   true,
			CreatedAt: time.Now(),
		}
		return nil
	})
	if err != nil {
		errorf("save: %v", err)
		os.Exit(1)
	}
	successf("aliased %s %s %s", bold(brcyan(name)), dim("→"), command)
}

// readHistoryLines pulls recent commands from stdin (preferred — the shell
// wrapper pipes raw history output) or falls back to atuin / $HISTFILE.
// Contract: every source returns oldest-first (atuin's default, fc's default,
// histfile order). We reverse once here so callers see newest-first.
func readHistoryLines() []string {
	var raw []string
	if !isStdinTTY() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			raw = append(raw, sc.Text())
		}
	}
	if len(raw) == 0 {
		raw = readAtuin()
	}
	if len(raw) == 0 {
		raw = readHistFile()
	}
	for i, j := 0, len(raw)-1; i < j; i, j = i+1, j-1 {
		raw[i], raw[j] = raw[j], raw[i]
	}
	return cleanHistory(raw)
}

func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// readAtuin shells out to atuin if it's installed. Used when alien chain is
// invoked outside the shell wrapper (e.g. directly from a TTY), so atuin
// users still get their real history. Returns newest-first.
func readAtuin() []string {
	if _, err := exec.LookPath("atuin"); err != nil {
		return nil
	}
	out, err := exec.Command("atuin", "history", "list",
		"--cmd-only", "--session").Output()
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			lines = append(lines, s)
		}
		if len(lines) >= 200 {
			break
		}
	}
	return lines
}

func readHistFile() []string {
	candidates := []string{}
	if h := os.Getenv("HISTFILE"); h != "" {
		candidates = append(candidates, h)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".zsh_history"),
			filepath.Join(home, ".bash_history"))
	}
	for _, p := range candidates {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()
		var lines []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lines = append(lines, parseHistLine(sc.Text()))
		}
		// Cap to the most recent 200 lines (file tail) but keep them in
		// natural file order (oldest-first) — readHistoryLines reverses.
		if len(lines) > 200 {
			lines = lines[len(lines)-200:]
		}
		return lines
	}
	return nil
}

// parseHistLine strips zsh extended-history metadata (": 1700000000:0;cmd")
// and trims surrounding whitespace.
func parseHistLine(s string) string {
	if strings.HasPrefix(s, ": ") {
		if idx := strings.Index(s, ";"); idx >= 0 {
			return strings.TrimSpace(s[idx+1:])
		}
	}
	return strings.TrimSpace(s)
}

func cleanHistory(in []string) []string {
	out := make([]string, 0, len(in))
	var prev string
	for _, l := range in {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if l == prev {
			continue
		}
		// Skip self-references: any `alien chain ...` or bare `alien` commands
		// that would create confusing chains.
		if strings.HasPrefix(l, "alien chain") || strings.HasPrefix(l, "a chain") {
			continue
		}
		out = append(out, l)
		prev = l
	}
	return out
}

// ---------- TUI ----------

type chainItem struct {
	cmd      string
	histIdx  int // original index in history (0 = newest)
	pickedAt int // -1 if unselected; otherwise monotonic pick counter
}

type chainModel struct {
	items   []chainItem
	cursor  int
	width   int
	height  int
	pickSeq int

	cancelled bool
}

func runChainTUI(history []string) ([]string, error) {
	items := make([]chainItem, len(history))
	for i, c := range history {
		items[i] = chainItem{cmd: c, histIdx: i, pickedAt: -1}
	}
	m := &chainModel{items: items, width: 100, height: 28}
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	// Stdin is piped (history); bubbletea needs a real terminal for keys.
	if tty, err := os.Open("/dev/tty"); err == nil {
		opts = append(opts, tea.WithInput(tty))
		defer tty.Close()
	}
	prog := tea.NewProgram(m, opts...)
	final, err := prog.Run()
	if err != nil {
		return nil, err
	}
	mm := final.(*chainModel)
	if mm.cancelled {
		return nil, nil
	}
	return mm.chainCommands(), nil
}

// chainCommands returns the picked items in pick order — i.e. the user
// controls the chain order by selecting items in the order they want them
// to run. Re-toggling an item moves it to the end of the chain.
func (m *chainModel) chainCommands() []string {
	type sel struct {
		seq int
		cmd string
	}
	var sels []sel
	for _, it := range m.items {
		if it.pickedAt >= 0 {
			sels = append(sels, sel{it.pickedAt, it.cmd})
		}
	}
	sort.Slice(sels, func(i, j int) bool { return sels[i].seq < sels[j].seq })
	out := make([]string, len(sels))
	for i, s := range sels {
		out[i] = s.cmd
	}
	return out
}

func (m *chainModel) Init() tea.Cmd { return nil }

func (m *chainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			if m.items[m.cursor].pickedAt >= 0 {
				m.items[m.cursor].pickedAt = -1
			} else {
				m.items[m.cursor].pickedAt = m.pickSeq
				m.pickSeq++
			}
		case "c":
			for i := range m.items {
				m.items[i].pickedAt = -1
			}
			m.pickSeq = 0
		}
	}
	return m, nil
}

func (m *chainModel) View() string {
	header := tStyleAccent.Render("👽 alien › chain") + " " +
		tStyleDim.Render("— pick commands from history; they'll run in original order")
	body := m.renderBody()
	footer := m.renderFooter()
	return lipgloss.JoinVertical(lipgloss.Left, header+"\n", body, footer)
}

func (m *chainModel) renderBody() string {
	leftWidth := m.width / 2
	if leftWidth < 40 {
		leftWidth = 40
	}
	rightWidth := m.width - leftWidth - 6
	if rightWidth < 30 {
		rightWidth = 30
	}
	bodyHeight := m.height - 5
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	left := m.renderList(leftWidth, bodyHeight)
	right := m.renderPreview(rightWidth, bodyHeight)

	leftBox := tStyleBorder.Width(leftWidth).Height(bodyHeight).Render(left)
	rightBox := tStyleBorder.Width(rightWidth).Height(bodyHeight).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, rightBox)
}

func (m *chainModel) renderList(w, h int) string {
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

	var b strings.Builder
	for i := start; i < end; i++ {
		it := m.items[i]
		mark := "[ ]"
		if it.pickedAt >= 0 {
			// Show position in the resulting chain (1-based by pick order).
			mark = tStyleOK.Render(fmt.Sprintf("[%d]", m.pickPosition(i)))
		}
		cmd := truncate(it.cmd, w-8)
		row := fmt.Sprintf("%s %s", mark, cmd)
		if i == m.cursor {
			b.WriteString(tStyleSelected.Render("› " + row))
		} else {
			b.WriteString("  " + row)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// pickPosition returns the 1-based position the item at index i will occupy
// in the final chained command, derived from pick order.
func (m *chainModel) pickPosition(i int) int {
	target := m.items[i].pickedAt
	pos := 1
	for _, it := range m.items {
		if it.pickedAt >= 0 && it.pickedAt < target {
			pos++
		}
	}
	return pos
}

func (m *chainModel) renderPreview(w, h int) string {
	picked := m.chainCommands()
	var b strings.Builder
	b.WriteString(tStyleDim.Render("chain preview") + "\n\n")
	if len(picked) == 0 {
		b.WriteString(tStyleDim.Render("(nothing selected — press space to pick)") + "\n")
		return b.String()
	}
	for i, c := range picked {
		prefix := tStyleAccent.Render(fmt.Sprintf("%d.", i+1))
		b.WriteString(prefix + " " + truncate(c, w-6) + "\n")
		if i < len(picked)-1 {
			b.WriteString("   " + tStyleDim.Render("&&") + "\n")
		}
	}
	b.WriteString("\n" + tStyleDim.Render("→ ") + truncate(strings.Join(picked, " && "), w-4) + "\n")
	return b.String()
}

func (m *chainModel) renderFooter() string {
	hints := []string{
		"space:toggle",
		"c:clear",
		"enter:save",
		"q:quit",
	}
	return tStyleDim.Render(strings.Join(hints, "  "))
}
