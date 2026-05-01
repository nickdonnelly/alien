package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skill/SKILL.md
var skillBody string

// installMode controls how a target's file is updated.
type installMode int

const (
	// modeOverwrite writes the file in full (creating directories as needed).
	// For files alien fully owns (SKILL.md, .mdc rule files).
	modeOverwrite installMode = iota
	// modeAppendFenced inserts our content between sentinel comments at the
	// end of an existing file, preserving anything else the user has there.
	// Re-installing replaces what's between the sentinels.
	modeAppendFenced
)

// skillTarget describes one place we know how to install the alien
// teaching content. Adding a new target is a single struct literal.
type skillTarget struct {
	Key     string // stable id, used for --target
	Label   string // shown in the picker
	Path    string // absolute path; resolved when target is registered
	Mode    installMode
	Body    func() string // returns the bytes to write / fence
	Comment string        // shown under the label in the picker
}

const (
	fenceStart = "<!-- alien:start -->"
	fenceEnd   = "<!-- alien:end -->"
)

// skillBodyAgent strips the YAML frontmatter from skillBody so it's
// suitable for plain-markdown targets (CLAUDE.md, AGENTS.md, etc.).
// Frontmatter is the leading `---\n...\n---\n` block.
func skillBodyAgent() string {
	s := skillBody
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return s
	}
	return strings.TrimLeft(rest[end+len("\n---\n"):], "\n")
}

// skillBodyCursorMDC wraps the agent body with the minimal YAML
// frontmatter Cursor's modern .cursor/rules/*.mdc format expects.
func skillBodyCursorMDC() string {
	return "---\n" +
		"description: Use the alien CLI to discover and invoke the user's shell aliases (token-saving).\n" +
		"alwaysApply: true\n" +
		"---\n\n" +
		skillBodyAgent()
}

// skillTargets enumerates every place we can install. Order matters —
// it's the order shown in the picker.
func skillTargets() []skillTarget {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	claudeUser := filepath.Join(home, ".claude", "skills", "alien", "SKILL.md")
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		claudeUser = filepath.Join(d, "claude", "skills", "alien", "SKILL.md")
	}

	return []skillTarget{
		{
			Key:     "claude-user",
			Label:   "Claude Code (user-level skill)",
			Path:    claudeUser,
			Mode:    modeOverwrite,
			Body:    func() string { return skillBody },
			Comment: "loaded by every Claude Code session",
		},
		{
			Key:     "claude-md",
			Label:   "Claude Code (project memory)",
			Path:    filepath.Join(cwd, "CLAUDE.md"),
			Mode:    modeAppendFenced,
			Body:    skillBodyAgent,
			Comment: "appended to ./CLAUDE.md for this project",
		},
		{
			Key:     "cursor",
			Label:   "Cursor (project rules)",
			Path:    filepath.Join(cwd, ".cursor", "rules", "alien.mdc"),
			Mode:    modeOverwrite,
			Body:    skillBodyCursorMDC,
			Comment: ".cursor/rules/alien.mdc",
		},
		{
			Key:     "agents-md",
			Label:   "Codex CLI / generic AGENTS.md",
			Path:    filepath.Join(cwd, "AGENTS.md"),
			Mode:    modeAppendFenced,
			Body:    skillBodyAgent,
			Comment: "appended to ./AGENTS.md",
		},
		{
			Key:     "aider",
			Label:   "Aider (CONVENTIONS.md)",
			Path:    filepath.Join(cwd, "CONVENTIONS.md"),
			Mode:    modeAppendFenced,
			Body:    skillBodyAgent,
			Comment: "appended to ./CONVENTIONS.md",
		},
	}
}

func findSkillTarget(key string) (skillTarget, bool) {
	for _, t := range skillTargets() {
		if t.Key == key {
			return t, true
		}
	}
	return skillTarget{}, false
}

// installedSkillTargets returns the labels of targets whose file currently
// exists with our content (overwrite mode) or our fenced section
// (append-fenced mode). Used by `alien doctor`.
func installedSkillTargets() []string {
	out := []string{}
	for _, t := range skillTargets() {
		data, err := os.ReadFile(t.Path)
		if err != nil {
			continue
		}
		s := string(data)
		switch t.Mode {
		case modeOverwrite:
			// Match a stable substring rather than full equality so a
			// trailing newline difference doesn't false-negative.
			if strings.Contains(s, "alien — alias-aware bash") {
				out = append(out, t.Key)
			}
		case modeAppendFenced:
			if strings.Contains(s, fenceStart) && strings.Contains(s, fenceEnd) {
				out = append(out, t.Key)
			}
		}
	}
	return out
}

// ---------- dispatch ----------

