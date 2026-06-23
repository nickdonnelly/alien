package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "git status", "git status"},
		{"trims", "  ls -al  ", "ls -al"},
		// Backslash-newline is a line continuation: join with nothing, drop the
		// cosmetic indent. This is the reported bug (`ls -\<newline>al`).
		{"continuation", "ls -\\\nal", "ls -al"},
		{"continuation indented", "ls -\\\n    al", "ls -al"},
		{"continuation crlf", "ls -\\\r\nal", "ls -al"},
		{"continuation multi", "echo \\\na \\\nb", "echo a b"},
		// A bare newline separates statements -> "; ".
		{"hard newline", "make\nmake install", "make; make install"},
		{"hard newline blank line", "make\n\nmake install", "make; make install"},
		{"trailing newline", "git status\n", "git status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeCommand(tc.in); got != tc.want {
				t.Errorf("sanitizeCommand(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// End-to-end: a continuation captured via --prev-cmd (how the shell wrapper
// delivers the last command) must be stored and exported as a clean single
// line so the generated `alias` is safe to re-tokenize.
func TestCLIAddContinuation(t *testing.T) {
	home := t.TempDir()

	mustRun(t, home, "", "ll", "--prev-cmd", "ls -\\\nal")
	if got := mustRun(t, home, "", "get", "ll").stdout; got != "ls -al\n" {
		t.Fatalf("get ll = %q; want %q", got, "ls -al\n")
	}

	export, err := os.ReadFile(filepath.Join(home, "aliases.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(export), "alias ll='ls -al'") {
		t.Errorf("aliases.sh missing clean alias line:\n%s", export)
	}
	// No raw newline should survive inside the single-quoted body.
	for _, line := range strings.Split(string(export), "\n") {
		if strings.HasPrefix(line, "alias ") && strings.HasSuffix(line, "\\") {
			t.Errorf("alias line ends with a dangling backslash: %q", line)
		}
	}
}
