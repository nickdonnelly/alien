package main

// End-to-end tests for the shell hit-capture hooks. They drive a real
// interactive zsh/bash with a scripted stdin and an isolated ALIEN_HOME,
// then assert the hits landed in usage.json. Skipped when the shell isn't
// installed.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNamesFileGeneration(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")
	mustRun(t, home, "", "off", "-c", "echo off")
	mustRun(t, home, "", "disable", "off")

	data, err := os.ReadFile(filepath.Join(home, "names.txt"))
	if err != nil {
		t.Fatalf("names.txt not written: %v", err)
	}
	names := strings.Fields(string(data))
	if len(names) != 1 || names[0] != "gs" {
		t.Errorf("names.txt = %q; want just gs (disabled aliases excluded)", names)
	}
}

// shellEnv builds the environment for a hooked shell: alien on PATH, an
// isolated ALIEN_HOME, tracking on.
func shellEnv(home string) []string {
	env := []string{
		"PATH=" + filepath.Dir(alienBin) + ":" + os.Getenv("PATH"),
		"ALIEN_HOME=" + home,
		"ALIEN_TRACK=1",
		"ALIEN_KEYBIND=", // no readline/zle binding needed
		"HOME=" + home,   // keep the shell away from real rc files
		"TERM=dumb",
		"NO_COLOR=1",
	}
	return env
}

func runHookedShell(t *testing.T, shell string, args []string, env []string, script string) string {
	t.Helper()
	cmd := exec.Command(shell, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Interactive shells fed by pipe can exit non-zero on EOF; only the
		// tracking assertions below matter. Log for diagnosis.
		t.Logf("%s exited: %v\noutput:\n%s", shell, err, out.String())
	}
	return out.String()
}

func TestZshHitCapture(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	home := t.TempDir()
	mustRun(t, home, "", "ll", "-c", "ls -1")

	// ZDOTDIR keeps zsh away from the user's real rc files; its .zshrc
	// loads the hook exactly as the README instructs.
	zdot := t.TempDir()
	rc := "source <(alien init zsh)\n"
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte(rc), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(shellEnv(home), "ZDOTDIR="+zdot)

	out := runHookedShell(t, zsh, []string{"-i"}, env,
		"ll >/dev/null 2>&1\nll >/dev/null 2>&1\ntrue\nexit\n")

	// The hits may still be in hits.log (the startup flush ran before the
	// commands) — flush and check the join.
	mustRun(t, home, "", "track", "flush")
	if e := findEntry(t, listJSON(t, home), "ll"); e.UsedCount != 2 {
		t.Errorf("zsh capture: used_count = %d; want 2\nshell output:\n%s", e.UsedCount, out)
	}
}

func TestBashHitCapture(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	home := t.TempDir()
	mustRun(t, home, "", "ll", "-c", "ls -1")

	// --norc + explicit eval keeps bash away from the user's rc files.
	// The hook needs interactive mode (-i) for PROMPT_COMMAND and history.
	script := `eval "$(alien init bash)"
ll >/dev/null 2>&1
ll >/dev/null 2>&1
true
exit
`
	out := runHookedShell(t, bash, []string{"--norc", "-i"}, shellEnv(home), script)

	mustRun(t, home, "", "track", "flush")
	if e := findEntry(t, listJSON(t, home), "ll"); e.UsedCount != 2 {
		t.Errorf("bash capture: used_count = %d; want 2\nshell output:\n%s", e.UsedCount, out)
	}
}

// ALIEN_TRACK=0 must disable capture entirely.
func TestZshTrackOptOut(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not installed")
	}
	home := t.TempDir()
	mustRun(t, home, "", "ll", "-c", "ls -1")

	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"),
		[]byte("source <(alien init zsh)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(shellEnv(home), "ZDOTDIR="+zdot)
	for i, e := range env {
		if e == "ALIEN_TRACK=1" {
			env[i] = "ALIEN_TRACK=0"
		}
	}

	runHookedShell(t, zsh, []string{"-i"}, env, "ll >/dev/null 2>&1\nexit\n")

	// Give any stray background flush a moment, then assert nothing landed.
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(home, "hits.log")); err == nil {
		t.Error("ALIEN_TRACK=0 still wrote hits.log")
	}
	mustRun(t, home, "", "track", "flush")
	if e := findEntry(t, listJSON(t, home), "ll"); e.UsedCount != 0 {
		t.Errorf("opt-out: used_count = %d; want 0", e.UsedCount)
	}
}
