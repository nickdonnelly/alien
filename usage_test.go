package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func readUsageFile(t *testing.T, home string) UsageDB {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var u UsageDB
	if err := json.Unmarshal(data, &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func appendHits(t *testing.T, home string, names ...string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(home, "hits.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, n := range names {
		fmt.Fprintln(f, n)
	}
}

func TestUsageRecord(t *testing.T) {
	u := newUsageDB()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	u.record("gs", 2, now)
	u.record("gs", 1, now.Add(time.Hour))

	e := u.Aliases["gs"]
	if e.Count != 3 {
		t.Errorf("Count = %d; want 3", e.Count)
	}
	if !e.LastUsed.Equal(now.Add(time.Hour)) {
		t.Errorf("LastUsed = %v", e.LastUsed)
	}
	if e.Daily["2026-06-10"] != 3 {
		t.Errorf("Daily = %v", e.Daily)
	}

	// Buckets past the retention window fold away.
	old := now.Add(-91 * 24 * time.Hour)
	u.record("gs", 1, old) // creates an old bucket
	u.record("gs", 1, now) // recording at `now` prunes it
	if _, ok := u.Aliases["gs"].Daily[old.Format("2006-01-02")]; ok {
		t.Errorf("stale daily bucket survived pruning: %v", u.Aliases["gs"].Daily)
	}
}

// v1 stores with used_count values migrate them into usage.json; the synced
// file drops the field on the next write.
func TestCLIUsageMigrationV1toV2(t *testing.T) {
	home := t.TempDir()
	seedStore(t, home, `{"version":1,"aliases":{
		"gs":{"command":"git status","enabled":true,"used_count":5,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-02-01T00:00:00Z"},
		"gl":{"command":"git log","enabled":true,"created_at":"2026-01-01T00:00:00Z"}}}`)

	// Counts survive the move and are visible through the join.
	if e := findEntry(t, listJSON(t, home), "gs"); e.UsedCount != 5 {
		t.Errorf("migrated used_count = %d; want 5", e.UsedCount)
	}
	u := readUsageFile(t, home)
	if u.Aliases["gs"].Count != 5 {
		t.Errorf("usage.json count = %d; want 5", u.Aliases["gs"].Count)
	}
	if _, ok := u.Aliases["gl"]; ok {
		t.Error("zero-count alias seeded into usage.json")
	}

	// Repeated reads must not double the count (migration is idempotent).
	mustRun(t, home, "", "ls", "--json")
	if u := readUsageFile(t, home); u.Aliases["gs"].Count != 5 {
		t.Errorf("re-migration changed count to %d", u.Aliases["gs"].Count)
	}

	// A write stamps v2 and drops the legacy field.
	mustRun(t, home, "", "comment", "gs", "x")
	raw := readStoreFile(t, home)
	if !strings.Contains(raw, `"version": 2`) {
		t.Errorf("store not stamped v2:\n%s", raw)
	}
	if strings.Contains(raw, "used_count") {
		t.Errorf("legacy used_count still written:\n%s", raw)
	}

	// New uses keep accumulating on top of the migrated baseline.
	mustRun(t, home, "", "run", "gs")
	if e := findEntry(t, listJSON(t, home), "gs"); e.UsedCount != 6 {
		t.Errorf("post-migration run: used_count = %d; want 6", e.UsedCount)
	}
}

func TestCLITrackFlush(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")
	appendHits(t, home, "gs", "gs", "ghost", "gs")

	mustRun(t, home, "", "track", "flush")
	if e := findEntry(t, listJSON(t, home), "gs"); e.UsedCount != 3 {
		t.Errorf("flushed count = %d; want 3", e.UsedCount)
	}
	// Unknown names are dropped, the log is consumed.
	u := readUsageFile(t, home)
	if _, ok := u.Aliases["ghost"]; ok {
		t.Error("hit for deleted/unknown alias was recorded")
	}
	if _, err := os.Stat(filepath.Join(home, "hits.log")); !os.IsNotExist(err) {
		t.Error("hits.log not consumed by flush")
	}

	// Flush with no log is a silent no-op.
	mustRun(t, home, "", "track", "flush")

	// Hits appended after a flush land in a fresh log and a later flush
	// picks them up.
	appendHits(t, home, "gs")
	mustRun(t, home, "", "track", "flush")
	if e := findEntry(t, listJSON(t, home), "gs"); e.UsedCount != 4 {
		t.Errorf("second flush: count = %d; want 4", e.UsedCount)
	}
}

// Concurrent appenders racing a flush must never lose hits: every hit ends
// up either in usage.json or still pending in hits.log.
func TestCLITrackFlushConcurrent(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")

	const writers = 4
	const hitsPerWriter = 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < hitsPerWriter; i++ {
				f, err := os.OpenFile(filepath.Join(home, "hits.log"),
					os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					t.Error(err)
					return
				}
				fmt.Fprintln(f, "gs")
				f.Close()
			}
		}()
	}
	// Flush repeatedly while writers are appending.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				runAlien(t, home, "", "track", "flush")
			}
		}
	}()
	wg.Wait()
	close(done)
	mustRun(t, home, "", "track", "flush") // final drain

	if e := findEntry(t, listJSON(t, home), "gs"); e.UsedCount != writers*hitsPerWriter {
		t.Errorf("count = %d; want %d (hits lost in the rename race)",
			e.UsedCount, writers*hitsPerWriter)
	}
}

