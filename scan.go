package main

// alien scan — analyse shell history and suggest aliases for the commands
// the user actually types. The pipeline is pure functions over a []string
// of history commands so every stage is unit-testable; only history
// acquisition and the final output touch the outside world.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type scanSuggestion struct {
	Name    string `json:"name"`    // proposed alias name
	Command string `json:"command"` // command (or token prefix) to alias
	Count   int    `json:"count"`   // times seen in history
	Saves   int    `json:"saves"`   // keystrokes saved per use
	Score   int    `json:"score"`   // count × saves, the ranking key
}

// ---------- history acquisition ----------

// readScanHistory returns normalized history commands, oldest first, plus
// the number of lines that couldn't be parsed (zsh metafied entries).
// Sources, in order: explicit file, piped stdin, atuin, $HISTFILE, the
// default zsh/bash history files.
func readScanHistory(file string) (cmds []string, unparseable int) {
	if file != "" {
		return parseHistFile(file)
	}
	if !isStdinTTY() {
		var lines []string
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		if len(lines) > 0 {
			return parseHistLines(lines)
		}
	}
	return readScanHistorySources()
}

// readScanHistorySources reads from atuin or history files only — never
// stdin. Doctor uses this directly so it can't block on an inherited pipe.
func readScanHistorySources() (cmds []string, unparseable int) {
	if cmds := readAtuinFull(); len(cmds) > 0 {
		return cmds, 0
	}
	candidates := []string{}
	if h := os.Getenv("HISTFILE"); h != "" {
		candidates = append(candidates, h)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".zsh_history"),
			filepath.Join(home, ".bash_history"))
	}
	for _, p := range candidates {
		if cs, un := parseHistFile(p); len(cs) > 0 {
			return cs, un
		}
	}
	return nil, 0
}

// readAtuinFull pulls full history from atuin when installed. Unlike the
// chain picker we want the whole corpus, not just the session.
func readAtuinFull() []string {
	if _, err := exec.LookPath("atuin"); err != nil {
		return nil
	}
	out, err := exec.Command("atuin", "history", "list", "--cmd-only").Output()
	if err != nil {
		return nil
	}
	var cmds []string
	for _, l := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			cmds = append(cmds, s)
		}
	}
	return cmds
}

func parseHistFile(path string) ([]string, int) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return parseHistLines(lines)
}

// parseHistLines turns raw history-file lines into commands. Handles:
//   - zsh EXTENDED_HISTORY metadata (": 1717920000:0;cmd")
//   - multiline commands (continuation lines end with a trailing backslash)
//   - bash HISTTIMEFORMAT timestamp lines ("#1717920000")
//   - zsh metafication: bytes ≥0x80 are escaped with 0x83; rather than
//     unmetafy we skip those lines and report how many (they're commands
//     with non-ASCII arguments — rarely alias material).
func parseHistLines(lines []string) (cmds []string, unparseable int) {
	var pending string
	flush := func() {
		if s := strings.TrimSpace(pending); s != "" {
			cmds = append(cmds, s)
		}
		pending = ""
	}
	for _, line := range lines {
		// zsh Meta byte is a raw 0x83 (not a UTF-8 rune) — check bytes.
		if strings.IndexByte(line, 0x83) >= 0 {
			unparseable++
			pending = ""
			continue
		}
		if pending == "" {
			// bash timestamp comment lines separate entries.
			if histTimestampRe.MatchString(line) {
				continue
			}
			line = parseHistLine(line) // strips zsh extended-history prefix
		}
		if strings.HasSuffix(line, "\\") {
			pending += strings.TrimSuffix(line, "\\") + " "
			continue
		}
		pending += line
		flush()
	}
	flush()
	return cmds, unparseable
}

var histTimestampRe = regexp.MustCompile(`^#\d+$`)

// ---------- candidate extraction ----------

