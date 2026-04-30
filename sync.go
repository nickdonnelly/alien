package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SyncConfig is persisted at $ALIEN_HOME/sync.json. It records the user's
// remote and auto-sync preferences. The file is gitignored — it's local
// state, not part of the user's alias data.
type SyncConfig struct {
	RemoteURL       string `json:"remote_url"`
	AutoPull        bool   `json:"auto_pull"`
	AutoPush        bool   `json:"auto_push"`
	ThrottleSeconds int    `json:"throttle_seconds"`
}

func syncConfigPath() string { return filepath.Join(dataDir(), "sync.json") }
func lastPullPath() string   { return filepath.Join(dataDir(), ".last_pull") }

func loadSyncConfig() (*SyncConfig, error) {
	data, err := os.ReadFile(syncConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c SyncConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.ThrottleSeconds == 0 {
		c.ThrottleSeconds = 300
	}
	return &c, nil
}

func saveSyncConfig(c *SyncConfig) error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := syncConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, syncConfigPath())
}

// git runs `git -C <dataDir> <args...>` and returns combined output.
func gitRun(args ...string) (string, error) {
	full := append([]string{"-C", dataDir()}, args...)
	cmd := exec.Command("git", full...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	err := cmd.Run()
	return buf.String(), err
}

func gitRepoExists() bool {
	if _, err := os.Stat(filepath.Join(dataDir(), ".git")); err == nil {
		return true
	}
	return false
}

// writeGitignore allow-lists only the files we want tracked. Everything else
// in $ALIEN_HOME (generated aliases.sh, sync state, future side files) is
// ignored. The leading `*` plus negations is the standard "deny by default"
// pattern.
func writeGitignore() error {
	content := `# Managed by alien sync — track only aliases.json
*
!.gitignore
!aliases.json
`
	return os.WriteFile(filepath.Join(dataDir(), ".gitignore"), []byte(content), 0o644)
}

// ---------- subcommands ----------

func cmdSync(args []string) {
	if len(args) == 0 {
		cmdSyncStatus(nil)
		return
	}
	switch args[0] {
	case "init":
		cmdSyncInit(args[1:])
	case "push":
		cmdSyncPush(args[1:])
	case "pull":
		cmdSyncPull(args[1:])
	case "status", "info":
		cmdSyncStatus(args[1:])
	case "auto":
		cmdSyncAuto(args[1:])
	case "forget", "disconnect":
		cmdSyncForget(args[1:])
	case "maybe-pull":
		cmdSyncMaybePull(args[1:])
	case "maybe-push":
		cmdSyncMaybePush(args[1:])
	default:
		errorf("unknown subcommand: alien sync %s", args[0])
		fmt.Fprintln(os.Stderr, "  available: init, push, pull, status, auto, forget")
		os.Exit(1)
	}
}

func cmdSyncInit(args []string) {
	if len(args) < 1 {
		errorf("usage: alien sync init <repo-url>")
		os.Exit(1)
	}
	url := args[0]

	if _, err := exec.LookPath("git"); err != nil {
		errorf("git not found on $PATH; install git to use sync")
		os.Exit(1)
	}
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}

	// Initialize the repo only if needed; never blow away an existing .git.
	if !gitRepoExists() {
		if out, err := gitRun("init", "--initial-branch=main"); err != nil {
			// Older gits don't support --initial-branch; fall back.
			if out2, err2 := gitRun("init"); err2 != nil {
				errorf("git init: %v\n%s\n%s", err, out, out2)
				os.Exit(1)
			}
		}
	} else {
		warnf("git repo already initialized in %s — reusing", dataDir())
	}

	// Set or update remote.
	gitRun("remote", "remove", "origin") // ignore "no such remote"
	if out, err := gitRun("remote", "add", "origin", url); err != nil {
		errorf("set remote: %v\n%s", err, out)
		os.Exit(1)
	}

	if _, err := gitRun("fetch", "origin"); err != nil {
		warnf("could not contact remote yet — `alien sync push` once it's reachable")
		// Fall through and still save the config so the URL is recorded.
	} else if out, _ := gitRun("ls-remote", "--heads", "origin"); strings.TrimSpace(out) != "" {
		// Remote has content. Adopt it. If we have a local aliases.json that
		// would collide with the one in the remote tree, set it aside so the
		// user doesn't lose work — git checkout would otherwise refuse.
		aliasesPath := filepath.Join(dataDir(), "aliases.json")
		backupPath := aliasesPath + ".alien-backup"
		hasLocal := false
		if _, err := os.Stat(aliasesPath); err == nil {
			hasLocal = true
			if err := os.Rename(aliasesPath, backupPath); err != nil {
				errorf("back up local aliases: %v", err)
				os.Exit(1)
			}
		}
		// Same dance for our generated aliases.sh and any local .gitignore so
		// they don't trip the checkout.
		for _, p := range []string{
			filepath.Join(dataDir(), "aliases.sh"),
			filepath.Join(dataDir(), ".gitignore"),
		} {
			os.Remove(p)
		}

		if out, err := gitRun("checkout", "-b", "main", "origin/main"); err != nil {
			// Restore the backup if checkout failed so we don't leave the
			// user with no aliases.
			if hasLocal {
				os.Rename(backupPath, aliasesPath)
			}
			errorf("adopt remote: %v\n%s", err, out)
			os.Exit(1)
		}
		// Refresh the aliases.sh export from the freshly pulled aliases.json.
		if s, err := loadStore(); err == nil {
			_ = s.writeShellExport()
		}
		if hasLocal {
			warnf("your previous aliases.json was saved to %s — merge by hand if needed", backupPath)
		}
		successf("adopted aliases from %s", url)
	} else {
		// Empty remote: we're authoritative. Make sure .gitignore is present,
		// then commit and push our current state.
		if err := writeGitignore(); err != nil {
			errorf("write .gitignore: %v", err)
			os.Exit(1)
		}
		gitRun("add", ".gitignore", "aliases.json")
		gitRun("commit", "-m", "init alien aliases")
		if out, err := gitRun("push", "-u", "origin", "main"); err != nil {
			errorf("initial push: %v\n%s", err, out)
			os.Exit(1)
		}
		successf("initialized %s and pushed", url)
	}

	cfg, _ := loadSyncConfig()
	if cfg == nil {
		cfg = &SyncConfig{ThrottleSeconds: 300}
	}
	cfg.RemoteURL = url
	if err := saveSyncConfig(cfg); err != nil {
		errorf("save sync config: %v", err)
		os.Exit(1)
	}
	infof("sync configured. `alien sync auto on` to enable background sync.")
}

