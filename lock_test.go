//go:build !windows

package main

import (
	"sync"
	"testing"
)

// TestStoreLockSerializesUpdates spawns N goroutines that each increment
// a counter via updateStore and verifies no updates are lost. Without the
// lock we'd see lost-update races; with the lock the final count is exact.
func TestStoreLockSerializesUpdates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ALIEN_HOME", dir)

	if err := updateStore(func(s *Store) error {
		s.Aliases["counter"] = Alias{Command: "echo 0", Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if err := updateStore(func(s *Store) error {
				a := s.Aliases["counter"]
				a.UsedCount++
				s.Aliases["counter"] = a
				return nil
			}); err != nil {
				t.Errorf("updateStore: %v", err)
			}
		}()
	}
	wg.Wait()

	s, err := loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Aliases["counter"].UsedCount; got != N {
		t.Errorf("UsedCount = %d; want %d (lost updates)", got, N)
	}
}

// Note: this test exercises in-process goroutines, not separate
// processes. The flock advisory lock covers both cases — same-process
// goroutines share the file descriptor's lock, so this is a legitimate
// (if friendlier) test of the lock's correctness.