var (
	envAssignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*$`)
	secretRe    = regexp.MustCompile(`(?i)(token|secret|passwd|password|api_?key)=`)
)

// normalizeScanCommand canonicalizes one history command for counting:
// whitespace collapsed, leading sudo / env assignments stripped.
func normalizeScanCommand(cmd string) string {
	tokens := strings.Fields(cmd)
	for len(tokens) > 0 && (tokens[0] == "sudo" || envAssignRe.MatchString(tokens[0])) {
		tokens = tokens[1:]
	}
	return strings.Join(tokens, " ")
}

// scanCandidates runs the analysis: count full commands AND their 2–3-token
// prefixes (so `git commit -m "..."` variants pool into `git commit -m`),
// filter out noise and already-aliased commands, then score by keystrokes
// saved. `taken` reports names that can't be used (existing aliases,
// binaries on PATH); minCount is the frequency floor.
func scanCandidates(history []string, s *Store, taken func(string) bool, minCount int) []scanSuggestion {
	aliasNames := map[string]bool{}
	aliasCmds := map[string]bool{}
	for n, a := range s.Aliases {
		aliasNames[n] = true
		aliasCmds[normalizeCommand(a.Command)] = true
	}

	counts := map[string]int{}
	for _, raw := range history {
		cmd := normalizeScanCommand(raw)
		tokens := strings.Fields(cmd)
		if len(tokens) == 0 {
			continue
		}
		if tokens[0] == "alien" || tokens[0] == "a" || aliasNames[tokens[0]] {
			continue // already an alias (or alien itself)
		}
		counts[cmd]++
		for _, k := range []int{2, 3} {
			if len(tokens) > k {
				counts[strings.Join(tokens[:k], " ")]++
			}
		}
	}

	// Subsumption: when a prefix only ever occurs as part of one longer
	// candidate (equal counts), keep the longer one — it saves more.
	drop := map[string]bool{}
	for cand, n := range counts {
		tokens := strings.Fields(cand)
		for k := 2; k < len(tokens); k++ {
			prefix := strings.Join(tokens[:k], " ")
			if counts[prefix] == n {
				drop[prefix] = true
			}
		}
	}

	type candidate struct {
		cmd string
		n   int
	}
	var cands []candidate
	for cmd, n := range counts {
		if n < minCount || drop[cmd] || len(cmd) < 8 {
			continue
		}
		if len(strings.Fields(cmd)) < 2 {
			continue // single tokens barely save anything
		}
		if aliasCmds[cmd] {
			continue // an alias for this exact command already exists
		}
		if secretRe.MatchString(cmd) {
			continue // don't echo anything credential-shaped
		}
		cands = append(cands, candidate{cmd, n})
	}
	// Assign names in deterministic priority order (best candidates get the
	// best names) and never propose the same name twice in one batch.
	sort.Slice(cands, func(i, j int) bool {
		si, sj := cands[i].n*len(cands[i].cmd), cands[j].n*len(cands[j].cmd)
		if si != sj {
			return si > sj
		}
		return cands[i].cmd < cands[j].cmd
	})
	proposed := map[string]bool{}
	var out []scanSuggestion
	for _, c := range cands {
		name := suggestAliasName(c.cmd, func(n string) bool {
			return aliasNames[n] || proposed[n] || taken(n)
		})
		if name == "" {
			continue
		}
		saves := len(c.cmd) - len(name) - 1
		if saves <= 0 {
			continue
		}
		proposed[name] = true
		out = append(out, scanSuggestion{
			Name: name, Command: c.cmd, Count: c.n, Saves: saves, Score: c.n * saves,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Command < out[j].Command
	})
	return out
}

// suggestAliasName proposes a short name for a command: the initials of its
// tokens ("git status" → "gs", "git commit -m" → "gcm"), falling back to
// first-word+initials and numbered variants until one is free. Returns ""
// when nothing reasonable is available.
func suggestAliasName(cmd string, taken func(string) bool) string {
	tokens := strings.Fields(cmd)
	var initials strings.Builder
	for _, tok := range tokens {
		r := []rune(strings.TrimLeft(tok, "-"))
		if len(r) == 0 {
			continue
		}
		c := r[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			initials.WriteRune(c | 0x20) // lowercase
		}
		if initials.Len() >= 4 {
			break
		}
	}
	base := initials.String()
	if len(base) < 2 {
		return ""
	}
	candidates := []string{base}
	if len(tokens[0]) <= 6 && validAliasName(tokens[0]+base[1:]) {
		candidates = append(candidates, tokens[0]+base[1:])
	}
	for _, c := range candidates {
		if validAliasName(c) && !taken(c) {
			return c
		}
	}
	for i := 2; i <= 9; i++ {
		c := base + strconv.Itoa(i)
		if !taken(c) {
			return c
		}
	}
	return ""
}

// nameOnPath reports whether a name would shadow a real executable
// (suggesting `gs` to a ghostscript user would be hostile).
func nameOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ---------- command ----------

func cmdScan(args []string) {
	args, wantJSON := extractBoolFlag(args, "--json")
	args, interactive := extractBoolFlag(args, "-i")
	args, interactiveLong := extractBoolFlag(args, "--interactive")
	interactive = interactive || interactiveLong
	args, fileFlag := extractFlag(args, "--file")
	args, topStr := extractFlag(args, "--top")
	_, minStr := extractFlag(args, "--min")

	top := 15
	if n, err := strconv.Atoi(topStr); err == nil && n > 0 {
		top = n
	}
	minCount := 5
	if n, err := strconv.Atoi(minStr); err == nil && n > 0 {
		minCount = n
	}

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	history, unparseable := readScanHistory(fileFlag)
	if len(history) == 0 {
		errorf("no shell history found — pass --file <histfile> or pipe history in")
		os.Exit(1)
	}

	suggestions := scanCandidates(history, s, nameOnPath, minCount)
	if len(suggestions) > top {
		suggestions = suggestions[:top]
	}

	if wantJSON {
		data, _ := json.MarshalIndent(suggestions, "", "  ")
		fmt.Println(string(data))
		return
	}
	if len(suggestions) == 0 {
		infof("no alias-worthy commands found (scanned %d history entries, min frequency %d)", len(history), minCount)
		return
	}

	if interactive {
		runScanTUI(suggestions)
		return
	}

	fmt.Println()
	fmt.Printf("  %s %s %s\n\n", brcyan("👽"), bold("alien scan"),
		dim(fmt.Sprintf("— %d history entries analysed", len(history))))
	maxName := 7 // "SUGGEST"
	for _, sg := range suggestions {
		if len(sg.Name) > maxName {
			maxName = len(sg.Name)
		}
	}
	fmt.Println(dim(fmt.Sprintf("  %s  %s  %s  %s",
		padRight("SUGGEST", maxName), "COUNT", "SAVES", "COMMAND")))
	for _, sg := range suggestions {
		fmt.Printf("  %s  %s  %s  %s\n",
			bold(brcyan(padRight(sg.Name, maxName))),
			dim(fmt.Sprintf("×%-4d", sg.Count)),
			dim(fmt.Sprintf("%-5d", sg.Saves)),
			truncate(sg.Command, 56))
	}
	fmt.Println()
	if unparseable > 0 {
		fmt.Printf("  %s\n", dim(fmt.Sprintf("(%d history entries skipped: non-ASCII zsh encoding)", unparseable)))
	}
	fmt.Printf("  %s %s %s\n\n", dim("install interactively with"),
		cyan("alien scan -i"), dim("· or one-off: alien <name> -c '<command>'"))
}
