package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHistLinesZshExtended(t *testing.T) {
	lines := []string{
		": 1717920000:0;git status",
		": 1717920005:0;echo one \\",
		"  two",
		": 1717920010:2;ls -al",
	}
	cmds, un := parseHistLines(lines)
	want := []string{"git status", "echo one   two", "ls -al"}
	if un != 0 {
		t.Errorf("unparseable = %d; want 0", un)
	}
	if len(cmds) != len(want) {
		t.Fatalf("cmds = %q; want %q", cmds, want)
	}
	for i := range want {
		if normalizeCommand(cmds[i]) != normalizeCommand(want[i]) {
			t.Errorf("cmds[%d] = %q; want %q", i, cmds[i], want[i])
		}
	}
}

func TestParseHistLinesBashTimestamps(t *testing.T) {
	lines := []string{
		"#1717920000",
		"git status",
		"#1717920005",
		"docker compose up -d",
		"plain command without timestamp",
	}
	cmds, _ := parseHistLines(lines)
	want := []string{"git status", "docker compose up -d", "plain command without timestamp"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Errorf("cmds = %q; want %q", cmds, want)
	}
}

func TestParseHistLinesMetafied(t *testing.T) {
	lines := []string{
		"git status",
		"echo h\x83\xe9llo", // zsh metafied byte — skipped, counted
		"ls -al",
	}
	cmds, un := parseHistLines(lines)
	if un != 1 {
		t.Errorf("unparseable = %d; want 1", un)
	}
	if len(cmds) != 2 {
		t.Errorf("cmds = %q; want 2 entries", cmds)
	}
}

func TestNormalizeScanCommand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git status", "git status"},
		{"sudo systemctl restart nginx", "systemctl restart nginx"},
		{"FOO=1 BAR=2 make test", "make test"},
		{"sudo FOO=1 make test", "make test"},
		{"  spaced   out  ", "spaced out"},
	}
	for _, c := range cases {
		if got := normalizeScanCommand(c.in); got != c.want {
			t.Errorf("normalizeScanCommand(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func repeat(cmd string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = cmd
	}
	return out
}

func TestScanCandidates(t *testing.T) {
	noTaken := func(string) bool { return false }
	s := &Store{Aliases: map[string]Alias{
		"gp": {Command: "git push", Enabled: true},
	}}

	var history []string
	history = append(history, repeat("git status", 10)...)
	history = append(history, repeat("docker compose up -d", 6)...)
	history = append(history, "git commit -m 'one'", "git commit -m 'two'",
		"git commit -m 'three'", "git commit -m 'four'", "git commit -m 'five'")
	history = append(history, repeat("git push", 9)...)            // already aliased (command match)
	history = append(history, repeat("gp", 8)...)                  // already aliased (name match)
	history = append(history, repeat("ls", 20)...)                 // single token
	history = append(history, repeat("export TOKEN=abc123", 7)...) // secret-shaped
	history = append(history, repeat("alien ls --json", 7)...)     // alien itself
	history = append(history, "rare command", "rare command")      // below min

	got := scanCandidates(history, s, noTaken, 5)

	byCmd := map[string]scanSuggestion{}
	for _, sg := range got {
		byCmd[sg.Command] = sg
	}
	if sg, ok := byCmd["git status"]; !ok || sg.Count != 10 || sg.Name != "gs" {
		t.Errorf("git status suggestion = %+v (present=%v)", sg, ok)
	}
	if sg, ok := byCmd["docker compose up -d"]; !ok || sg.Name != "dcud" {
		t.Errorf("docker compose suggestion = %+v (present=%v)", sg, ok)
	}
	// The -m variants pool into the 3-token prefix; equal-count shorter
	// prefixes are subsumed.
	if sg, ok := byCmd["git commit -m"]; !ok || sg.Count != 5 {
		t.Errorf("git commit -m suggestion = %+v (present=%v)", sg, ok)
	}
	if _, ok := byCmd["git commit"]; ok {
		t.Error("subsumed prefix 'git commit' should not be suggested separately")
	}
	for _, banned := range []string{"git push", "gp", "ls", "export TOKEN=abc123", "alien ls --json", "rare command"} {
		if _, ok := byCmd[banned]; ok {
			t.Errorf("%q should have been filtered out", banned)
		}
	}
	// Ranking is by score, descending and deterministic.
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("not sorted by score: %+v before %+v", got[i-1], got[i])
		}
	}
}