func cmdSkill(args []string) {
	if len(args) == 0 {
		// Bare `alien skill install` opens the picker; bare `alien skill`
		// shows help so users know what's available.
		printSkillHelp()
		return
	}
	switch args[0] {
	case "install":
		cmdSkillInstall(args[1:])
	case "uninstall", "remove":
		cmdSkillUninstall(args[1:])
	case "print", "show", "cat":
		cmdSkillPrint(args[1:])
	case "targets", "list":
		cmdSkillList(args[1:])
	case "path":
		cmdSkillPath(args[1:])
	case "-h", "--help", "help":
		printSkillHelp()
	default:
		errorf("unknown subcommand: alien skill %s", args[0])
		fmt.Fprintln(os.Stderr, "  available: install, uninstall, print, targets, path")
		os.Exit(1)
	}
}

func printSkillHelp() {
	fmt.Print(`alien skill — install the agent-teaching skill into your AI tools

  alien skill install [--target <key>]... [--all] [--force]
      Without flags: opens an interactive picker for selecting one or
      more targets. With --target, installs to that target only (can be
      repeated). With --all, installs to every target.

  alien skill uninstall [--target <key>]... [--all]
      Remove from one or more targets.

  alien skill targets
      List the available targets and where each writes.

  alien skill print [--target <key>]
      Write the skill body to stdout (default: claude-user format).

  alien skill path [--target <key>]
      Print the install path for a target.
`)
}

// cmdSkillList prints the registered targets with their install paths
// and current status.
func cmdSkillList(args []string) {
	_, wantJSON := extractBoolFlag(args, "--json")
	installed := map[string]bool{}
	for _, k := range installedSkillTargets() {
		installed[k] = true
	}
	targets := skillTargets()
	if wantJSON {
		type entry struct {
			Key       string `json:"key"`
			Label     string `json:"label"`
			Path      string `json:"path"`
			Installed bool   `json:"installed"`
		}
		out := make([]entry, 0, len(targets))
		for _, t := range targets {
			out = append(out, entry{Key: t.Key, Label: t.Label, Path: t.Path, Installed: installed[t.Key]})
		}
		emitJSON(out)
		return
	}
	fmt.Println()
	fmt.Printf("  %s %s\n\n", brcyan("👽"), bold("alien skill targets"))
	maxKey := 0
	for _, t := range targets {
		if len(t.Key) > maxKey {
			maxKey = len(t.Key)
		}
	}
	for _, t := range targets {
		mark := gray("○")
		if installed[t.Key] {
			mark = green("●")
		}
		fmt.Printf("  %s %s  %s\n", mark, bold(brcyan(padRight(t.Key, maxKey))), t.Label)
		fmt.Printf("      %s %s\n", dim("path:"), gray(t.Path))
	}
	fmt.Println()
}

// cmdSkillPrint writes the skill body for a particular target to stdout.
func cmdSkillPrint(args []string) {
	_, key := extractFlag(args, "--target")
	if key == "" {
		key = "claude-user"
	}
	t, ok := findSkillTarget(key)
	if !ok {
		errorf("unknown target %q (run `alien skill targets`)", key)
		os.Exit(1)
	}
	fmt.Print(t.Body())
}

// cmdSkillPath prints the install path for a target.
func cmdSkillPath(args []string) {
	_, key := extractFlag(args, "--target")
	if key == "" {
		key = "claude-user"
	}
	t, ok := findSkillTarget(key)
	if !ok {
		errorf("unknown target %q", key)
		os.Exit(1)
	}
	fmt.Println(t.Path)
}

// ---------- install ----------

func cmdSkillInstall(args []string) {
	args, all := extractBoolFlag(args, "--all")
	args, force := extractBoolFlag(args, "--force")
	keys := extractAllFlags(args, "--target")

	var selected []skillTarget
	switch {
	case all:
		selected = skillTargets()
	case len(keys) > 0:
		for _, k := range keys {
			t, ok := findSkillTarget(k)
			if !ok {
				errorf("unknown target %q (run `alien skill targets`)", k)
				os.Exit(1)
			}
			selected = append(selected, t)
		}
	default:
		picked, err := pickSkillTargets(skillTargets())
		if err != nil {
			errorf("%v", err)
			os.Exit(1)
		}
		if picked == nil {
			infof("install cancelled")
			return
		}
		selected = picked
	}

	for _, t := range selected {
		if err := installSkillTarget(t, force); err != nil {
			errorf("%s: %v", t.Key, err)
			continue
		}
		successf("installed %s → %s", bold(brcyan(t.Key)), t.Path)
	}
}

