// Package filelock is an exclusive advisory lock on a file (flock(2)), held
// for a process's lifetime. The kernel releases it when the process dies, so
// a held lock proves a live owner without trusting a PID file or a probe.
// Linux and macOS only; the file must be on a local filesystem.
package filelock

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"time"
)

// ErrLocked: another open file description holds the lock.
var ErrLocked = errors.New("locked by another process")

// retryWait bounds how long Acquire retries a held lock: Held briefly takes
// a shared lock, so a concurrent probe must not fail a starting owner.
const retryWait = 500 * time.Millisecond

// Lock is a held lock. Release (or process exit) frees it. The file is
// never deleted: removing a lock file lets two owners lock different inodes.
type Lock struct {
	f    *os.File
	once sync.Once
}

// Acquire creates path (mode perm) if missing and takes the exclusive lock,
// retrying for up to 500ms; still held then, it returns ErrLocked.
func Acquire(path string, perm fs.FileMode) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, perm)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(retryWait)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrLocked
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// AcquireContext is Acquire that keeps retrying until ctx ends (then
// ErrLocked): for short-lived critical sections that queue up, such as
// concurrent `stage service` commands.
func AcquireContext(ctx context.Context, path string, perm fs.FileMode) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, perm)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ErrLocked
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// File is the locked file (to record owner metadata).
func (l *Lock) File() *os.File { return l.f }

// Release frees the lock. Safe to call more than once.
func (l *Lock) Release() {
	l.once.Do(func() { _ = l.f.Close() })
}

// Held reports whether a live process holds path's lock, without creating
// the file. A missing file is not held.
func Held(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}
