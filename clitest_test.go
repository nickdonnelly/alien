package main

// CLI integration harness: builds the alien binary once per test run and
// drives it as a subprocess with an isolated ALIEN_HOME per test. Command
// handlers print directly and call os.Exit, so exercising them end-to-end
// through the binary is how we assert output and exit codes without
// refactoring every handler.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var alienBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "alien-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdtemp:", err)
		os.Exit(1)
	}
	alienBin = filepath.Join(tmp, "alien")
	build := exec.Command("go", "build", "-o", alienBin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build alien binary:", err)
		os.RemoveAll(tmp)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

type cliResult struct {
	stdout string
	stderr string
	code   int
}

func runAlien(t *testing.T, home, stdin string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(alienBin, args...)
	cmd.Env = append(os.Environ(), "ALIEN_HOME="+home, "NO_COLOR=1")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run alien %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return cliResult{stdout: out.String(), stderr: errb.String(), code: code}
}

// mustRun fails the test unless the command exits 0.
func mustRun(t *testing.T, home, stdin string, args ...string) cliResult {
	t.Helper()
	r := runAlien(t, home, stdin, args...)
	if r.code != 0 {
		t.Fatalf("alien %v: exit %d\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

// listJSON runs `alien ls --json` and decodes it.
func listJSON(t *testing.T, home string, extra ...string) []listEntry {
	t.Helper()
	r := mustRun(t, home, "", append([]string{"ls", "--json"}, extra...)...)
	var entries []listEntry
	if err := json.Unmarshal([]byte(r.stdout), &entries); err != nil {
		t.Fatalf("parse ls --json: %v\noutput: %s", err, r.stdout)
	}
	return entries
}

func findEntry(t *testing.T, entries []listEntry, name string) listEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("alias %q not in listing: %+v", name, entries)
	return listEntry{}
}

// seedStore writes a raw aliases.json so tests can set up states the CLI
// won't create itself (shell-sourced entries, corrupt files, odd versions).
func seedStore(t *testing.T, home, content string) {
	t.Helper()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "aliases.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------- add ----------

func TestCLIAdd(t *testing.T) {
	home := t.TempDir()

	r := mustRun(t, home, "", "gs", "-c", "git status", "-m", "short status")
	if !strings.Contains(r.stderr, "aliased") {
		t.Errorf("expected success message, got: %s", r.stderr)
	}
	if got := mustRun(t, home, "", "get", "gs").stdout; got != "git status\n" {
		t.Errorf("get gs = %q; want %q", got, "git status\n")
	}

	// --prev-cmd is how the shell wrapper passes the previous command.
	mustRun(t, home, "", "lc", "--prev-cmd", "ls -alh ~/.config")
	if got := mustRun(t, home, "", "get", "lc").stdout; got != "ls -alh ~/.config\n" {
		t.Errorf("get lc = %q", got)
	}

	// No command available at all.
	r = runAlien(t, home, "", "empty")
	if r.code != 1 || !strings.Contains(r.stderr, "no command to alias") {
		t.Errorf("add with no command: exit %d, stderr %q", r.code, r.stderr)
	}

	// Duplicate without --force is refused...
	r = runAlien(t, home, "", "gs", "-c", "git stash")
	if r.code != 1 || !strings.Contains(r.stderr, "already exists") {
		t.Errorf("dup add: exit %d, stderr %q", r.code, r.stderr)
	}
	// ...and the original survives.
	if got := mustRun(t, home, "", "get", "gs").stdout; got != "git status\n" {
		t.Errorf("dup add clobbered original: %q", got)
	}

	// --force overwrites.
	mustRun(t, home, "", "gs", "-c", "git stash", "--force")
	if got := mustRun(t, home, "", "get", "gs").stdout; got != "git stash\n" {
		t.Errorf("forced add not applied: %q", got)
	}

	// Invalid names are rejected.
	r = runAlien(t, home, "", "1bad", "-c", "true")
	if r.code != 1 || !strings.Contains(r.stderr, "invalid alias name") {
		t.Errorf("invalid name: exit %d, stderr %q", r.code, r.stderr)
	}

	// Self-referencing aliases would loop.
	r = runAlien(t, home, "", "loop", "-c", "alien ls")
	if r.code != 1 || !strings.Contains(r.stderr, "refusing") {
		t.Errorf("self-reference: exit %d, stderr %q", r.code, r.stderr)
	}

	// Unknown flag at the front must not become an alias name.
	r = runAlien(t, home, "", "--bogus")
	if r.code != 2 {
		t.Errorf("unknown flag: exit %d; want 2", r.code)
	}

	// Control characters (paste accidents, raw escapes) are rejected;
	// newlines and tabs are legitimate.
	r = runAlien(t, home, "", "esc", "-c", "echo \x1b[31mred")
	if r.code != 1 || !strings.Contains(r.stderr, "control character") {
		t.Errorf("control char: exit %d, stderr %q", r.code, r.stderr)
	}
	mustRun(t, home, "", "multi", "-c", "echo one\necho two")
}

// ---------- ls --json / get / show / export ----------

func TestCLIListJSON(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status", "-m", "short status")
	mustRun(t, home, "", "gl", "-c", "git log --oneline")
	mustRun(t, home, "", "disable", "gl")

	entries := listJSON(t, home)
	if len(entries) != 2 {
		t.Fatalf("got %d entries; want 2", len(entries))
	}
	gs := findEntry(t, entries, "gs")
	if gs.Command != "git status" || gs.Comment != "short status" || !gs.Enabled {
		t.Errorf("gs entry wrong: %+v", gs)
	}
	if gl := findEntry(t, entries, "gl"); gl.Enabled {
		t.Errorf("gl should be disabled: %+v", gl)
	}

	// tags must serialize as [] (not null) for parser ergonomics.
	raw := mustRun(t, home, "", "ls", "--json").stdout
	if strings.Contains(raw, `"tags": null`) {
		t.Errorf("tags serialized as null:\n%s", raw)
	}

	only := listJSON(t, home, "--enabled-only")
	if len(only) != 1 || only[0].Name != "gs" {
		t.Errorf("--enabled-only = %+v; want just gs", only)
	}
}

func TestCLIGetShowExport(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")
	mustRun(t, home, "", "off", "-c", "echo off")
	mustRun(t, home, "", "disable", "off")
	seedShell := `{"version":1,"aliases":{
		"gs":{"command":"git status","enabled":true,"created_at":"2026-01-01T00:00:00Z"},
		"off":{"command":"echo off","enabled":false,"created_at":"2026-01-01T00:00:00Z"},
		"sl":{"command":"ls","enabled":true,"source":"shell","from":".zshrc","created_at":"2026-01-01T00:00:00Z"}}}`
	seedStore(t, home, seedShell)

	r := runAlien(t, home, "", "get", "nope")
	if r.code != 1 || !strings.Contains(r.stderr, "no alias named") {
		t.Errorf("get unknown: exit %d, stderr %q", r.code, r.stderr)
	}

	show := mustRun(t, home, "", "show", "gs").stdout
	if !strings.Contains(show, "git status") {
		t.Errorf("show gs missing command:\n%s", show)
	}

	// export prints enabled entries only; shell-sourced entries are included
	// (export is the "give me everything" dump, unlike aliases.sh).
	export := mustRun(t, home, "", "export").stdout
	if !strings.Contains(export, "alias gs='git status'") {
		t.Errorf("export missing gs:\n%s", export)
	}
	if strings.Contains(export, "alias off=") {
		t.Errorf("export includes disabled alias:\n%s", export)
	}

	// aliases.sh (the sourced file) must skip shell-sourced entries — the rc
	// already defines them.
	sh, err := os.ReadFile(filepath.Join(home, "aliases.sh"))
	if err == nil && strings.Contains(string(sh), "alias sl=") {
		t.Errorf("aliases.sh re-emits shell-sourced alias:\n%s", sh)
	}
}

// ---------- delete / toggle / comment ----------

func TestCLIDeleteToggleComment(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")

	// toggle flips, enable/disable set explicitly.
	mustRun(t, home, "", "toggle", "gs")
	if e := findEntry(t, listJSON(t, home), "gs"); e.Enabled {
		t.Errorf("toggle did not disable gs")
	}
	mustRun(t, home, "", "enable", "gs")
	if e := findEntry(t, listJSON(t, home), "gs"); !e.Enabled {
		t.Errorf("enable did not enable gs")
	}

	r := runAlien(t, home, "", "toggle", "nope")
	if r.code != 1 {
		t.Errorf("toggle unknown: exit %d; want 1", r.code)
	}

	// comment set + clear.
	mustRun(t, home, "", "comment", "gs", "tidy", "status")
	if e := findEntry(t, listJSON(t, home), "gs"); e.Comment != "tidy status" {
		t.Errorf("comment = %q; want %q", e.Comment, "tidy status")
	}
	mustRun(t, home, "", "comment", "gs")
	if e := findEntry(t, listJSON(t, home), "gs"); e.Comment != "" {
		t.Errorf("comment not cleared: %q", e.Comment)
	}

	// delete without -f prompts; "n" (and EOF) cancel, "y" deletes.
	r = mustRun(t, home, "n\n", "delete", "gs")
	if !strings.Contains(r.stderr, "cancelled") {
		t.Errorf("delete n: expected cancel, stderr %q", r.stderr)
	}
	if len(listJSON(t, home)) != 1 {
		t.Errorf("cancelled delete removed the alias")
	}
	mustRun(t, home, "y\n", "delete", "gs")
	if len(listJSON(t, home)) != 0 {
		t.Errorf("confirmed delete left the alias behind")
	}

	r = runAlien(t, home, "", "delete", "gs", "-f")
	if r.code != 1 || !strings.Contains(r.stderr, "no alias named") {
		t.Errorf("delete unknown: exit %d, stderr %q", r.code, r.stderr)
	}
}

// ---------- run / suggest ----------

func TestCLIRun(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "greet", "-c", "echo hello $1")
	mustRun(t, home, "", "fail", "-c", "exit 7")
	mustRun(t, home, "", "off", "-c", "echo off")
	mustRun(t, home, "", "disable", "off")

	r := mustRun(t, home, "", "run", "greet", "world")
	if r.stdout != "hello world\n" {
		t.Errorf("run greet world = %q; want %q", r.stdout, "hello world\n")
	}
	// usage is tracked through the agent path.
	if e := findEntry(t, listJSON(t, home), "greet"); e.UsedCount != 1 {
		t.Errorf("UsedCount = %d; want 1", e.UsedCount)
	}

	// child exit code propagates.
	if r := runAlien(t, home, "", "run", "fail"); r.code != 7 {
		t.Errorf("run fail: exit %d; want 7", r.code)
	}

	if r := runAlien(t, home, "", "run", "off"); r.code != 1 || !strings.Contains(r.stderr, "disabled") {
		t.Errorf("run disabled: exit %d, stderr %q", r.code, r.stderr)
	}
	if r := runAlien(t, home, "", "run", "nope"); r.code != 1 {
		t.Errorf("run unknown: exit %d; want 1", r.code)
	}
}

func TestCLISuggest(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")
	mustRun(t, home, "", "off", "-c", "echo off")
	mustRun(t, home, "", "disable", "off")

	r := mustRun(t, home, "", "suggest", "git status")
	if r.stdout != "gs\n" {
		t.Errorf("suggest = %q; want %q", r.stdout, "gs\n")
	}
	// whitespace-insensitive match.
	r = mustRun(t, home, "", "suggest", "git    status")
	if r.stdout != "gs\n" {
		t.Errorf("suggest with extra spaces = %q", r.stdout)
	}
	// no match: exit 1, silent.
	r = runAlien(t, home, "", "suggest", "git push")
	if r.code != 1 || r.stdout != "" {
		t.Errorf("suggest no-match: exit %d, stdout %q", r.code, r.stdout)
	}
	// disabled aliases are never suggested.
	r = runAlien(t, home, "", "suggest", "echo off")
	if r.code != 1 {
		t.Errorf("suggest disabled: exit %d; want 1", r.code)
	}
}

// ---------- managed-source guard rails ----------

func TestCLIGuardManaged(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, `{"version":1,"aliases":{
		"sl":{"command":"ls","enabled":true,"source":"shell","from":".zshrc","created_at":"2026-01-01T00:00:00Z"}}}`)

	r := runAlien(t, home, "", "delete", "sl", "-f")
	if r.code != 1 || !strings.Contains(r.stderr, "shell config") {
		t.Errorf("delete shell-sourced: exit %d, stderr %q", r.code, r.stderr)
	}
	r = runAlien(t, home, "", "toggle", "sl")
	if r.code != 1 || !strings.Contains(r.stderr, "shell config") {
		t.Errorf("toggle shell-sourced: exit %d, stderr %q", r.code, r.stderr)
	}
	// comments are alien-only metadata — allowed on shell entries.
	mustRun(t, home, "", "comment", "sl", "from my rc")
	if e := findEntry(t, listJSON(t, home), "sl"); e.Comment != "from my rc" {
		t.Errorf("comment on shell entry = %q", e.Comment)
	}
}

// ---------- store error handling ----------

func TestCLIStoreErrors(t *testing.T) {
	t.Run("corrupt json", func(t *testing.T) {
		home := t.TempDir()
		seedStore(t, home, `{nope`)
		r := runAlien(t, home, "", "ls")
		if r.code != 1 || !strings.Contains(r.stderr, "parse") {
			t.Errorf("corrupt store: exit %d, stderr %q", r.code, r.stderr)
		}
	})
	t.Run("newer version", func(t *testing.T) {
		home := t.TempDir()
		seedStore(t, home, `{"version":99,"aliases":{}}`)
		r := runAlien(t, home, "", "ls")
		if r.code != 1 || !strings.Contains(r.stderr, "newer than this binary") {
			t.Errorf("future store: exit %d, stderr %q", r.code, r.stderr)
		}
	})
	t.Run("v0 file reads as v1", func(t *testing.T) {
		home := t.TempDir()
		seedStore(t, home, `{"aliases":{"gs":{"command":"git status","enabled":true,"created_at":"2026-01-01T00:00:00Z"}}}`)
		if got := mustRun(t, home, "", "get", "gs").stdout; got != "git status\n" {
			t.Errorf("v0 store: get gs = %q", got)
		}
	})
}

// ---------- concurrency: the advisory lock must not lose writes ----------

func TestCLIConcurrentAdds(t *testing.T) {
	home := t.TempDir()
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("conc%d", i)
			r := runAlien(t, home, "", name, "-c", "echo "+name)
			if r.code != 0 {
				errs <- fmt.Sprintf("%s: exit %d: %s", name, r.code, r.stderr)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if got := len(listJSON(t, home)); got != n {
		t.Errorf("after %d concurrent adds, %d aliases survived — writes were lost", n, got)
	}
}

// ---------- misc ----------

func TestCLIVersionAndHelp(t *testing.T) {
	home := t.TempDir()
	if r := mustRun(t, home, "", "--version"); !strings.Contains(r.stdout, "alien") {
		t.Errorf("--version output: %q", r.stdout)
	}
	if r := mustRun(t, home, "", "help"); !strings.Contains(r.stdout, "USAGE") {
		t.Errorf("help output missing USAGE:\n%s", r.stdout)
	}
	if r := mustRun(t, home, "", "path"); !strings.Contains(r.stdout, "aliases.json") {
		t.Errorf("path output: %q", r.stdout)
	}
}

// --enabled-only and --tag scope the human-readable listing, not just --json.
func TestCLIListFiltersApplyWithoutJSON(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, `{
  "version": 2,
  "aliases": {
    "gs": {"command": "git status", "enabled": true, "tags": ["git"]},
    "gd": {"command": "git diff", "enabled": false, "tags": ["git"]},
    "dc": {"command": "docker compose", "enabled": true, "tags": ["docker"]}
  }
}`)

	r := mustRun(t, home, "", "ls", "--enabled-only")
	if strings.Contains(r.stdout, "gd") {
		t.Errorf("--enabled-only still lists the disabled alias:\n%s", r.stdout)
	}
	for _, want := range []string{"gs", "dc"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("--enabled-only dropped enabled alias %q:\n%s", want, r.stdout)
		}
	}
	// The footer summarises the rows actually printed.
	if !strings.Contains(r.stderr, "2 aliases") {
		t.Errorf("footer should count the filtered rows; got %q", r.stderr)
	}

	r = mustRun(t, home, "", "ls", "--tag", "docker")
	if strings.Contains(r.stdout, "gs") || strings.Contains(r.stdout, "gd") {
		t.Errorf("--tag docker listed non-docker aliases:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "dc") {
		t.Errorf("--tag docker dropped the matching alias:\n%s", r.stdout)
	}

	// Combining both filters leaves nothing here, which must not look like an
	// empty store.
	r = mustRun(t, home, "", "ls", "--tag", "nope")
	if !strings.Contains(r.stderr, "no aliases match") {
		t.Errorf("empty filter result should say so; got %q", r.stderr)
	}
}
