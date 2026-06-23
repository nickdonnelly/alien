package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Accept the standard identifier shape OR a run of two-plus dots (so the
// classic `..`, `...` navigation aliases work). Anything weirder than that
// is rejected; shells nominally allow more but we don't want to encourage
// names that fight with the rest of the toolchain.
var nameRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_\-]*|\.{2,})$`)

func validAliasName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	return nameRe.MatchString(name)
}

// sourceCell formats the right-most "FROM" column based on a stored alias's
// Source and From fields. The column is intentionally compact — just enough
// to tell the user what sets this entry apart from a hand-typed one.
//
//	user        ""                       -> ""
//	shell       From=".zshrc"            -> ".zshrc"
//	shell       From="omz:git"           -> "omz:git"
//	shell       From=""                  -> "shell"
//	pack:docker (any)                    -> "🛸 docker"
func sourceCell(src, from string) string {
	switch {
	case src == "":
		return ""
	case src == "shell":
		if from == "" {
			return gray("shell")
		}
		return gray(from)
	case strings.HasPrefix(src, "pack:"):
		return cyan("🛸 " + strings.TrimPrefix(src, "pack:"))
	default:
		return dim(src)
	}
}

// sourceBadge wraps sourceCell for callers that don't have the From field
// handy (like guardManaged's diagnostics). Falls back to source-only.
func sourceBadge(src string) string { return sourceCell(src, "") }

// guardManaged blocks destructive operations on aliases whose owner is not
// the user. Shell-imported entries live in the user's rc; alien refuses to
// touch them and points to `promote`. Pack-installed entries are owned by
// `alien ufo uninstall <pack>` (although individual edits are permitted —
// they just lose pack-uninstall hygiene, which we warn about).
func guardManaged(name, source, action string) {
	switch {
	case source == "shell":
		errorf("%s is defined in your shell config — cannot %s here", bold(name), action)
		fmt.Fprintf(os.Stderr, "  edit it in your rc, or %s to take ownership\n",
			cyan("alien promote "+name))
		os.Exit(1)
	case strings.HasPrefix(source, "pack:"):
		warnf("%s came from %s; %s will detach it from the pack", bold(name), source, action)
	}
}

// ---------- add ----------

func cmdAdd(args []string, prevCmd string) {
	var name, command, comment string
	var force bool
	positional := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c", "--cmd", "--command":
			if i+1 < len(args) {
				command = args[i+1]
				i++
			}
		case "-m", "--comment":
			if i+1 < len(args) {
				comment = args[i+1]
				i++
			}
		case "-f", "--force":
			force = true
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) == 0 {
		errorf("alias name required")
		os.Exit(1)
	}
	name = positional[0]
	if !validAliasName(name) {
		errorf("invalid alias name %q (allowed: letters, digits, _ and -; must start with letter or _)", name)
		os.Exit(1)
	}
	if command == "" {
		command = strings.TrimSpace(prevCmd)
	}
	// Resolve line continuations / embedded newlines into a clean single-line
	// command before anything stores or exports it (see sanitizeCommand).
	command = sanitizeCommand(command)
	if command == "" {
		errorf("no command to alias — run a command first, then `alien %s`, or use --cmd", name)
		os.Exit(1)
	}
	// Refuse to alias alien itself; that would create unhelpful loops.
	trimmed := strings.TrimSpace(command)
	if trimmed == "alien" || strings.HasPrefix(trimmed, "alien ") {
		errorf("refusing to create an alias whose command starts with `alien` (would loop)")
		os.Exit(1)
	}
	if err := validateCommandText(command); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}

	if err := updateStore(func(s *Store) error {
		if existing, ok := s.Aliases[name]; ok && !force {
			errorf("alias %s already exists → %s", bold(name), existing.Command)
			fmt.Fprintf(os.Stderr, "  use %s to overwrite, or %s to modify\n",
				cyan("alien add "+name+" --cmd '...' --force"), cyan("alien edit "+name))
			os.Exit(1)
		}
		s.Aliases[name] = Alias{
			Command:   command,
			Comment:   comment,
			Enabled:   true,
			CreatedAt: time.Now(),
		}
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("aliased %s %s %s", bold(brcyan(name)), dim("→"), command)
	if comment != "" {
		fmt.Fprintf(os.Stderr, "  %s %s\n", gray("#"), gray(comment))
	}
}

// ---------- list / show ----------

func cmdList(args []string) {
	// Agent-friendly flags. `--json` and `--enabled-only` are bare; `--tag`
	// takes a value. extractFlag consumes a value (it'd treat a bare flag's
	// next token as the value), so strip bares first via extractBool.
	args, wantJSON := extractBoolFlag(args, "--json")
	args, enabledOnly := extractBoolFlag(args, "--enabled-only")
	_, tagFilter := extractFlag(args, "--tag")

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}

	if wantJSON {
		emitListJSON(s, tagFilter, enabledOnly)
		return
	}

	if len(s.Aliases) == 0 {
		fmt.Fprintf(os.Stderr, "%s no aliases yet. Run a command, then `alien <name>`.\n", brcyan("👽"))
		return
	}
	names := s.sortedNames()
	maxName := 0
	maxCmd := 0
	for _, n := range names {
		if len(n) > maxName {
			maxName = len(n)
		}
		if l := len(s.Aliases[n].Command); l > maxCmd {
			maxCmd = l
		}
	}
	if maxCmd > 60 {
		maxCmd = 60
	}

	header := fmt.Sprintf("  %s  %s  %s",
		padRight("NAME", maxName),
		padRight("COMMAND", maxCmd),
		"COMMENT")
	fmt.Println(dim(header))

	for _, n := range names {
		a := s.Aliases[n]
		cmd := truncate(a.Command, maxCmd)
		statusGlyph := green("●")
		nameStr := bold(brcyan(padRight(n, maxName)))
		cmdStr := padRight(cmd, maxCmd)
		if !a.Enabled {
			statusGlyph = gray("○")
			nameStr = dim(padRight(n, maxName))
			cmdStr = dim(cmdStr)
		}
		badge := sourceBadge(a.Source)
		comment := strings.TrimSpace(a.Comment)
		tail := ""
		switch {
		case badge != "" && comment != "":
			tail = badge + "  " + gray(comment)
		case badge != "":
			tail = badge
		case comment != "":
			tail = gray(comment)
		}
		line := fmt.Sprintf("%s %s  %s  %s", statusGlyph, nameStr, cmdStr, tail)
		fmt.Println(line)
	}
	u, p, sh := countSources(s)
	fmt.Fprintf(os.Stderr, "\n%s %d aliases  %s\n",
		dim("alien:"), len(names), sourceSummary(u, p, sh, countEnabled(s)))
}

func countEnabled(s *Store) int {
	n := 0
	for _, a := range s.Aliases {
		if a.Enabled {
			n++
		}
	}
	return n
}

// countSources returns (user, pack, shell) counts.
func countSources(s *Store) (user, pack, shell int) {
	for _, a := range s.Aliases {
		switch {
		case a.Source == "":
			user++
		case strings.HasPrefix(a.Source, "pack:"):
			pack++
		case a.Source == "shell":
			shell++
		}
	}
	return
}

func sourceSummary(user, pack, shell, active int) string {
	parts := []string{}
	if user > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", user, "user"))
	}
	if pack > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", pack, "pack"))
	}
	if shell > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", shell, "shell"))
	}
	body := strings.Join(parts, " · ")
	return dim(fmt.Sprintf("(%s · %d active)", body, active))
}

func cmdShow(args []string) {
	if len(args) < 1 {
		errorf("usage: alien show <name>")
		os.Exit(1)
	}
	name := args[0]
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	a, ok := s.Aliases[name]
	if !ok {
		errorf("no alias named %s", bold(name))
		os.Exit(1)
	}
	status := green("enabled")
	if !a.Enabled {
		status = yellow("disabled")
	}
	src := "user"
	if a.Source != "" {
		src = a.Source
	}
	fmt.Println()
	fmt.Printf("  %s %s\n", brcyan("👽"), bold(brcyan(name)))
	fmt.Printf("  %s %s\n", gray("command :"), a.Command)
	if a.Comment != "" {
		fmt.Printf("  %s %s\n", gray("comment :"), a.Comment)
	}
	fmt.Printf("  %s %s\n", gray("status  :"), status)
	fmt.Printf("  %s %s\n", gray("source  :"), src)
	if a.From != "" {
		fmt.Printf("  %s %s\n", gray("from    :"), a.From)
	}
	if uses := usageCounts()[name]; uses > 0 {
		fmt.Printf("  %s %d\n", gray("uses    :"), uses)
	}
	fmt.Printf("  %s %s\n", gray("created :"), a.CreatedAt.Local().Format("2006-01-02 15:04"))
	fmt.Println()
}

// ---------- fzf format ----------

// matchesTab returns whether an alias belongs in the named "tab" view.
// Tabs the picker can show:
//   - "" / "all":      everything
//   - "user":          Source == ""
//   - "shell":         Source == "shell"
//   - "pack:<name>":   that exact pack
func matchesTab(a Alias, tab string) bool {
	switch tab {
	case "", "all":
		return true
	case "user":
		return a.Source == ""
	case "shell":
		return a.Source == "shell"
	default:
		if strings.HasPrefix(tab, "pack:") {
			return a.Source == tab
		}
		return true
	}
}

// cmdFzfList prints fzf-ready output. The first three lines are the header
// (column labels, tab strip, key hints) — fzf is invoked with
// --header-lines=3 so they're shown above the list and excluded from the
// selectable items. Header lines deliberately contain no tab character so
// fzf renders them verbatim instead of trying to apply --with-nth.
//
// Lines 4+ are the data, formatted as `NAME\tDISPLAY`. The shell extracts
// the alias name from column 1 of the user's choice.
//
// Accepts --filter <tab> to scope to one of the tabs above.
func cmdFzfList(args []string) {
	_, filter := extractFlag(args, "--filter")

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	all := s.sortedNames()
	names := make([]string, 0, len(all))
	for _, n := range all {
		if matchesTab(s.Aliases[n], filter) {
			names = append(names, n)
		}
	}

	maxName, maxCmd, maxFrom := columnWidths(s, names)
	// Print the three header lines first. Each is prefixed with a tab so
	// fzf's --with-nth=2 --delimiter=\t treats column 1 as a hidden field
	// (matching the data-row format) and renders the visible content from
	// column 2. Without this, header rows would show as empty in fzf
	// because column 2 of an unsplit line is empty.
	for _, line := range strings.Split(buildPickerHeader(s, filter, maxName, maxCmd, maxFrom), "\n") {
		fmt.Printf("\t%s\n", line)
	}

	for _, n := range names {
		a := s.Aliases[n]
		dot := green("●")
		nameCol := bold(brcyan(padRight(n, maxName)))
		cmdCol := padRight(truncate(a.Command, maxCmd), maxCmd)
		if !a.Enabled {
			dot = gray("○")
			nameCol = dim(padRight(n, maxName))
			cmdCol = dim(cmdCol)
		}
		fromCol := sourceCell(a.Source, a.From)
		fromCol = padRightVisible(fromCol, maxFrom)
		comment := ""
		if c := strings.TrimSpace(a.Comment); c != "" {
			comment = "  " + gray("# "+c)
		}
		display := fmt.Sprintf("%s %s  %s  %s%s", dot, nameCol, cmdCol, fromCol, comment)
		// First field is the raw name (no ANSI) — shell uses it for selection.
		fmt.Printf("%s\t%s\n", n, display)
	}
}

func columnWidths(s *Store, names []string) (nameW, cmdW, fromW int) {
	for _, n := range names {
		if len(n) > nameW {
			nameW = len(n)
		}
		a := s.Aliases[n]
		if l := len(a.Command); l > cmdW {
			cmdW = l
		}
		if l := visibleLen(sourceCell(a.Source, a.From)); l > fromW {
			fromW = l
		}
	}
	if cmdW > 50 {
		cmdW = 50
	}
	if nameW < 4 {
		nameW = 4 // matches "NAME" width
	}
	return
}

// visibleLen returns the rendered length of s, ignoring ANSI escape sequences.
// Good enough for column padding; doesn't handle wide CJK glyphs.
func visibleLen(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' || r == 'K' {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			n++
		}
	}
	return n
}

// padRightVisible pads s to width w using its visible length (skipping ANSI).
func padRightVisible(s string, w int) string {
	gap := w - visibleLen(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// buildPickerHeader composes three lines: column labels (aligned with the
// data widths), the tab strip with the active tab marked, and the keybind
// hints. The data widths come from cmdFzfList so the labels line up with
// the underlying columns even as content changes.
func buildPickerHeader(s *Store, currentTab string, nameW, cmdW, fromW int) string {
	if currentTab == "" {
		currentTab = "all"
	}
	if nameW < 5 {
		nameW = 5 // matches "ALIAS"
	}
	if cmdW < 7 {
		cmdW = 7 // matches "COMMAND"
	}
	if fromW < 4 {
		fromW = 4 // matches "FROM"
	}
	// Column labels: leading "  " accounts for the status glyph + space the
	// data rows print; remaining columns mirror the data row spacing.
	cols := dim(fmt.Sprintf("  %s  %s  %s",
		padRight("ALIAS", nameW),
		padRight("COMMAND", cmdW),
		padRight("FROM", fromW)))

	// Tab strip.
	tabs := availableTabs(s)
	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		label := tabLabel(t)
		if t == currentTab {
			parts = append(parts, brcyan("❯ "+label))
		} else {
			parts = append(parts, gray(label))
		}
	}
	tabLine := "  " + strings.Join(parts, "  ")

	hints := dim("  enter:run · tab:insert · ctrl-e:edit · ctrl-d:delete · [/]:tabs · esc:cancel")
	return cols + "\n" + tabLine + "\n" + hints
}

// availableTabs lists the tabs the user can cycle through, in display order.
// "all" always first; pack tabs sorted alphabetically after the fixed ones.
func availableTabs(s *Store) []string {
	out := []string{"all", "user", "shell"}
	if s == nil {
		return out
	}
	packs := make([]string, 0, len(s.Packs))
	for n := range s.Packs {
		packs = append(packs, n)
	}
	sortStrings(packs)
	for _, n := range packs {
		out = append(out, "pack:"+n)
	}
	return out
}

// sortStrings is a tiny stdlib indirection so commands.go doesn't need to
// import "sort" just for this; reuse the existing sort import if present.
func sortStrings(ss []string) {
	// Insertion sort — n is at most a few dozen pack names.
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}

func tabLabel(t string) string {
	switch t {
	case "all", "user", "shell":
		return t
	default:
		if strings.HasPrefix(t, "pack:") {
			return "🛸 " + strings.TrimPrefix(t, "pack:")
		}
		return t
	}
}

// extractBoolFlag strips a bare flag (e.g. `--json`) from args and reports
// whether it was present. Use this for boolean flags; use extractFlag for
// `--flag VALUE` flags. Mixing them on a single args slice requires
// stripping the bare ones first, otherwise extractFlag will swallow a
// neighboring bool flag as its "value".
func extractBoolFlag(args []string, flag string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == flag {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

// hasFlag is kept as a non-mutating presence check.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// listEntry is the wire shape for `alien ls --json`. All fields always
// present (no omitempty) so agents can `jq '.[].used_count'` without
// branching on nulls.
type listEntry struct {
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	Comment   string    `json:"comment"`
	Source    string    `json:"source"`
	From      string    `json:"from"`
	Tags      []string  `json:"tags"`
	Enabled   bool      `json:"enabled"`
	UsedCount int       `json:"used_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func emitListJSON(s *Store, tagFilter string, enabledOnly bool) {
	uses := usageCounts()
	out := make([]listEntry, 0, len(s.Aliases))
	for _, n := range s.sortedNames() {
		a := s.Aliases[n]
		if enabledOnly && !a.Enabled {
			continue
		}
		if tagFilter != "" && !containsString(a.Tags, tagFilter) {
			continue
		}
		tags := a.Tags
		if tags == nil {
			tags = []string{} // [] over null for parser ergonomics
		}
		out = append(out, listEntry{
			Name:      n,
			Command:   a.Command,
			Comment:   a.Comment,
			Source:    a.Source,
			From:      a.From,
			Tags:      tags,
			Enabled:   a.Enabled,
			UsedCount: uses[n],
			CreatedAt: a.CreatedAt,
			UpdatedAt: a.UpdatedAt,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		errorf("encode json: %v", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

func containsString(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

// ---------- run / suggest (agent-friendly) ----------

// cmdRun executes an alias's stored command directly, without depending on
// the user's interactive shell. Useful for agents (each Bash tool call is a
// fresh shell, so the user's aliases are normally unavailable) and for
// scripts. Args after <name> are forwarded to the command via positional
// parameters.
//
//	alien run greet world   ->  sh -c 'echo hello $1' alien-run world
func cmdRun(args []string) {
	if len(args) < 1 {
		errorf("usage: alien run <name> [args...]")
		os.Exit(1)
	}
	name := args[0]
	rest := args[1:]

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	a, ok := s.Aliases[name]
	if !ok {
		errorf("no alias named %s", bold(name))
		os.Exit(1)
	}
	if !a.Enabled {
		errorf("%s is disabled — `alien enable %s` to re-enable", bold(name), name)
		os.Exit(1)
	}

	// `sh -c CMD NAME ARGS...` makes NAME = $0 and ARGS = $1..$N inside CMD,
	// which is the standard idiom for forwarding positional args into a
	// `-c` script. We use "alien-run" as a recognizable $0 for diagnostics.
	cmdArgs := append([]string{"-c", a.Command, "alien-run"}, rest...)
	cmd := exec.Command("sh", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()

	// Track usage. Best-effort: if anything fails we warn but don't mask
	// the child's exit code. Counts live in the machine-local usage.json
	// (not the synced store) so tracking never generates sync churn.
	if err := updateUsage(func(u *UsageDB) error {
		u.record(name, 1, time.Now())
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "%s usage tracking: %v\n", dim("alien:"), err)
	}

	if runErr == nil {
		return
	}
	if exit, ok := runErr.(*exec.ExitError); ok {
		os.Exit(exit.ExitCode())
	}
	// Spawn failure (sh missing, etc.) — surface it.
	errorf("run %s: %v", bold(name), runErr)
	os.Exit(1)
}

// cmdSuggest reports any alias whose stored command matches the input, after
// normalizing whitespace on both sides. Exits 0 if at least one match was
// printed, 1 otherwise. Stays silent on no-match so it composes cleanly:
//
//	if name=$(alien suggest 'git status -sb'); then
//	  alien run "$name"
//	else
//	  git status -sb
//	fi
func cmdSuggest(args []string) {
	// `alien suggest --history` is a discoverability alias for `alien scan`.
	if rest, history := extractBoolFlag(args, "--history"); history {
		cmdScan(rest)
		return
	}
	if len(args) < 1 {
		errorf("usage: alien suggest <command...>")
		os.Exit(2)
	}
	target := normalizeCommand(strings.Join(args, " "))
	if target == "" {
		os.Exit(1)
	}
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	matches := []string{}
	for _, n := range s.sortedNames() {
		a := s.Aliases[n]
		if !a.Enabled {
			continue
		}
		if normalizeCommand(a.Command) == target {
			matches = append(matches, n)
		}
	}
	if len(matches) == 0 {
		os.Exit(1)
	}
	for _, n := range matches {
		fmt.Println(n)
	}
}

// validateCommandText rejects control characters that have no business in a
// stored command. Newlines and tabs are allowed — multi-line commands are
// legal inside single quotes. Anything else below 0x20 (and DEL) is almost
// always a paste accident, and raw escape bytes would corrupt aliases.sh
// and the picker display.
func validateCommandText(cmd string) error {
	for _, r := range cmd {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("command contains control character %q — refusing to store it", r)
		}
	}
	return nil
}

// backslashNewlineRe matches a shell line-continuation: a backslash at the
// end of a physical line, plus the newline (and any leading whitespace on the
// continued line, which the user added only for readability).
var backslashNewlineRe = regexp.MustCompile(`\\\r?\n[ \t]*`)

// hardNewlineRe matches a run of newlines (with surrounding blank space) that
// is NOT a continuation — i.e. genuinely separate statements.
var hardNewlineRe = regexp.MustCompile(`[ \t]*\r?\n[ \t\r\n]*`)

// sanitizeCommand resolves embedded newlines so the stored command is always a
// safe single physical line for `alias name='...'`. Aliases are a single-line
// shell construct: the body is re-tokenized when the alias runs, so a stray
// continuation backslash or raw newline corrupts the arguments (e.g. the
// backslash reaches the program as `-\`, or the newline splits it into two
// commands).
//
// A backslash-newline is a line continuation: the shell joins the lines with
// nothing, so we drop the backslash, the newline, and the cosmetic indent that
// followed it. Any newline that remains separates statements, so it becomes
// "; " — the canonical way to put more than one statement in an alias.
func sanitizeCommand(cmd string) string {
	cmd = backslashNewlineRe.ReplaceAllString(cmd, "")
	cmd = hardNewlineRe.ReplaceAllString(cmd, "; ")
	cmd = strings.TrimRight(cmd, "; \t")
	return strings.TrimSpace(cmd)
}

// normalizeCommand collapses runs of whitespace into a single space and
// trims leading/trailing whitespace. Suggest's match is intentionally
// strict — same tokens in the same order — but tolerant of formatting
// differences a user might apply when typing the same command twice.
func normalizeCommand(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ---------- get / export ----------

func cmdGet(args []string) {
	if len(args) < 1 {
		errorf("usage: alien get <name>")
		os.Exit(1)
	}
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	a, ok := s.Aliases[args[0]]
	if !ok {
		errorf("no alias named %s", bold(args[0]))
		os.Exit(1)
	}
	// raw stdout so callers (shell function) can capture exactly the command
	fmt.Println(a.Command)
}

func cmdExport(args []string) {
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	for _, n := range s.sortedNames() {
		a := s.Aliases[n]
		if !a.Enabled {
			continue
		}
		fmt.Printf("alias %s=%s\n", n, shellQuote(a.Command))
	}
}

// ---------- delete / toggle / enable / disable / comment ----------

func cmdDelete(args []string) {
	var force bool
	positional := []string{}
	for _, a := range args {
		if a == "-f" || a == "--force" || a == "-y" || a == "--yes" {
			force = true
		} else {
			positional = append(positional, a)
		}
	}
	if len(positional) < 1 {
		errorf("usage: alien delete <name> [-f]")
		os.Exit(1)
	}
	name := positional[0]
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	a, ok := s.Aliases[name]
	if !ok {
		errorf("no alias named %s", bold(name))
		os.Exit(1)
	}
	guardManaged(name, a.Source, "delete")
	if !force {
		fmt.Fprintf(os.Stderr, "%s delete %s %s %s? [y/N] ",
			yellow("?"), bold(brcyan(name)), dim("→"), a.Command)
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "y" && ans != "yes" {
			infof("cancelled")
			return
		}
	}
	if err := updateStore(func(s *Store) error {
		delete(s.Aliases, name)
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("removed %s", bold(name))
}

func cmdToggle(args []string) { setEnabled(args, nil) }
func cmdEnable(args []string) { v := true; setEnabled(args, &v) }
func cmdDisable(args []string) {
	v := false
	setEnabled(args, &v)
}

func setEnabled(args []string, target *bool) {
	if len(args) < 1 {
		errorf("usage: alien toggle|enable|disable <name>")
		os.Exit(1)
	}
	name := args[0]
	var finalEnabled bool
	if err := updateStore(func(s *Store) error {
		a, ok := s.Aliases[name]
		if !ok {
			errorf("no alias named %s", bold(name))
			os.Exit(1)
		}
		if a.Source == "shell" {
			errorf("can't toggle %s — it's defined in your shell config", bold(name))
			fmt.Fprintf(os.Stderr, "  remove or comment it out in your rc, or %s first\n",
				cyan("alien promote "+name))
			os.Exit(1)
		}
		if target == nil {
			a.Enabled = !a.Enabled
		} else {
			a.Enabled = *target
		}
		s.Aliases[name] = a
		finalEnabled = a.Enabled
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	if finalEnabled {
		successf("enabled %s", bold(brcyan(name)))
	} else {
		successf("disabled %s", bold(name))
	}
}

func cmdComment(args []string) {
	if len(args) < 1 {
		errorf("usage: alien comment <name> [text...]")
		os.Exit(1)
	}
	name := args[0]
	comment := strings.TrimSpace(strings.Join(args[1:], " "))
	if err := updateStore(func(s *Store) error {
		a, ok := s.Aliases[name]
		if !ok {
			errorf("no alias named %s", bold(name))
			os.Exit(1)
		}
		// Comments are alien-only metadata, so even shell-source entries
		// can carry one — it doesn't touch the user's rc. Pack-installed
		// entries can also be commented without detaching them.
		a.Comment = comment
		s.Aliases[name] = a
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	if comment == "" {
		successf("cleared comment on %s", bold(brcyan(name)))
	} else {
		successf("commented %s: %s", bold(brcyan(name)), comment)
	}
}

// ---------- edit ----------

func cmdEdit(args []string) {
	if len(args) < 1 {
		errorf("usage: alien edit <name>")
		os.Exit(1)
	}
	name := args[0]
	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	a, ok := s.Aliases[name]
	if !ok {
		errorf("no alias named %s", bold(name))
		os.Exit(1)
	}
	guardManaged(name, a.Source, "edit")

	editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")

	tmp, err := os.CreateTemp("", "alien-edit-*.conf")
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	enabledStr := "true"
	if !a.Enabled {
		enabledStr = "false"
	}
	tmpl := fmt.Sprintf(`# alien edit: %s
# Save & quit to apply. Lines starting with # are ignored.
# Rename by changing `+"`name`"+`. Set `+"`enabled`"+` to true/false.

name:     %s
command:  %s
comment:  %s
enabled:  %s
`, name, name, a.Command, a.Comment, enabledStr)
	if _, err := tmp.WriteString(tmpl); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	tmp.Close()

	// Run the editor inheriting our stdio.
	cmd := exec.Command("sh", "-c", fmt.Sprintf("%s %q", editor, tmpPath))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		errorf("editor exited: %v", err)
		os.Exit(1)
	}

	parsed, err := parseEditFile(tmpPath)
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	newName := strings.TrimSpace(parsed["name"])
	newCmd := sanitizeCommand(parsed["command"])
	newComment := strings.TrimSpace(parsed["comment"])
	newEnabledStr := strings.ToLower(strings.TrimSpace(parsed["enabled"]))

	if newName == "" || newCmd == "" {
		errorf("name and command must not be empty")
		os.Exit(1)
	}
	if !validAliasName(newName) {
		errorf("invalid alias name %q", newName)
		os.Exit(1)
	}
	if err := validateCommandText(newCmd); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}

	// Apply under the lock. We deliberately re-read inside the closure
	// rather than reusing the `s` we loaded for the editor template — the
	// editor session can be arbitrarily long, so the on-disk state may
	// have changed underneath us (e.g. a sync pull, or another alien
	// invocation). We only patch the entry the user was editing.
	enabled := newEnabledStr == "true" || newEnabledStr == "yes" || newEnabledStr == "1"
	if err := updateStore(func(s *Store) error {
		cur, ok := s.Aliases[name]
		if !ok {
			errorf("alias %s no longer exists — it was removed during editing", bold(name))
			os.Exit(1)
		}
		cur.Command = newCmd
		cur.Comment = newComment
		cur.Enabled = enabled
		if newName != name {
			if _, exists := s.Aliases[newName]; exists {
				errorf("alias %s already exists", bold(newName))
				os.Exit(1)
			}
			delete(s.Aliases, name)
			s.Aliases[newName] = cur
		} else {
			s.Aliases[name] = cur
		}
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("updated %s", bold(brcyan(newName)))
}

func parseEditFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		out[strings.ToLower(key)] = val
	}
	return out, scanner.Err()
}

// ---------- shell init ----------

func cmdInit(args []string) {
	shell := ""
	if len(args) > 0 {
		shell = args[0]
	} else {
		shell = filepath.Base(os.Getenv("SHELL"))
	}
	switch shell {
	case "zsh":
		fmt.Print(zshInit)
	case "bash":
		fmt.Print(bashInit)
	default:
		errorf("unsupported shell %q (try `alien init zsh` or `alien init bash`)", shell)
		os.Exit(1)
	}
}

// ---------- helpers ----------

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