// The sync .gitignore is deny-by-default: usage.json and hits.log must
// never become sync-tracked, or every flush would generate commits.
func TestSyncGitignoreExcludesUsage(t *testing.T) {
	if err := writeGitignoreContentCheck(); err != nil {
		t.Fatal(err)
	}
}

func writeGitignoreContentCheck() error {
	// writeGitignore writes to dataDir(); steer it into a temp dir.
	old := os.Getenv("ALIEN_HOME")
	dir, err := os.MkdirTemp("", "alien-gitignore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	os.Setenv("ALIEN_HOME", dir)
	defer os.Setenv("ALIEN_HOME", old)

	if err := writeGitignore(); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, "*\n") {
		return fmt.Errorf(".gitignore lost its deny-by-default rule:\n%s", content)
	}
	for _, banned := range []string{"!usage.json", "!hits.log"} {
		if strings.Contains(content, banned) {
			return fmt.Errorf(".gitignore allow-lists %s:\n%s", banned, content)
		}
	}
	return nil
}

func TestCLIStatsJSON(t *testing.T) {
	home := t.TempDir()
	mustRun(t, home, "", "gs", "-c", "git status")
	mustRun(t, home, "", "gl", "-c", "git log")
	mustRun(t, home, "", "run", "gs")
	mustRun(t, home, "", "run", "gs")
	appendHits(t, home, "gs") // stats flushes pending hits itself

	r := mustRun(t, home, "", "stats", "--json")
	var payload struct {
		Totals struct {
			TrackedInvocations int `json:"tracked_invocations"`
		} `json:"totals"`
		MostUsed []struct {
			Name      string `json:"name"`
			UsedCount int    `json:"used_count"`
			Last7Days int    `json:"last_7_days"`
			Trend     string `json:"trend"`
		} `json:"most_used"`
		NeverUsed []struct {
			Name string `json:"name"`
		} `json:"never_used"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("parse stats --json: %v\n%s", err, r.stdout)
	}
	if payload.Totals.TrackedInvocations != 3 {
		t.Errorf("tracked_invocations = %d; want 3", payload.Totals.TrackedInvocations)
	}
	if len(payload.MostUsed) != 1 || payload.MostUsed[0].Name != "gs" ||
		payload.MostUsed[0].UsedCount != 3 {
		t.Errorf("most_used = %+v", payload.MostUsed)
	}
	if payload.MostUsed[0].Last7Days != 3 || payload.MostUsed[0].Trend != "rising" {
		t.Errorf("trend fields = %+v", payload.MostUsed[0])
	}
	if len(payload.NeverUsed) != 1 || payload.NeverUsed[0].Name != "gl" {
		t.Errorf("never_used = %+v", payload.NeverUsed)
	}
}
