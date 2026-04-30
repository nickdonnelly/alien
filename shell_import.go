package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// rcSearchPaths returns the list of rc files we'll grep for `alias <name>=`
// to figure out where each shell-imported alias came from. The shell hook
// passes its known list via the ALIEN_RC_FILES env var (colon-separated);
// we fall back to a sensible default set if it's not provided.
func rcSearchPaths() []string {
	if v := os.Getenv("ALIEN_RC_FILES"); v != "" {
		out := []string{}
		for _, p := range strings.Split(v, ":") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	out := []string{}
	for _, rel := range []string{
		".zshrc", ".zshenv", ".zprofile", ".zlogin",
		".bashrc", ".bash_profile", ".profile",
		".aliases", ".bash_aliases", ".zsh_aliases",
	} {
		p := filepath.Join(home, rel)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// originResolver maps each alias name to a human-readable origin label
// (e.g. ".zshrc", "omz:git") by greping the cached rc file contents for
// `alias <name>=` lines. It loads each file only once.
type originResolver struct {
	files []string
	cache map[string][]byte
}

func newOriginResolver() *originResolver {
	r := &originResolver{cache: map[string][]byte{}}
	for _, p := range rcSearchPaths() {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		r.files = append(r.files, p)
		r.cache[p] = data
	}
	return r
}

func (r *originResolver) lookup(name string) string {
	if r == nil || len(r.files) == 0 {
		return ""
	}
	// Match `alias name=` allowing leading whitespace and either bare or
	// quoted forms. We don't bother to track WHICH definition wins if a
	// name appears in multiple files — we just take the first hit, which
	// is good enough for "where can I edit this?".
	pat := regexp.MustCompile(`(?m)^[\s]*alias[\s]+` + regexp.QuoteMeta(name) + `[\s]*=`)
	for _, p := range r.files {
		if pat.Match(r.cache[p]) {
			return prettyOrigin(p)
		}
	}
	return ""
}

// prettyOrigin returns a short label for an rc file path. Special-cased for
// oh-my-zsh layout: `~/.oh-my-zsh/plugins/git/git.plugin.zsh` becomes
// `omz:git`. Everything else is just the basename.
func prettyOrigin(path string) string {
	if i := strings.Index(path, "/.oh-my-zsh/plugins/"); i >= 0 {
		rest := path[i+len("/.oh-my-zsh/plugins/"):]
		if j := strings.Index(rest, "/"); j > 0 {
			return "omz:" + rest[:j]
		}
		return "omz:" + rest
	}
	if strings.Contains(path, "/.oh-my-zsh/") {
		return "omz"
	}
	return filepath.Base(path)
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

	// First pass: parse stdin into a map of name → (command, from). Doing
	// this outside the lock keeps the lock window short — scanning can be
	// arbitrarily slow if stdin stalls.
	type pending struct {
		command string
		from    string
	}
	parsed := map[string]pending{}
	resolver := newOriginResolver()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineCount := 0
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
		parsed[name] = pending{command: cmd, from: resolver.lookup(name)}
	}
	if err := scanner.Err(); err != nil {
		errorf("read stdin: %v", err)
		os.Exit(1)
	}

	var imported, updated, pruned int
	if err := updateStore(func(s *Store) error {
		seen := map[string]bool{}
		now := time.Now()
		for name, p := range parsed {
			// Don't override aliases the user (or a pack) owns. They may
			// have `promoted` this name to take ownership of the rc entry.
			if existing, ok := s.Aliases[name]; ok && existing.Source != "shell" {
				seen[name] = true
				continue
			}
			if existing, ok := s.Aliases[name]; ok && existing.Command == p.command && existing.From == p.from {
				seen[name] = true
				continue
			}
			entry := Alias{
				Command:   p.command,
				Enabled:   true,
				Source:    "shell",
				From:      p.from,
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
		return nil
	}); err != nil {
		errorf("%v", err)
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
	if err := updateStore(func(s *Store) error {
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
		return nil
	}); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	successf("promoted %s — now user-managed", bold(brcyan(name)))
	fmt.Fprintf(os.Stderr, "  %s the rc definition still exists; alien will no longer mirror it\n", gray("note:"))
}
