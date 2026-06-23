package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateSyncURL(t *testing.T) {
	good := []string{
		"https://github.com/nick/aliases.git",
		"http://internal.host/repo.git",
		"ssh://git@github.com/nick/aliases.git",
		"git://host/repo.git",
		"git@github.com:nick/aliases.git",
		"deploy-bot@my.host.example:~/aliases",
	}
	for _, u := range good {
		if err := validateSyncURL(u, false); err != nil {
			t.Errorf("validateSyncURL(%q) = %v; want nil", u, err)
		}
	}

	bad := []string{
		"",
		"   ",
		"--upload-pack=evil",
		"-o=ProxyCommand=evil",
		"ext::sh -c whoami",
		"fd::17",
		"github.com/nick/aliases", // no scheme, not scp-like
	}
	for _, u := range bad {
		if err := validateSyncURL(u, false); err == nil {
			t.Errorf("validateSyncURL(%q) = nil; want error", u)
		}
	}

	// Local paths are gated behind allowLocal.
	if err := validateSyncURL("/tmp/repo.git", false); err == nil {
		t.Error("absolute path accepted without allowLocal")
	}
	if err := validateSyncURL("/tmp/repo.git", true); err != nil {
		t.Errorf("absolute path rejected with allowLocal: %v", err)
	}
	if err := validateSyncURL("file:///tmp/repo.git", true); err != nil {
		t.Errorf("file:// rejected with allowLocal: %v", err)
	}
}

func TestCLISyncInitRejectsBadURL(t *testing.T) {
	home := t.TempDir()
	for _, u := range []string{"ext::sh -c whoami", "--upload-pack=evil", "/tmp/some-repo"} {
		r := runAlien(t, home, "", "sync", "init", u)
		if r.code != 1 {
			t.Errorf("sync init %q: exit %d; want 1\nstderr: %s", u, r.code, r.stderr)
		}
	}
}

// ---------- fixture-based sync scenarios ----------

// setupSyncEnv points git away from the user's config and identity, and
// allows local-path remotes. runAlien inherits os.Environ(), so t.Setenv
// propagates to the spawned binaries.
func setupSyncEnv(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_AUTHOR_NAME", "alien-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "alien-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "alien-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "alien-test@example.com")
	t.Setenv("ALIEN_ALLOW_LOCAL_SYNC", "1")
}

func makeBareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	out, err := exec.Command("git", "init", "--bare", "--initial-branch=main", dir).CombinedOutput()
	if err != nil {
		// Older git without --initial-branch.
		out2, err2 := exec.Command("git", "init", "--bare", dir).CombinedOutput()
		if err2 != nil {
			t.Fatalf("git init --bare: %v\n%s\n%s", err2, out, out2)
		}
	}
	return dir
}

func gitInHome(t *testing.T, home string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", home}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestCLISyncInitEmptyRemote(t *testing.T) {
	setupSyncEnv(t)
	remote := makeBareRemote(t)
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")
	mustRun(t, home, "", "run", "gs") // creates usage.json
	appendHits(t, home, "gs")         // creates hits.log

	mustRun(t, home, "", "sync", "init", remote)

	// Only .gitignore and aliases.json are tracked — the M3 invariant:
	// usage data must never reach the remote.
	tracked := strings.Fields(gitInHome(t, home, "ls-files"))
	want := []string{".gitignore", "aliases.json"}
	if strings.Join(tracked, ",") != strings.Join(want, ",") {
		t.Errorf("tracked files = %v; want %v", tracked, want)
	}

	// Usage churn must not dirty the repo (auto-push would commit it).
	mustRun(t, home, "", "run", "gs")
	mustRun(t, home, "", "track", "flush")
	if status := strings.TrimSpace(gitInHome(t, home, "status", "--porcelain")); status != "" {
		t.Errorf("usage activity dirtied the sync repo:\n%s", status)
	}
}

func TestCLISyncAdoptPopulatedRemote(t *testing.T) {
	setupSyncEnv(t)
	remote := makeBareRemote(t)

	// Machine A publishes its aliases.
	homeA := t.TempDir()
	mustRun(t, homeA, "", "shared", "-c", "echo from-A")
	mustRun(t, homeA, "", "sync", "init", remote)

	// Machine B has its own local aliases and adopts the remote.
	homeB := t.TempDir()
	mustRun(t, homeB, "", "local", "-c", "echo from-B")
	r := mustRun(t, homeB, "", "sync", "init", remote)
	if !strings.Contains(r.stderr, "adopted") {
		t.Errorf("expected adoption message, got:\n%s", r.stderr)
	}

	// Remote aliases are live; the local store was backed up, not lost.
	if got := mustRun(t, homeB, "", "get", "shared").stdout; got != "echo from-A\n" {
		t.Errorf("adopted alias = %q", got)
	}
	backup, err := os.ReadFile(filepath.Join(homeB, "aliases.json.alien-backup"))
	if err != nil {
		t.Fatalf("local aliases not backed up: %v", err)
	}
	if !strings.Contains(string(backup), "from-B") {
		t.Errorf("backup lost local alias:\n%s", backup)
	}
}

