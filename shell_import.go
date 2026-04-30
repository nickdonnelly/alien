package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// parseAliasLine parses a single line from the output of the shell `alias`
// builtin. It accepts both forms:
//
//	bash: `alias name='cmd'`
//	zsh:  `name='cmd'`
//
// Values are typically single-quoted with the shell's `'\''` escape used to
// embed a literal single quote. We unquote that. Returns ok=false on any
// line we can't confidently parse.
func parseAliasLine(line string) (name, command string, ok bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false
	}
	s = strings.TrimPrefix(s, "alias ")
	eq := strings.Index(s, "=")
	if eq <= 0 {
		return "", "", false
	}
	name = s[:eq]
	val := s[eq+1:]
	// Strip a single trailing newline if any (scanner already does this).
	val = strings.TrimRight(val, "\r")

	if len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'' {
		val = val[1 : len(val)-1]
		val = strings.ReplaceAll(val, `'\''`, `'`)
	} else if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		// Some shells emit double-quoted values for unusual content. Treat
		// the body as-is; we don't try to fully resolve $-expansion.
		val = val[1 : len(val)-1]
	}
	if val == "" {
		return "", "", false
	}
	return name, val, true
}

// cmdImportShell reads `alias` output on stdin and merges entries into the
// store with Source="shell". It also prunes stale shell-sourced entries no
// longer present in the input, so removing an alias from the rc and
// restarting the shell propagates the deletion.
//
// If stdin is a terminal (no piped input), this is a no-op — the shell hook
// always pipes input, so an interactive invocation is almost certainly user
// error and we just print a usage hint.
func cmdImportShell(args []string) {
	var quiet bool
	for _, a := range args {
		if a == "--quiet" || a == "-q" {
			quiet = true
		}
	}

	fi, err := os.Stdin.Stat()
	if err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		if !quiet {
			warnf("usage: alias | alien import-shell")
		}
		return
	}

	s, err := loadStore()
	if err != nil {
		errorf("%v", err)
		os.Exit(1)
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	seen := map[string]bool{}
	var lineCount, imported, updated int

	for scanner.Scan() {
		lineCount++
		name, cmd, ok := parseAliasLine(scanner.Text())
		if !ok || !validAliasName(name) {
			continue
		}
		// Skip our own entry points; importing them would cause loops.
		if name == "alien" || name == "a" {
			continue
		}
		// Don't override aliases the user (or a pack) owns. They may have
		// `promoted` this name to take ownership of the rc-defined entry.
		if existing, ok := s.Aliases[name]; ok && existing.Source != "shell" {
			seen[name] = true // keep them in the store as-is
			continue
		}

		now := time.Now()
		if existing, ok := s.Aliases[name]; ok && existing.Command == cmd {
			// Unchanged — keep CreatedAt, just refresh seen.
			seen[name] = true
			continue
		}
		entry := Alias{
			Command:   cmd,
			Enabled:   true,
			Source:    "shell",
			CreatedAt: now,
		}
		if existing, ok := s.Aliases[name]; ok {
			entry.CreatedAt = existing.CreatedAt
			entry.UpdatedAt = now
			entry.Comment = existing.Comment
			updated++
		} else {
			imported++
		}
		s.Aliases[name] = entry
		seen[name] = true
	}
	if err := scanner.Err(); err != nil {
		errorf("read stdin: %v", err)
		os.Exit(1)
	}

	// Prune shell-source entries no longer present in the rc.
	var pruned int
	if lineCount > 0 {
		for name, a := range s.Aliases {
			if a.Source != "shell" {
				continue
			}
			if !seen[name] {
				delete(s.Aliases, name)
				pruned++
			}
		}
	}

	if err := s.save(); err != nil {
		errorf("save: %v", err)
		os.Exit(1)
	}
	if !quiet && (imported > 0 || updated > 0 || pruned > 0) {
		infof("shell aliases: %d imported, %d updated, %d pruned", imported, updated, pruned)
	}
}

// cmdPromote takes a shell-sourced alias and converts it to a user-managed
// one. The next import-shell run will see the entry's Source != "shell" and
// leave it alone, even if the rc still defines the same name.
func cmdPromote(args []string) {
	if len(args) < 1 {
		errorf("usage: alien promote <name>")
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
	if a.Source != "shell" {
		errorf("%s is already user-managed", bold(name))
		os.Exit(1)
	}
	a.Source = ""
	a.UpdatedAt = time.Now()
	s.Aliases[name] = a
	if err := s.save(); err != nil {
		errorf("save: %v", err)
		os.Exit(1)
	}
	successf("promoted %s — now user-managed", bold(brcyan(name)))
	fmt.Fprintf(os.Stderr, "  %s the rc definition still exists; alien will no longer mirror it\n", gray("note:"))
}