func cmdSyncPush(args []string) {
	if !gitRepoExists() {
		errorf("not a sync repo — run `alien sync init <url>` first")
		os.Exit(1)
	}
	var msg string
	for i := 0; i < len(args); i++ {
		if args[i] == "-m" || args[i] == "--message" {
			if i+1 < len(args) {
				msg = args[i+1]
				i++
			}
		}
	}
	if msg == "" {
		msg = defaultCommitMessage()
	}

	if out, err := gitRun("add", "aliases.json", ".gitignore"); err != nil {
		errorf("git add: %v\n%s", err, out)
		os.Exit(1)
	}
	// If nothing changed, `commit` exits 1; tolerate that.
	out, err := gitRun("commit", "-m", msg)
	if err != nil {
		if strings.Contains(out, "nothing to commit") {
			infof("no local changes to push")
		} else {
			errorf("git commit: %v\n%s", err, out)
			os.Exit(1)
		}
	} else {
		successf("committed: %s", msg)
	}

	out, err = gitRun("push")
	if err != nil {
		errorf("git push: %v\n%s", err, out)
		os.Exit(1)
	}
	successf("pushed to remote")
}

func cmdSyncPull(args []string) {
	if !gitRepoExists() {
		errorf("not a sync repo — run `alien sync init <url>` first")
		os.Exit(1)
	}
	out, err := gitRun("pull", "--rebase")
	if err != nil {
		errorf("git pull: %v\n%s", err, out)
		fmt.Fprintln(os.Stderr, "  resolve conflicts in aliases.json, then:")
		fmt.Fprintln(os.Stderr, "    git -C "+dataDir()+" rebase --continue")
		fmt.Fprintln(os.Stderr, "    alien sync push")
		os.Exit(1)
	}
	successf("pulled latest")
	// Refresh aliases.sh in case the pull changed aliases.json.
	if s, err := loadStore(); err == nil {
		_ = s.writeShellExport()
	}
	touchLastPull()
}

