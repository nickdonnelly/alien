package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigGetSetUnset(t *testing.T) {
	home := t.TempDir()

	// Unset key resolves to its default.
	if got := mustRun(t, home, "", "config", "get", "tab-insert").stdout; got != "name\n" {
		t.Fatalf("default tab-insert = %q; want %q", got, "name\n")
	}

	// Set persists and canonicalizes aliases (cmd -> command).
	mustRun(t, home, "", "config", "set", "tab-insert", "cmd")
	if got := mustRun(t, home, "", "config", "get", "tab-insert").stdout; got != "command\n" {
		t.Fatalf("after set: tab-insert = %q; want %q", got, "command\n")
	}
	if data := readFile(t, filepath.Join(home, "config.toml")); !strings.Contains(data, `tab-insert = "command"`) {
		t.Errorf("config.toml missing setting:\n%s", data)
	}

	// Unset returns to default.
	mustRun(t, home, "", "config", "unset", "tab-insert")
	if got := mustRun(t, home, "", "config", "get", "tab-insert").stdout; got != "name\n" {
		t.Errorf("after unset: tab-insert = %q; want %q", got, "name\n")
	}
}

func TestConfigRejectsBadInput(t *testing.T) {
	home := t.TempDir()

	if r := runAlien(t, home, "", "config", "set", "tab-insert", "bogus"); r.code == 0 {
		t.Errorf("set tab-insert=bogus should fail; got exit 0")
	}
	if r := runAlien(t, home, "", "config", "set", "nope", "x"); r.code == 0 {
		t.Errorf("set of unknown key should fail; got exit 0")
	}
	if r := runAlien(t, home, "", "config", "get", "nope"); r.code == 0 {
		t.Errorf("get of unknown key should fail; got exit 0")
	}
}

// Any invocation must create config.toml with documented defaults if missing.
func TestConfigAutoCreated(t *testing.T) {
	home := t.TempDir()

	// A command unrelated to config still creates the file.
	mustRun(t, home, "", "ls", "--json")

	data := readFile(t, filepath.Join(home, "config.toml"))
	if !strings.Contains(data, `tab-insert = "name"`) {
		t.Errorf("auto-created config.toml missing default:\n%s", data)
	}
	// Each option is documented with a comment.
	if !strings.Contains(data, "#") || !strings.Contains(data, "the alias name") {
		t.Errorf("auto-created config.toml missing help comments:\n%s", data)
	}

	// Re-creation must not clobber a user's edit.
	mustRun(t, home, "", "config", "set", "tab-insert", "command")
	mustRun(t, home, "", "ls", "--json") // another invocation
	if got := mustRun(t, home, "", "config", "get", "tab-insert").stdout; got != "command\n" {
		t.Errorf("auto-create clobbered user setting: tab-insert = %q", got)
	}
}

func TestParseTOMLRoundTrip(t *testing.T) {
	c := newConfig()
	c.values["tab-insert"] = "command"
	parsed := parseTOML([]byte(c.render()))
	if parsed["tab-insert"] != "command" {
		t.Errorf("round-trip tab-insert = %q; want command\nrendered:\n%s", parsed["tab-insert"], c.render())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
