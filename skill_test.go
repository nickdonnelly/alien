package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillBodyAgentStripsFrontmatter(t *testing.T) {
	body := skillBodyAgent()
	if strings.HasPrefix(body, "---") {
		t.Fatalf("agent body should not start with frontmatter; got: %q", body[:80])
	}
	if !strings.Contains(body, "alien — alias-aware bash") {
		t.Errorf("agent body missing core heading")
	}
}

func TestSkillBodyCursorMDCHasFrontmatter(t *testing.T) {
	body := skillBodyCursorMDC()
	if !strings.HasPrefix(body, "---\n") {
		t.Errorf("cursor mdc body should start with YAML frontmatter")
	}
	if !strings.Contains(body, "alwaysApply: true") {
		t.Errorf("cursor mdc body missing alwaysApply")
	}
	if !strings.Contains(body, "alien — alias-aware bash") {
		t.Errorf("cursor mdc body missing core content")
	}
}

func TestFenceAppendAndReplace(t *testing.T) {
	dir := t.TempDir()
	target := skillTarget{
		Key:  "fixture",
		Path: filepath.Join(dir, "AGENTS.md"),
		Mode: modeAppendFenced,
		Body: func() string { return "# section one\nbody one\n" },
	}

	// Pre-existing content the user has authored.
	pre := "# my notes\n\nimportant stuff\n"
	if err := os.WriteFile(target.Path, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	// First install: should append fenced section, leaving notes intact.
	if err := installSkillTarget(target, false); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(target.Path)
	if !strings.Contains(string(out), "important stuff") {
		t.Error("user notes were lost on append")
	}
	if !strings.Contains(string(out), fenceStart) || !strings.Contains(string(out), fenceEnd) {
		t.Error("fence markers missing after install")
	}

	// Re-install with new body — fenced section should be replaced, not duplicated.
	target.Body = func() string { return "# section two\nbody two\n" }
	if err := installSkillTarget(target, false); err != nil {
		t.Fatal(err)
	}
	out, _ = os.ReadFile(target.Path)
	if strings.Contains(string(out), "section one") {
		t.Error("old fenced content not replaced")
	}
	if !strings.Contains(string(out), "section two") {
		t.Error("new fenced content not present")
	}
	if strings.Count(string(out), fenceStart) != 1 {
		t.Errorf("fence duplicated: %d", strings.Count(string(out), fenceStart))
	}

	// Uninstall: fence and content gone, notes preserved.
	if err := uninstallSkillTarget(target); err != nil {
		t.Fatal(err)
	}
	out, _ = os.ReadFile(target.Path)
	if strings.Contains(string(out), fenceStart) || strings.Contains(string(out), "section two") {
		t.Error("uninstall did not remove fenced section")
	}
	if !strings.Contains(string(out), "important stuff") {
		t.Error("user notes lost on uninstall")
	}
}

func TestOverwriteRefusesWithoutForce(t *testing.T) {
	dir := t.TempDir()
	target := skillTarget{
		Key:  "fixture",
		Path: filepath.Join(dir, "SKILL.md"),
		Mode: modeOverwrite,
		Body: func() string { return "v1" },
	}
	if err := installSkillTarget(target, false); err != nil {
		t.Fatal(err)
	}
	target.Body = func() string { return "v2" }
	if err := installSkillTarget(target, false); err == nil {
		t.Error("expected error on second install without --force")
	}
	if err := installSkillTarget(target, true); err != nil {
		t.Fatalf("force install failed: %v", err)
	}
	out, _ := os.ReadFile(target.Path)
	if string(out) != "v2" {
		t.Errorf("file content = %q; want %q", out, "v2")
	}
}
