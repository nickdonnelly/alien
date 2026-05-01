//go:build windows

package main

// withStoreLock is a no-op on Windows. Multi-process locking on Windows
// requires LockFileEx via x/sys/windows; until we have a real Windows
// build target it's not worth the complexity, and a single-user CLI on
// Windows rarely races in practice.
func withStoreLock(fn func() error) error { return fn() }
