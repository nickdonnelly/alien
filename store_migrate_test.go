package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMigrationsChain(t *testing.T) {
	var order []int
	ms := []migration{
		{From: 1, Apply: func(s *Store) error { order = append(order, 1); return nil }},
		{From: 2, Apply: func(s *Store) error { order = append(order, 2); return nil }},
	}
	s := &Store{Version: 1, Aliases: map[string]Alias{}}
	changed, err := runMigrationsWith(s, ms, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("changed = false; want true")
	}
	if s.Version != 3 {
		t.Errorf("Version = %d; want 3", s.Version)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("migrations ran out of order: %v", order)
	}
}

func TestRunMigrationsAlreadyCurrent(t *testing.T) {
	ms := []migration{
		{From: 1, Apply: func(s *Store) error { t.Error("migration ran on current store"); return nil }},
	}
	s := &Store{Version: 2}
	changed, err := runMigrationsWith(s, ms, 2)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("changed = true on an already-current store")
	}
}

func TestRunMigrationsGap(t *testing.T) {
	// v1 store, target v3, but only the 2→3 step is registered.
	ms := []migration{
		{From: 2, Apply: func(s *Store) error { return nil }},
	}
	s := &Store{Version: 1}
	_, err := runMigrationsWith(s, ms, 3)
	if err == nil || !strings.Contains(err.Error(), "no migration registered") {
		t.Errorf("gap: err = %v; want 'no migration registered'", err)
	}
}

func TestRunMigrationsApplyError(t *testing.T) {
	boom := errors.New("boom")
	ms := []migration{
		{From: 1, Apply: func(s *Store) error { return boom }},
	}
	s := &Store{Version: 1}
	changed, err := runMigrationsWith(s, ms, 2)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v; want wrapped boom", err)
	}
	if changed {
		t.Error("changed = true after a failed migration")
	}
	if s.Version != 1 {
		t.Errorf("Version advanced to %d despite failure", s.Version)
	}
}

// The persistence policy: loadStore migrates in memory only; the migrated
// form reaches disk on the first write because save() stamps storeVersion.
// Exercised end-to-end through the binary: a v0 file stays v0 on disk after
// reads, and is stamped to the current version after any mutation.
func TestCLIMigrationPersistsOnWrite(t *testing.T) {
	home := t.TempDir()
	v0 := `{"aliases":{"gs":{"command":"git status","enabled":true,"created_at":"2026-01-01T00:00:00Z"}}}`
	seedStore(t, home, v0)

	// A pure read must not rewrite the file.
	mustRun(t, home, "", "ls", "--json")
	if raw := readStoreFile(t, home); strings.Contains(raw, `"version"`) {
		t.Errorf("pure read persisted a version stamp:\n%s", raw)
	}

	// Any write stamps the current schema version.
	mustRun(t, home, "", "comment", "gs", "checked")
	stamp := fmt.Sprintf(`"version": %d`, storeVersion)
	if raw := readStoreFile(t, home); !strings.Contains(raw, stamp) {
		t.Errorf("write did not stamp storeVersion:\n%s", raw)
	}
}

func readStoreFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "aliases.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
