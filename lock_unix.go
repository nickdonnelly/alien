//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// withStoreLock acquires an exclusive advisory lock on a sentinel file in
// $ALIEN_HOME, runs fn, and releases the lock. Used to wrap any
// load-mutate-save sequence so two concurrent alien processes don't lose
// each other's writes (e.g. an interactive `alien add` racing the
// background `alien sync maybe-push` after a previous call).
//
// We lock a separate `.lock` file rather than aliases.json itself so the
// existing atomic write pattern (write `.tmp`, rename) still works without
// fighting the lock semantics.
//
// flock(2) advisory locks are released automatically when the process
// exits, so a crashed alien can't permanently wedge the store.
func withStoreLock(fn func() error) error {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, ".lock")

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
