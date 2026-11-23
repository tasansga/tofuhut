package reconciler

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
)

type groupLock struct {
	held    bool
	waiters []chan struct{}
}

// LockManager serializes workload runs that share a configured lock group in FIFO order.
type LockManager struct {
	mu     sync.Mutex
	groups map[string]*groupLock
}

// NewLockManager creates a LockManager for coordinating per-group mutual exclusion.
func NewLockManager() *LockManager {
	return &LockManager{
		groups: make(map[string]*groupLock),
	}
}

// Acquire blocks until the lock for group is held or ctx is cancelled.
// An empty group or nil LockManager is a no-op.
func (lm *LockManager) Acquire(ctx context.Context, group, workload string) (func(), error) {
	if lm == nil || group == "" {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	lm.mu.Lock()
	g, ok := lm.groups[group]
	if !ok {
		g = &groupLock{}
		lm.groups[group] = g
	}

	if !g.held && len(g.waiters) == 0 {
		g.held = true
		lm.mu.Unlock()
		recordLockAcquiredImmediate(ctx, group, workload)
		stopHeld := startLockHeldMetric(ctx, group, workload)
		return lm.newUnlockFunc(group, stopHeld), nil
	}

	waiter := make(chan struct{})
	g.waiters = append(g.waiters, waiter)
	lm.mu.Unlock()

	logFields := logrus.Fields{
		"component":  "lock_manager",
		"workload":   workload,
		"lock_group": group,
	}
	if requestID, ok := RequestIDFromContext(ctx); ok {
		logFields["request_id"] = requestID
	}
	logrus.WithFields(logFields).Info("waiting for lock group")

	finishWait := startLockWaitMetric(ctx, group, workload)

	select {
	case <-waiter:
		finishWait("acquired")
		logrus.WithFields(logFields).Info("acquired lock group")
		stopHeld := startLockHeldMetric(ctx, group, workload)
		return lm.newUnlockFunc(group, stopHeld), nil
	case <-ctx.Done():
		lm.cancelWaiter(group, waiter)
		finishWait("cancelled")
		return nil, ctx.Err()
	}
}

func (lm *LockManager) newUnlockFunc(group string, stopHeld func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			stopHeld()
			lm.release(group)
		})
	}
}

func (lm *LockManager) release(group string) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	g, ok := lm.groups[group]
	if !ok {
		return
	}
	if len(g.waiters) > 0 {
		next := g.waiters[0]
		g.waiters[0] = nil
		g.waiters = g.waiters[1:]
		close(next)
		return
	}
	g.held = false
	delete(lm.groups, group)
}

func (lm *LockManager) cancelWaiter(group string, waiter chan struct{}) {
	lm.mu.Lock()
	g, ok := lm.groups[group]
	if !ok {
		lm.mu.Unlock()
		return
	}
	for i, w := range g.waiters {
		if w == waiter {
			copy(g.waiters[i:], g.waiters[i+1:])
			g.waiters[len(g.waiters)-1] = nil
			g.waiters = g.waiters[:len(g.waiters)-1]
			lm.mu.Unlock()
			return
		}
	}
	lm.mu.Unlock()

	// Lock was handed to this waiter concurrently with ctx cancellation; pass it on.
	select {
	case <-waiter:
		lm.release(group)
	default:
	}
}
