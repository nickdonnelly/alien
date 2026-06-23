package main

import (
	"os/exec"
	"strings"
	"testing"
)

// FuzzShellQuote asserts the property that matters: a command quoted by
// shellQuote survives a real POSIX shell unchanged. The seed corpus runs as
// a normal test in CI; `go test -fuzz=FuzzShellQuote` explores further.
func FuzzShellQuote(f *testing.F) {
	seeds := []string{
		"",
		"ls -al",
		"echo $HOME",
		"it's",
		`it's "quoted"`,
		"back\\slash",
		"$(rm -rf /tmp/nope)",
		"`whoami`",
		"line one\nline two",
		"tab\there",
		"héllo 👽",
		"'",
		"''",
		`'\''`,
		"!history-expansion",
		"a;b|c&d>e<f",
		"*glob? [chars]",
		"~tilde",
		"%percent %s %d",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if strings.ContainsRune(s, 0) {
			t.Skip("NUL cannot survive argv — rejected at input time instead")
		}
		out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(s)).Output()
		if err != nil {
			t.Fatalf("sh failed on shellQuote(%q) = %s: %v", s, shellQuote(s), err)
		}
		if string(out) != s {
			t.Errorf("round-trip mismatch:\n in: %q\nout: %q\nquoted: %s", s, out, shellQuote(s))
		}
	})
}
