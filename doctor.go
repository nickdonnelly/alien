package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// cmdDoctor runs a series of self-diagnostic checks and reports the result
// of each. Designed to be the first thing a user runs when alien "isn't
// working" — covers the common failure modes: missing fzf, hook not
// sourced, store unwritable, sync misconfigured, oh-my-zsh layout quirks.
func cmdDoctor(args []string) {
	_, wantJSON := extractBoolFlag(args, "--json")

	checks := []checkResult{}
	checks = append(checks, checkBinary())
	checks = append(checks, checkStorePath())
	checks = append(checks, checkStoreLoads())
	checks = append(checks, checkFzf())
	checks = append(checks, checkShellHook())
	checks = append(checks, checkGitForSync())
	checks = append(checks, checkSyncConfig())
	checks = append(checks, checkSkillTargets())

	if wantJSON {
		data, _ := json.MarshalIndent(checks, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Println()
	fmt.Printf("  %s %s\n\n", brcyan("👽"), bold("alien doctor"))
	worst := levelOK
	for _, r := range checks {
		fmt.Println(r.render())
		if r.Level > worst {
			worst = r.Level
		}
	}
	fmt.Println()
	switch worst {
	case levelOK:
		fmt.Printf("  %s all checks passed.\n", green("✓"))
	case levelWarn:
		fmt.Printf("  %s issues detected — alien works, but features above are degraded.\n", yellow("!"))
	case levelFail:
		fmt.Printf("  %s problems found — fix the items marked %s above.\n", red("✗"), red("✗"))
	}
	fmt.Println()
	if worst == levelFail {
		os.Exit(1)
	}
}

type level int

const (
	levelOK level = iota
	levelWarn
	levelFail
)

type checkResult struct {
	Name   string `json:"name"`
	Level  level  `json:"level"` // 0=ok, 1=warn, 2=fail
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func ok(name, detail string) checkResult { return checkResult{Name: name, Level: levelOK, Detail: detail} }
func warn(name, detail, hint string) checkResult {
	return checkResult{Name: name, Level: levelWarn, Detail: detail, Hint: hint}
}
func fail(name, detail, hint string) checkResult {
	return checkResult{Name: name, Level: levelFail, Detail: detail, Hint: hint}
}

func (r checkResult) render() string {
	var glyph string
	switch r.Level {
	case levelOK:
		glyph = green("✓")
	case levelWarn:
		glyph = yellow("!")
	case levelFail:
		glyph = red("✗")
	}
	out := fmt.Sprintf("  %s %s", glyph, padRight(r.Name, 22))
	if r.Detail != "" {
		out += "  " + r.Detail
	}
	if r.Hint != "" {
		out += "\n      " + dim("→ "+r.Hint)
	}
	return out
}

// ---------- individual checks ----------

func checkBinary() checkResult {
	exe, err := os.Executable()
	if err != nil {
		return warn("binary path", "could not resolve", "")
	}
	return ok("binary path", fmt.Sprintf("%s (alien %s)", exe, version))
}

func checkStorePath() checkResult {
	p := storePath()
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail("store directory", err.Error(), "fix permissions on "+dir)
	}
	// Try to open the file for write to confirm it's writable.
	test := filepath.Join(dir, ".doctor-write-check")
	if err := os.WriteFile(test, []byte("ok"), 0o644); err != nil {
		return fail("store directory", "not writable: "+err.Error(),
			"check ownership/permissions of "+dir)
	}
	os.Remove(test)
	return ok("store directory", dir)
}

func checkStoreLoads() checkResult {
	s, err := loadStore()
	if err != nil {
		return fail("aliases.json", err.Error(),
			"the file may be corrupt or from a newer alien — back it up and inspect")
	}
	return ok("aliases.json", fmt.Sprintf("v%d, %d aliases, %d packs", s.Version, len(s.Aliases), len(s.Packs)))
}

func checkFzf() checkResult {
	path, err := exec.LookPath("fzf")
	if err != nil {
		return warn("fzf", "not found on $PATH",
			"install fzf for the picker (https://github.com/junegunn/fzf)")
	}
	return ok("fzf", path)
}

// checkShellHook tries to detect whether the current shell sources alien's
// init script. It looks for any of the alien hook patterns in the user's
// rc files. False negatives are possible if the user has wired things in
// an unusual way; we report a warn (not a fail) accordingly.
func checkShellHook() checkResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return warn("shell hook", "could not find $HOME", "")
	}
	candidates := []string{".zshrc", ".bashrc", ".bash_profile", ".profile"}
	patterns := []string{
		"alien init zsh",
		"alien init bash",
		"alien skill",
	}
	for _, rel := range candidates {
		p := filepath.Join(home, rel)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, pat := range patterns[:2] {
			if strings.Contains(string(data), pat) {
				return ok("shell hook", "found in "+rel)
			}
		}
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	hint := ""
	switch shell {
	case "zsh":
		hint = `add: source <(alien init zsh)  to ~/.zshrc`
	case "bash":
		hint = `add: eval "$(alien init bash)"  to ~/.bashrc`
	default:
		hint = "wire the alien init output into your shell's rc"
	}
	return warn("shell hook", "not detected in any rc file", hint)
}

func checkGitForSync() checkResult {
	if _, err := loadSyncConfig(); err != nil || !gitRepoExists() {
		return ok("git for sync", "not configured — skipping")
	}
	path, err := exec.LookPath("git")
	if err != nil {
		return fail("git for sync", "git not found on $PATH",
			"install git or run `alien sync forget`")
	}
	return ok("git for sync", path)
}

func checkSyncConfig() checkResult {
	cfg, err := loadSyncConfig()
	if err != nil {
		return warn("sync config", err.Error(), "delete and re-run `alien sync init`")
	}
	if cfg == nil {
		return ok("sync", "not configured")
	}
	if cfg.RemoteURL == "" {
		return warn("sync", "config exists but no remote URL", "run `alien sync init <repo-url>`")
	}
	return ok("sync", fmt.Sprintf("remote=%s, auto=pull:%v push:%v", cfg.RemoteURL, cfg.AutoPull, cfg.AutoPush))
}

// checkSkillTargets reports which agent-skill targets currently have the
// alien skill installed. Purely informational — it's fine to have none.
func checkSkillTargets() checkResult {
	installed := installedSkillTargets()
	if len(installed) == 0 {
		return ok("agent skills", fmt.Sprintf("none installed (run %s to add one)", cyan("alien skill install")))
	}
	return ok("agent skills", strings.Join(installed, ", "))
}

// installedSkillTargets is defined in skill.go; declaring it here would be
// redundant. We keep `runtime` imported in case future doctor checks
// branch on GOOS.
var _ = runtime.GOOS
