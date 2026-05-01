package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAliasLine(t *testing.T) {
	cases := []struct {
		in, name, command string
		ok                bool
	}{
		// zsh form (no leading "alias ")
		{"ll='ls -al'", "ll", "ls -al", true},
		// bash form (with leading "alias ")
		{"alias ll='ls -al'", "ll", "ls -al", true},
		// the embedded-quote escape used by both shells
		{`it=' it'\''s '`, "it", " it's ", true},
		// double-quoted form (rare but valid)
		{`d="git diff"`, "d", "git diff", true},
		// blank / comments / no =
		{"", "", "", false},
		{"# a comment", "", "", false},
		{"noequals", "", "", false},
		{"=novalue", "", "", false},
		// empty value
		{"empty=''", "", "", false},
	}
	for _, c := range cases {
		name, cmd, ok := parseAliasLine(c.in)
		if ok != c.ok || name != c.name || cmd != c.command {
			t.Errorf("parseAliasLine(%q) = (%q, %q, %v); want (%q, %q, %v)",
				c.in, name, cmd, ok, c.name, c.command, c.ok)
		}
	}
}

func TestNormalizeCommand(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		" ":                "",
		"git status":       "git status",
		"  git   status  ": "git status",
		"git\tstatus\nfoo": "git status foo",
	}
	for in, want := range cases {
		if got := normalizeCommand(in); got != want {
			t.Errorf("normalizeCommand(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestPrettyOrigin(t *testing.T) {
	cases := map[string]string{
		"/home/u/.zshrc":                                            ".zshrc",
		"/home/u/.bashrc":                                           ".bashrc",
		"/home/u/.oh-my-zsh/plugins/git/git.plugin.zsh":             "omz:git",
		"/home/u/.oh-my-zsh/plugins/docker-compose/dc.plugin.zsh":   "omz:docker-compose",
		"/home/u/.oh-my-zsh/lib/aliases.zsh":                        "omz",
		"/some/other/path/.aliases":                                 ".aliases",
	}
	for in, want := range cases {
		if got := prettyOrigin(in); got != want {
			t.Errorf("prettyOrigin(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestOriginResolverLookup writes a minimal rc file and confirms the
// regex-based grep finds the alias and returns the right origin label.
func TestOriginResolverLookup(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".zshrc")
	body := "alias ll='ls -al'\n# alias ignored='no'\nalias gst=\"git status\"\n"
	if err := os.WriteFile(rc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALIEN_RC_FILES", rc)

	r := newOriginResolver()
	if got := r.lookup("ll"); got != ".zshrc" {
		t.Errorf("lookup(ll) = %q; want .zshrc", got)
	}
	if got := r.lookup("gst"); got != ".zshrc" {
		t.Errorf("lookup(gst) = %q; want .zshrc", got)
	}
	if got := r.lookup("ignored"); got != "" {
		t.Errorf("lookup(ignored) = %q; want empty", got)
	}
}
