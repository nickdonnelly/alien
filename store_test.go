package main

import (
	"testing"
	"unicode/utf8"
)

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "''"},
		{"ls -al", "'ls -al'"},
		{"echo $HOME", "'echo $HOME'"},
		// The standard `'\''` trick: close quote, escaped quote, reopen.
		{"it's", `'it'\''s'`},
		{`it's "quoted"`, `'it'\''s "quoted"'`},
		{"\\backslash", "'\\backslash'"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestValidAliasName(t *testing.T) {
	good := []string{"ll", "g", "g_", "g-1", "_under", "..", "...", "....."}
	bad := []string{
		"", " ", "1leading", "with space", "with/slash", "with$dollar",
		"with.dot", ".onedot", "...with-suffix",
	}
	for _, n := range good {
		if !validAliasName(n) {
			t.Errorf("validAliasName(%q) = false; want true", n)
		}
	}
	for _, n := range bad {
		if validAliasName(n) {
			t.Errorf("validAliasName(%q) = true; want false", n)
		}
	}
}

func TestMatchesTab(t *testing.T) {
	cases := []struct {
		a    Alias
		tab  string
		want bool
	}{
		{Alias{}, "", true},
		{Alias{}, "all", true},
		{Alias{}, "user", true},
		{Alias{Source: "shell"}, "user", false},
		{Alias{Source: "shell"}, "shell", true},
		{Alias{Source: "pack:git"}, "pack:git", true},
		{Alias{Source: "pack:git"}, "pack:docker", false},
		{Alias{Source: "pack:git"}, "user", false},
		{Alias{Source: ""}, "shell", false},
	}
	for i, c := range cases {
		if got := matchesTab(c.a, c.tab); got != c.want {
			t.Errorf("case %d: matchesTab(source=%q, tab=%q) = %v; want %v",
				i, c.a.Source, c.tab, got, c.want)
		}
	}
}

// truncate and padRight measure width in runes. Byte slicing would split a
// multi-byte character, emitting invalid UTF-8 that renders as a replacement
// glyph, and would over-count widths so columns stop lining up.
func TestTruncateAndPadRightAreRuneAware(t *testing.T) {
	truncCases := []struct {
		in   string
		n    int
		want string
	}{
		{"ls -al", 10, "ls -al"},      // shorter than the limit: unchanged
		{"ls -al", 6, "ls -al"},       // exactly the limit: unchanged
		{"abcdefg", 4, "abc…"},        // ASCII cut
		{"echo ▲▲▲▲▲", 8, "echo ▲▲…"}, // multi-byte cut lands on a rune boundary
		{"▲▲▲", 1, "…"},
		{"▲▲▲", 0, "▲▲▲"}, // non-positive width disables truncation
	}
	for _, c := range truncCases {
		got := truncate(c.in, c.n)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q; want %q", c.in, c.n, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) produced invalid UTF-8: %q", c.in, c.n, got)
		}
		if n := utf8.RuneCountInString(got); c.n > 0 && n > c.n {
			t.Errorf("truncate(%q, %d) = %q, %d runes wide; want at most %d", c.in, c.n, got, n, c.n)
		}
	}

	// Equal rune counts must pad to equal widths regardless of encoding size.
	if a, b := padRight("▲▲▲", 6), padRight("abc", 6); utf8.RuneCountInString(a) != utf8.RuneCountInString(b) {
		t.Errorf("padRight width mismatch: %q (%d runes) vs %q (%d runes)",
			a, utf8.RuneCountInString(a), b, utf8.RuneCountInString(b))
	}
	if got := padRight("▲▲▲", 2); got != "▲▲▲" {
		t.Errorf("padRight(%q, 2) = %q; want the input unchanged", "▲▲▲", got)
	}
}
