package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Usage data lives in $ALIEN_HOME/usage.json, deliberately OUTSIDE the
// synced store. aliases.json is git-tracked by `alien sync`; if counts
// lived there, every flush would generate a sync commit and cross-machine
// count updates would rebase-conflict constantly. Counts are per-machine
// by design. The sync .gitignore is deny-by-default, so usage.json and
// hits.log are never tracked.
//
// Shell hooks append alias names to hits.log (one per line, O_APPEND);
// `alien track flush` claims the log by rename, aggregates it, and merges
// into usage.json under its own lock. Appending is ~free in the shell;
// the expensive merge happens rarely and never in the prompt path.

const usageVersion = 1

// dailyRetention bounds the per-day buckets kept per alias; older buckets
// fold away on save. 90 days is enough for 7/30-day trend math with slack.
const dailyRetention = 90 * 24 * time.Hour

type UsageEntry struct {
	Count    int            `json:"count"`
	LastUsed time.Time      `json:"last_used"`
	Daily    map[string]int `json:"daily,omitempty"` // "2006-01-02" -> hits
}

type UsageDB struct {
	Version int                   `json:"version"`
	Aliases map[string]UsageEntry `json:"aliases"`
}

func usagePath() string { return filepath.Join(dataDir(), "usage.json") }
func hitsPath() string  { return filepath.Join(dataDir(), "hits.log") }

func newUsageDB() *UsageDB {
	return &UsageDB{Version: usageVersion, Aliases: map[string]UsageEntry{}}
}

// loadUsage returns an empty DB when the file is missing. Unlike the alias
// store, a corrupt usage.json is not fatal — it's telemetry, so we start
// over rather than wedge every command that joins usage data.
func loadUsage() *UsageDB {
	u := newUsageDB()
	data, err := os.ReadFile(usagePath())
	if err != nil || len(data) == 0 {
		return u
	}
	if json.Unmarshal(data, u) != nil || u.Aliases == nil {
		return newUsageDB()
	}
	return u
}

func (u *UsageDB) save() error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	u.Version = usageVersion
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	tmp := usagePath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, usagePath())
}

// updateUsage is the safe path for read-modify-write on usage.json,
// mirroring updateStore. It uses its own sentinel so flushes never
// contend with alias edits.
func updateUsage(mutate func(u *UsageDB) error) error {
	return withUsageLock(func() error {
		u := loadUsage()
		if err := mutate(u); err != nil {
			return err
		}
		return u.save()
	})
}

// record adds n hits to an alias at time `at`, bumping the daily bucket and
// pruning buckets older than the retention window.
func (u *UsageDB) record(name string, n int, at time.Time) {
	e := u.Aliases[name]
	e.Count += n
	if at.After(e.LastUsed) {
		e.LastUsed = at
	}
	if e.Daily == nil {
		e.Daily = map[string]int{}
	}
	e.Daily[at.Format("2006-01-02")] += n
	cutoff := at.Add(-dailyRetention).Format("2006-01-02")
	for day := range e.Daily {
		if day < cutoff {
			delete(e.Daily, day)
		}
	}
	u.Aliases[name] = e
}

// usageCounts returns name -> total count, for read-time joins (ls --json,
// show, stats).
func usageCounts() map[string]int {
	u := loadUsage()
	out := make(map[string]int, len(u.Aliases))
	for n, e := range u.Aliases {
		out[n] = e.Count
	}
	return out
}

// ---------- alien track ----------

func cmdTrack(args []string) {
	if len(args) == 0 {
		errorf("usage: alien track flush")
		os.Exit(1)
	}
	switch args[0] {
	case "flush":
		// Runs from shell hooks: stay silent and always exit 0 so a tracking
		// hiccup can never break the user's shell (mirrors sync maybe-pull).
		_ = trackFlush()
	default:
		errorf("unknown subcommand: alien track %s", args[0])
		os.Exit(1)
	}
}

// trackFlush drains hits.log into usage.json. The log is claimed via rename
// so shell appenders are never blocked: writers open by path on every
// append, so a post-rename append simply recreates a fresh hits.log. Names
// no longer in the store are dropped (alias deleted since the hit).
func trackFlush() error {
	hp := hitsPath()
	claim := fmt.Sprintf("%s.%d", hp, os.Getpid())
	if err := os.Rename(hp, claim); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return adoptStaleClaims()
		}
		return err
	}
	if err := mergeHitsFile(claim); err != nil {
		return err
	}
	return adoptStaleClaims()
}

// adoptStaleClaims merges claim files left behind by a flush that died
// mid-merge. Only claims older than an hour are adopted — a younger one
// may belong to a flush that's still running.
func adoptStaleClaims() error {
	matches, err := filepath.Glob(hitsPath() + ".*")
	if err != nil {
		return err
	}
	var firstErr error
	for _, m := range matches {
		if strings.HasSuffix(m, ".tmp") {
			continue
		}
		fi, err := os.Stat(m)
		if err != nil || time.Since(fi.ModTime()) < time.Hour {
			continue
		}
		if err := mergeHitsFile(m); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// mergeHitsFile aggregates one claimed hits file into usage.json and
// removes it on success.
func mergeHitsFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			counts[name]++
		}
	}
	if len(counts) > 0 {
		known := map[string]bool{}
		if s, err := loadStore(); err == nil {
			for n := range s.Aliases {
				known[n] = true
			}
		}
		now := time.Now()
		if err := updateUsage(func(u *UsageDB) error {
			for name, n := range counts {
				if !known[name] {
					continue
				}
				u.record(name, n, now)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return os.Remove(path)
}
