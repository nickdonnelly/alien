package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Alias struct {
	Command   string    `json:"command"`
	Comment   string    `json:"comment,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	UsedCount int       `json:"used_count,omitempty"`

	// Source identifies where the alias came from. The empty string is the
	// default ("user-managed"). "shell" means it was imported from the user's
	// running shell (their rc files); we surface it in the picker but never
	// write it to aliases.sh because the rc already defines it. "pack:<name>"
	// means it was installed via `alien ufo install <name>` and will be
	// removed cleanly by `alien ufo uninstall <name>`.
	Source string `json:"source,omitempty"`

	// From is a human-readable origin label used in the picker's "FROM"
	// column. For shell-imported entries this is the rc file or oh-my-zsh
	// plugin where we found the `alias <name>=` definition (e.g. ".zshrc"
	// or "omz:git"). For pack entries it stays empty — the badge already
	// shows the pack name. For user entries it stays empty.
	From string `json:"from,omitempty"`

	Tags []string `json:"tags,omitempty"`
}

// InstalledPack records the metadata of a pack install so we can do a clean
// uninstall later (delete only AliasNames, leaving user-renamed copies alone
// — those have their own Source string).
type InstalledPack struct {
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	Description string    `json:"description,omitempty"`
	Source      string    `json:"source,omitempty"` // origin: builtin, file path, URL
	InstalledAt time.Time `json:"installed_at"`
	AliasNames  []string  `json:"alias_names"`
}

type Store struct {
	Version int                      `json:"version"`
	Aliases map[string]Alias         `json:"aliases"`
	Packs   map[string]InstalledPack `json:"packs,omitempty"`

	path string
}

const storeVersion = 1

// migration is a one-step upgrade applied when an on-disk store has
// Version == From. After it returns, the store's Version is bumped to
// From+1; the migration runner walks all migrations in order until the
// store reaches `storeVersion`.
type migration struct {
	From int
	Apply func(s *Store) error
}

// migrations lists every schema upgrade we've ever shipped. New entries
// only — never reorder, never delete, never edit. Bump `storeVersion`
// when you add one.
//
// To add a migration: append `{From: N, Apply: func(s *Store) error { ... }}`
// where N is the *current* storeVersion before your change, then bump
// `storeVersion` to N+1.
var migrations = []migration{
	// {From: 1, Apply: func(s *Store) error { ... }},
}

// runMigrations walks `migrations` until the store is at storeVersion.
// Returns true if anything was applied so the caller knows to persist.
func runMigrations(s *Store) (changed bool, err error) {
	for s.Version < storeVersion {
		applied := false
		for _, m := range migrations {
			if m.From == s.Version {
				if err := m.Apply(s); err != nil {
					return changed, fmt.Errorf("migrate v%d→v%d: %w", m.From, m.From+1, err)
				}
				s.Version = m.From + 1
				applied = true
				changed = true
				break
			}
		}
		if !applied {
			// No migration registered to leave this version. Nothing we
			// can do automatically — surface it so the user knows their
			// store is older than the binary expects.
			return changed, fmt.Errorf("no migration registered for store version %d", s.Version)
		}
	}
	return changed, nil
}

func dataDir() string {
	if d := os.Getenv("ALIEN_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "alien")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".alien"
	}
	return filepath.Join(home, ".config", "alien")
}

func storePath() string  { return filepath.Join(dataDir(), "aliases.json") }
func exportPath() string { return filepath.Join(dataDir(), "aliases.sh") }

// updateStore is the safe path for any read-modify-write on the alias store.
// It holds an advisory lock around load+mutate+save so concurrent alien
// processes don't lose each other's writes. Pure reads (cmdList, cmdShow,
// cmdGet, cmdFzfList, ...) can keep using the bare loadStore — there's
// nothing to lose.
func updateStore(mutate func(s *Store) error) error {
	return withStoreLock(func() error {
		s, err := loadStore()
		if err != nil {
			return err
		}
		if err := mutate(s); err != nil {
			return err
		}
		return s.save()
	})
}

func loadStore() (*Store, error) {
	p := storePath()
	s := &Store{Version: storeVersion, Aliases: map[string]Alias{}, path: p}

	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if s.Aliases == nil {
		s.Aliases = map[string]Alias{}
	}
	if s.Packs == nil {
		s.Packs = map[string]InstalledPack{}
	}
	s.path = p

	// A v0 file (no `version` key) is interpreted as v1 — that's our
	// initial schema, predating this comment. Anything newer than the
	// binary expects is fatal: the user has a newer alien somewhere.
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Version > storeVersion {
		return nil, fmt.Errorf("store version %d is newer than this binary supports (v%d)",
			s.Version, storeVersion)
	}
	if _, err := runMigrations(s); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	s.Version = storeVersion
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	return s.writeShellExport()
}

// writeShellExport regenerates aliases.sh with one `alias x='y'` line per
// enabled entry. The shell integration sources this file after every alien
// invocation so new aliases are immediately active.
func (s *Store) writeShellExport() error {
	out := exportPath()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	fmt.Fprintln(f, "# Generated by alien. Do not edit by hand.")
	for _, name := range s.sortedNames() {
		a := s.Aliases[name]
		if !a.Enabled {
			continue
		}
		// Shell-source entries already exist in the user's rc; re-emitting
		// them would be redundant and would conflict with their own quoting.
		if a.Source == "shell" {
			continue
		}
		fmt.Fprintf(f, "alias %s=%s\n", name, shellQuote(a.Command))
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

func (s *Store) sortedNames() []string {
	names := make([]string, 0, len(s.Aliases))
	for n := range s.Aliases {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// shellQuote single-quotes a string for safe inclusion in `alias x=...`.
// Single quotes inside the string are escaped via the standard '\'' trick.
func shellQuote(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
		} else {
			out = append(out, s[i])
		}
	}
	out = append(out, '\'')
	return string(out)
}