// Two commands with the same initials must not both be assigned the same
// name in one scan batch, and assignment must be deterministic regardless
// of map iteration order.
func TestScanCandidatesNoDuplicateNames(t *testing.T) {
	noTaken := func(string) bool { return false }
	s := &Store{Aliases: map[string]Alias{}}
	var history []string
	history = append(history, repeat("python3 app.py", 10)...)
	history = append(history, repeat("python3 ackbar.py", 8)...)

	for round := 0; round < 5; round++ {
		got := scanCandidates(history, s, noTaken, 5)
		if len(got) != 2 {
			t.Fatalf("got %d suggestions; want 2: %+v", len(got), got)
		}
		if got[0].Name == got[1].Name {
			t.Fatalf("duplicate name %q proposed for both commands", got[0].Name)
		}
		// Higher-frequency command wins the shorter initials.
		if got[0].Command != "python3 app.py" || got[0].Name != "pa" {
			t.Errorf("round %d: best candidate = %+v; want python3 app.py as pa", round, got[0])
		}
	}
}

func TestSuggestAliasName(t *testing.T) {
	none := func(string) bool { return false }
	if got := suggestAliasName("git status", none); got != "gs" {
		t.Errorf("got %q; want gs", got)
	}
	if got := suggestAliasName("git commit -m", none); got != "gcm" {
		t.Errorf("got %q; want gcm", got)
	}
	// Deconfliction: when initials are taken, fall back to word+initials,
	// then numbered.
	gsTaken := func(n string) bool { return n == "gs" }
	if got := suggestAliasName("git status", gsTaken); got != "gits" {
		t.Errorf("deconflicted = %q; want gits", got)
	}
	allTaken := func(n string) bool { return n != "gs2" }
	if got := suggestAliasName("git status", allTaken); got != "gs2" {
		t.Errorf("numbered fallback = %q; want gs2", got)
	}
}

func TestSuggestAliasNameAvoidsPathBinaries(t *testing.T) {
	// Build a fake PATH with a binary named "gs" (the ghostscript trap).
	dir := t.TempDir()
	bin := filepath.Join(dir, "gs")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if nameOnPath("gs") != true {
		t.Fatal("test setup: gs not found on fake PATH")
	}
	got := suggestAliasName("git status", nameOnPath)
	if got == "gs" {
		t.Error("suggested a name that shadows a PATH binary")
	}
}

func TestCLIScanJSON(t *testing.T) {
	home := t.TempDir()
	hist := filepath.Join(t.TempDir(), "history")
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString(": 1717920000:0;git status --short\n")
	}
	for i := 0; i < 3; i++ {
		b.WriteString("ls -al\n") // below default --min
	}
	if err := os.WriteFile(hist, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	r := mustRun(t, home, "", "scan", "--json", "--file", hist)
	var got []scanSuggestion
	if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
		t.Fatalf("parse scan --json: %v\n%s", err, r.stdout)
	}
	if len(got) != 1 || got[0].Command != "git status --short" || got[0].Count != 12 {
		t.Errorf("scan --json = %+v", got)
	}

	// `suggest --history` is an alias for scan.
	r2 := mustRun(t, home, "", "suggest", "--history", "--json", "--file", hist)
	if r2.stdout != r.stdout {
		t.Errorf("suggest --history output differs from scan:\n%s\nvs\n%s", r2.stdout, r.stdout)
	}

	// --min raises the floor past everything.
	r3 := mustRun(t, home, "", "scan", "--json", "--file", hist, "--min", "50")
	if !strings.Contains(r3.stdout, "[]") && strings.TrimSpace(r3.stdout) != "null" {
		t.Errorf("scan --min 50 should be empty, got: %s", r3.stdout)
	}
}