func cmdSyncStatus(_ []string) {
	cfg, _ := loadSyncConfig()
	fmt.Println()
	fmt.Printf("  %s %s\n", brcyan("👽"), bold("alien sync"))
	if !gitRepoExists() {
		fmt.Printf("  %s %s\n", gray("status :"), yellow("not configured"))
		fmt.Printf("  %s %s\n\n", gray("hint   :"), cyan("alien sync init <repo-url>"))
		return
	}
	fmt.Printf("  %s %s\n", gray("repo   :"), dataDir())
	if cfg != nil && cfg.RemoteURL != "" {
		fmt.Printf("  %s %s\n", gray("remote :"), cfg.RemoteURL)
	}
	if cfg != nil {
		auto := "off"
		if cfg.AutoPull || cfg.AutoPush {
			auto = fmt.Sprintf("on (pull=%v push=%v throttle=%ds)", cfg.AutoPull, cfg.AutoPush, cfg.ThrottleSeconds)
		}
		fmt.Printf("  %s %s\n", gray("auto   :"), auto)
	}
	out, _ := gitRun("status", "--short")
	if strings.TrimSpace(out) == "" {
		fmt.Printf("  %s %s\n", gray("state  :"), green("clean"))
	} else {
		fmt.Printf("  %s\n%s\n", gray("state  :"), out)
	}
	fmt.Println()
}

func cmdSyncAuto(args []string) {
	if len(args) < 1 {
		errorf("usage: alien sync auto on|off [pull|push|both]")
		os.Exit(1)
	}
	on := args[0] == "on"
	scope := "both"
	if len(args) > 1 {
		scope = args[1]
	}

	cfg, _ := loadSyncConfig()
	if cfg == nil {
		cfg = &SyncConfig{ThrottleSeconds: 300}
	}
	switch scope {
	case "pull":
		cfg.AutoPull = on
	case "push":
		cfg.AutoPush = on
	default:
		cfg.AutoPull = on
		cfg.AutoPush = on
	}
	if err := saveSyncConfig(cfg); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	if on {
		successf("auto-sync enabled (%s)", scope)
	} else {
		successf("auto-sync disabled (%s)", scope)
	}
}

func cmdSyncForget(_ []string) {
	if !gitRepoExists() {
		warnf("nothing to forget — sync is not configured")
		return
	}
	// Don't blindly rm -rf; ask the user.
	fmt.Fprintf(os.Stderr, "%s remove %s/.git and disable sync? [y/N] ",
		yellow("?"), dataDir())
	var ans string
	fmt.Scanln(&ans)
	if strings.ToLower(strings.TrimSpace(ans)) != "y" {
		infof("cancelled")
		return
	}
	if err := os.RemoveAll(filepath.Join(dataDir(), ".git")); err != nil {
		errorf("%v", err)
		os.Exit(1)
	}
	os.Remove(filepath.Join(dataDir(), ".gitignore"))
	os.Remove(syncConfigPath())
	os.Remove(lastPullPath())
	successf("sync disconnected")
}

// ---------- internal: maybe-pull / maybe-push ----------

// cmdSyncMaybePull is invoked from the shell startup hook. It pulls only if
// AutoPull is enabled and the throttle has elapsed since the last successful
// pull. Always exits 0 to never break the user's shell startup.
func cmdSyncMaybePull(args []string) {
	defer func() { recover() /* never crash the shell */ }()
	if !gitRepoExists() {
		return
	}
	cfg, _ := loadSyncConfig()
	if cfg == nil || !cfg.AutoPull {
		return
	}
	throttle := time.Duration(cfg.ThrottleSeconds) * time.Second
	if last := readLastPull(); time.Since(last) < throttle {
		return
	}
	out, err := gitRun("pull", "--rebase", "--quiet")
	if err != nil {
		// Don't spam stderr in normal use; only when --verbose.
		for _, a := range args {
			if a == "--verbose" || a == "-v" {
				warnf("auto-pull failed: %v\n%s", err, out)
			}
		}
		return
	}
	if s, err := loadStore(); err == nil {
		_ = s.writeShellExport()
	}
	touchLastPull()
}

// cmdSyncMaybePush is invoked by the shell function after a successful alien
// modification. It runs in the foreground but is fast (one git push); the
// shell can also background-call it with `&` if desired.
func cmdSyncMaybePush(_ []string) {
	defer func() { recover() }()
	if !gitRepoExists() {
		return
	}
	cfg, _ := loadSyncConfig()
	if cfg == nil || !cfg.AutoPush {
		return
	}
	out, _ := gitRun("status", "--porcelain")
	if strings.TrimSpace(out) == "" {
		return
	}
	gitRun("add", "aliases.json", ".gitignore")
	gitRun("commit", "-m", defaultCommitMessage())
	gitRun("push", "--quiet")
}

func defaultCommitMessage() string {
	return "alien: update aliases (" + time.Now().Format("2006-01-02 15:04") + ")"
}

func readLastPull() time.Time {
	data, err := os.ReadFile(lastPullPath())
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}
	}
	return t
}

func touchLastPull() {
	_ = os.WriteFile(lastPullPath(), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}
