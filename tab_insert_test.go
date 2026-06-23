package main

// Tests for the Tab-insert behavior of the shell picker hooks: Tab inserts the
// alias name by default, or the expanded command when ALIEN_TAB_INSERT=command.
// We can't drive fzf headlessly, so we source the hook and call the helper
// (_alien_tab_text) it uses directly.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tabInsertEnv builds the hooked-shell env. mode is the ALIEN_TAB_INSERT
// override; pass "" to leave it unset so the hook falls back to `alien config`.
func tabInsertEnv(home, mode string) []string {
	env := []string{
		"PATH=" + filepath.Dir(alienBin) + ":" + os.Getenv("PATH"),
		"ALIEN_HOME=" + home,
		"ALIEN_TRACK=0",  // keep the prompt/preexec hooks out of the way
		"ALIEN_KEYBIND=", // no zle/readline binding needed
		"HOME=" + home,
		"TERM=dumb",
		"NO_COLOR=1",
	}
	if mode != "" {
		env = append(env, "ALIEN_TAB_INSERT="+mode)
	}
	return env
}

func TestZshTabInsert(t *testing.T) {
	zsh := lookShell(t, "zsh")
	home := t.TempDir()
	mustRun(t, home, "", "ll", "-c", "ls -al")

	script := `eval "$(alien init zsh)"
print -r -- "TAB:$(_alien_tab_text ll)"
exit
`
	for mode, want := range map[string]string{"name": "ll", "command": "ls -al"} {
		out := runHookedShell(t, zsh, []string{"-i"}, tabInsertEnv(home, mode), script)
		if got := grepTab(out); got != want {
			t.Errorf("zsh ALIEN_TAB_INSERT=%s: inserted %q; want %q\noutput:\n%s", mode, got, want, out)
		}
	}
}

func TestBashTabInsert(t *testing.T) {
	bash := lookShell(t, "bash")
	home := t.TempDir()
	mustRun(t, home, "", "ll", "-c", "ls -al")

	script := `eval "$(alien init bash)"
printf 'TAB:%s\n' "$(_alien_tab_text ll)"
exit
`
	for mode, want := range map[string]string{"name": "ll", "command": "ls -al"} {
		out := runHookedShell(t, bash, []string{"--norc", "-i"}, tabInsertEnv(home, mode), script)
		if got := grepTab(out); got != want {
			t.Errorf("bash ALIEN_TAB_INSERT=%s: inserted %q; want %q\noutput:\n%s", mode, got, want, out)
		}
	}
}

// grepTab pulls the value after the "TAB:" marker. zsh prints its prompt on
// the same physical line, so the marker can be mid-line — match anywhere. An
// interactive bash echoes the command first, so the marker also appears in the
// echoed `printf 'TAB:...'` line; the real output is the last match.
// With no ALIEN_TAB_INSERT override, the hook must honor the stored config.
func TestZshTabInsertFromConfig(t *testing.T) {
	zsh := lookShell(t, "zsh")
	home := t.TempDir()
	mustRun(t, home, "", "ll", "-c", "ls -al")
	mustRun(t, home, "", "config", "set", "tab-insert", "command")

	script := `eval "$(alien init zsh)"
print -r -- "TAB:$(_alien_tab_text ll)"
exit
`
	out := runHookedShell(t, zsh, []string{"-i"}, tabInsertEnv(home, ""), script)
	if got := grepTab(out); got != "ls -al" {
		t.Errorf("config tab-insert=command: inserted %q; want %q\noutput:\n%s", got, "ls -al", out)
	}
}

func grepTab(out string) string {
	got := ""
	for _, line := range strings.Split(out, "\n") {
		if i := strings.LastIndex(line, "TAB:"); i >= 0 {
			got = strings.TrimRight(line[i+len("TAB:"):], " \t\r")
		}
	}
	return got
}

func lookShell(t *testing.T, shell string) string {
	t.Helper()
	p, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s not installed", shell)
	}
	return p
}