func TestCLISyncPushPullRoundTrip(t *testing.T) {
	setupSyncEnv(t)
	remote := makeBareRemote(t)

	homeA := t.TempDir()
	mustRun(t, homeA, "", "one", "-c", "echo one")
	mustRun(t, homeA, "", "sync", "init", remote)

	homeB := t.TempDir()
	mustRun(t, homeB, "", "sync", "init", remote)
	if got := mustRun(t, homeB, "", "get", "one").stdout; got != "echo one\n" {
		t.Errorf("initial clone: get one = %q", got)
	}

	// A adds and pushes; B pulls and sees it, with aliases.sh regenerated.
	mustRun(t, homeA, "", "two", "-c", "echo two")
	mustRun(t, homeA, "", "sync", "push")
	mustRun(t, homeB, "", "sync", "pull")
	if got := mustRun(t, homeB, "", "get", "two").stdout; got != "echo two\n" {
		t.Errorf("after pull: get two = %q", got)
	}
	sh, err := os.ReadFile(filepath.Join(homeB, "aliases.sh"))
	if err != nil || !strings.Contains(string(sh), "alias two=") {
		t.Errorf("aliases.sh not regenerated after pull: %v\n%s", err, sh)
	}

	// Push with nothing to commit is a clean no-op.
	r := mustRun(t, homeB, "", "sync", "push")
	if !strings.Contains(r.stderr, "no local changes") {
		t.Errorf("no-op push message: %s", r.stderr)
	}
}

func TestCLISyncMaybePullThrottle(t *testing.T) {
	setupSyncEnv(t)
	remote := makeBareRemote(t)

	homeA := t.TempDir()
	mustRun(t, homeA, "", "one", "-c", "echo one")
	mustRun(t, homeA, "", "sync", "init", remote)

	homeB := t.TempDir()
	mustRun(t, homeB, "", "sync", "init", remote)
	mustRun(t, homeB, "", "sync", "auto", "on", "pull")

	// Publish a change from A that B hasn't seen.
	mustRun(t, homeA, "", "two", "-c", "echo two")
	mustRun(t, homeA, "", "sync", "push")

	// Fresh .last_pull → throttled, no pull.
	lastPull := filepath.Join(homeB, ".last_pull")
	if err := os.WriteFile(lastPull,
		[]byte(time.Now().UTC().Format(time.RFC3339)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, homeB, "", "sync", "maybe-pull")
	if r := runAlien(t, homeB, "", "get", "two"); r.code == 0 {
		t.Error("maybe-pull ignored the throttle")
	}

	// Stale .last_pull → pulls.
	if err := os.WriteFile(lastPull,
		[]byte(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, homeB, "", "sync", "maybe-pull")
	if got := mustRun(t, homeB, "", "get", "two").stdout; got != "echo two\n" {
		t.Errorf("after stale throttle: get two = %q", got)
	}
}

func TestCLISyncDivergenceConflictHint(t *testing.T) {
	setupSyncEnv(t)
	remote := makeBareRemote(t)

	homeA := t.TempDir()
	mustRun(t, homeA, "", "shared", "-c", "echo original")
	mustRun(t, homeA, "", "sync", "init", remote)

	homeB := t.TempDir()
	mustRun(t, homeB, "", "sync", "init", remote)

	// Both sides change the same alias; A wins the push race.
	mustRun(t, homeA, "", "shared", "-c", "echo from-A", "--force")
	mustRun(t, homeA, "", "sync", "push")
	mustRun(t, homeB, "", "shared", "-c", "echo from-B", "--force")
	gitInHome(t, homeB, "add", "aliases.json")
	gitInHome(t, homeB, "commit", "-m", "local divergent change")

	r := runAlien(t, homeB, "", "sync", "pull")
	if r.code != 1 {
		t.Fatalf("divergent pull: exit %d; want 1\nstderr: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "rebase --continue") {
		t.Errorf("conflict output missing recovery hint:\n%s", r.stderr)
	}
}
