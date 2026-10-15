package tasks

import (
	"context"
	"sync"
)

// KeyLock serializes work on a shared resource identified by a string key.
// It exists to prevent concurrent git operations (pull/clone/checkout) on the
// same repository directory when a template allows parallel tasks: all such
// tasks share one working copy on disk, and concurrent `git pull` corrupts it.
//
// The zero value is ready to use.
type KeyLock struct {
	mu    sync.Mutex
	locks map[string]chan struct{}
}

// Lock blocks until the mutex for the given key is acquired and returns the
// unlock function.
//
// ponytail: entries are never removed — the map is bounded by the number of
// repository directories (repos × templates), which is negligible.
func (l *KeyLock) Lock(key string) func() {
	lock := l.lock(key)
	<-lock
	return func() { lock <- struct{}{} }
}

// LockWithStop waits for a keyed lock unless a task stop request arrives first.
func (l *KeyLock) LockWithStop(key string, stop <-chan struct{}) (func(), error) {
	lock := l.lock(key)
	select {
	case <-stop:
		return nil, context.Canceled
	default:
	}
	select {
	case <-lock:
		select {
		case <-stop:
			lock <- struct{}{}
			return nil, context.Canceled
		default:
		}
		return func() { lock <- struct{}{} }, nil
	case <-stop:
		return nil, context.Canceled
	}
}

func (l *KeyLock) lock(key string) chan struct{} {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]chan struct{})
	}
	lock, ok := l.locks[key]
	if !ok {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		l.locks[key] = lock
	}
	l.mu.Unlock()
	return lock
}
