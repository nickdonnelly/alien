//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// withStoreLock guards any load-mutate-save sequence on aliases.json so two
// concurrent alien processes don't lose each other's writes (e.g. an
// interactive `alien add` racing the background `alien sync maybe-push`).
// withUsageLock guards usage.json the same way, on its own sentinel so
// usage flushes never contend with alias edits.
func withStoreLock(fn func() error) error { return withLock(".lock", fn) }
func withUsageLock(fn func() error) error { return withLock(".usage.lock", fn) }

// withLock acquires an exclusive advisory lock on the named sentinel file in
// $ALIEN_HOME, runs fn, and releases the lock.
//
// We lock a separate sentinel rather than the data file itself so the
// existing atomic write pattern (write `.tmp`, rename) still works without
// fighting the lock semantics.
//
// flock(2) advisory locks are released automatically when the process
// exits, so a crashed alien can't permanently wedge the store.
func withLock(name string, fn func() error) error {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, name)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer f.Close()

	// Try non-blocking first; if contended, retry with a short sleep so we
	// don't busy-wait on the kernel. Cap at ~5s — anything longer almost
	// certainly means a crashed peer holding the lock, and we'd rather
	// surface an error than hang forever.
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			return fmt.Errorf("acquire lock: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for alien store lock at %s", lockPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}