func installSkillTarget(t skillTarget, force bool) error {
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
		return err
	}
	switch t.Mode {
	case modeOverwrite:
		if _, err := os.Stat(t.Path); err == nil && !force {
			return fmt.Errorf("%s exists — re-run with --force to overwrite", t.Path)
		}
		return atomicWrite(t.Path, []byte(t.Body()))

	case modeAppendFenced:
		body := t.Body()
		fenced := fenceStart + "\n" + body
		if !strings.HasSuffix(fenced, "\n") {
			fenced += "\n"
		}
		fenced += fenceEnd + "\n"

		existing, _ := os.ReadFile(t.Path)
		if hasFence(existing) {
			updated := replaceFence(existing, fenced)
			return atomicWrite(t.Path, updated)
		}
		// Append (with separator) — leave any existing content intact.
		out := existing
		if len(out) > 0 && !endsWithNewline(out) {
			out = append(out, '\n')
		}
		if len(out) > 0 {
			out = append(out, '\n') // blank line before our section
		}
		out = append(out, []byte(fenced)...)
		return atomicWrite(t.Path, out)
	}
	return fmt.Errorf("unhandled install mode")
}

// cmdSkillUninstall removes a target's contribution. For overwrite mode
// the file is deleted (and its parent dir, if empty); for append-fenced
// mode the fenced section is excised but the rest of the file is left
// alone.
func cmdSkillUninstall(args []string) {
	args, all := extractBoolFlag(args, "--all")
	keys := extractAllFlags(args, "--target")
	var selected []skillTarget
	switch {
	case all:
		selected = skillTargets()
	case len(keys) > 0:
		for _, k := range keys {
			t, ok := findSkillTarget(k)
			if !ok {
				errorf("unknown target %q", k)
				os.Exit(1)
			}
			selected = append(selected, t)
		}
	default:
		// No args: uninstall whatever is installed.
		installed := installedSkillTargets()
		if len(installed) == 0 {
			warnf("no targets currently installed")
			return
		}
		for _, k := range installed {
			if t, ok := findSkillTarget(k); ok {
				selected = append(selected, t)
			}
		}
	}
	for _, t := range selected {
		if err := uninstallSkillTarget(t); err != nil {
			errorf("%s: %v", t.Key, err)
			continue
		}
		successf("removed %s from %s", bold(brcyan(t.Key)), t.Path)
	}
}

func uninstallSkillTarget(t skillTarget) error {
	switch t.Mode {
	case modeOverwrite:
		if err := os.Remove(t.Path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		// Tidy: remove the parent dir if it's now empty (e.g. ~/.claude/skills/alien/).
		if entries, err := os.ReadDir(filepath.Dir(t.Path)); err == nil && len(entries) == 0 {
			_ = os.Remove(filepath.Dir(t.Path))
		}
		return nil

	case modeAppendFenced:
		data, err := os.ReadFile(t.Path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !hasFence(data) {
			return nil
		}
		stripped := stripFence(data)
		// If the file is now effectively empty, delete it.
		if len(strings.TrimSpace(string(stripped))) == 0 {
			return os.Remove(t.Path)
		}
		return atomicWrite(t.Path, stripped)
	}
	return nil
}

// ---------- helpers ----------

// extractAllFlags returns every value passed via --flag VALUE (or
// --flag=VALUE) in args. Used for `--target a --target b ...`.
func extractAllFlags(args []string, flag string) []string {
	out := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == flag:
			if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
			}
		case strings.HasPrefix(a, flag+"="):
			out = append(out, strings.TrimPrefix(a, flag+"="))
		}
	}
	return out
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func hasFence(data []byte) bool {
	s := string(data)
	return strings.Contains(s, fenceStart) && strings.Contains(s, fenceEnd)
}

// replaceFence swaps the bytes between fenceStart and fenceEnd (inclusive)
// with `replacement`. `replacement` must already include both fence lines.
func replaceFence(existing []byte, replacement string) []byte {
	s := string(existing)
	si := strings.Index(s, fenceStart)
	ei := strings.Index(s, fenceEnd)
	if si < 0 || ei < 0 || ei < si {
		return existing
	}
	endLine := ei + len(fenceEnd)
	if endLine < len(s) && s[endLine] == '\n' {
		endLine++
	}
	return []byte(s[:si] + replacement + s[endLine:])
}

func stripFence(existing []byte) []byte {
	s := string(existing)
	si := strings.Index(s, fenceStart)
	ei := strings.Index(s, fenceEnd)
	if si < 0 || ei < 0 || ei < si {
		return existing
	}
	endLine := ei + len(fenceEnd)
	if endLine < len(s) && s[endLine] == '\n' {
		endLine++
	}
	// Also drop a single blank line before the fence if present, to avoid
	// leaving double newlines after we're gone.
	pre := s[:si]
	pre = strings.TrimRight(pre, "\n")
	if len(pre) > 0 {
		pre += "\n"
	}
	return []byte(pre + s[endLine:])
}

func endsWithNewline(data []byte) bool {
	return len(data) > 0 && data[len(data)-1] == '\n'
}

func emitJSON(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}
