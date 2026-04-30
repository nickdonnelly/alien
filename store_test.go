package main

import "testing"

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
