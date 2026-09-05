package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testRunner struct {
	mu       sync.Mutex
	runs     map[string]int
	started  map[string]chan struct{}
	unblock  map[string]chan struct{}
	canceled map[string]chan struct{}
}

func newTestRunner() *testRunner {
	return &testRunner{
		runs:     make(map[string]int),
		started:  make(map[string]chan struct{}),
		unblock:  make(map[string]chan struct{}),
		canceled: make(map[string]chan struct{}),
	}
}

func (r *testRunner) Run(ctx context.Context, workload string) error {
	r.mu.Lock()
	r.runs[workload]++
	startedCh, ok := r.started[workload]
	if !ok {
		startedCh = make(chan struct{}, 1)
		r.started[workload] = startedCh
	}
	unblockCh, ok := r.unblock[workload]
	if !ok {
		unblockCh = make(chan struct{})
		r.unblock[workload] = unblockCh
	}
	canceledCh, ok := r.canceled[workload]
	if !ok {
		canceledCh = make(chan struct{}, 1)
		r.canceled[workload] = canceledCh
	}
	r.mu.Unlock()

	select {
	case startedCh <- struct{}{}:
	default:
	}

	select {
	case <-unblockCh:
		return nil
	case <-ctx.Done():
		select {
		case canceledCh <- struct{}{}:
		default:
		}
		return ctx.Err()
	}
}

func (r *testRunner) waitForStart(t *testing.T, workload string) {
	t.Helper()
	var ch chan struct{}
	for i := 0; i < 50; i++ {
		r.mu.Lock()
		ch = r.started[workload]
		r.mu.Unlock()
		if ch != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, ch, "channel should exist for %s", workload)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for workload %s to start", workload)
	}
}

func (r *testRunner) unblockWorkload(workload string) {
	r.mu.Lock()
	ch, ok := r.unblock[workload]
	if !ok {
		ch = make(chan struct{})
		r.unblock[workload] = ch
	}
	r.mu.Unlock()
	close(ch)
}

func TestSchedulerUpdateSpecsDoesNotCancelActiveWorkload(t *testing.T) {
	runner := newTestRunner()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := New(runner, []WorkloadSpec{
		{Name: "workload-a", Interval: 10 * time.Minute, Enabled: true},
	}, Options{})
	sched.Start(ctx)
	defer sched.Stop()

	// Wait for workload-a to start its initial run
	runner.waitForStart(t, "workload-a")

	// Now simulate a rescan where workload-a is still present and workload-b is newly added
	sched.UpdateSpecs([]WorkloadSpec{
		{Name: "workload-a", Interval: 10 * time.Minute, Enabled: true},
		{Name: "workload-b", Interval: 10 * time.Minute, Enabled: true},
	})

	// workload-b should start immediately
	runner.waitForStart(t, "workload-b")

	// Ensure workload-a's active run was NOT canceled
	runner.mu.Lock()
	canceledCh := runner.canceled["workload-a"]
	runner.mu.Unlock()
	if canceledCh != nil {
		select {
		case <-canceledCh:
			t.Fatalf("workload-a was canceled during UpdateSpecs!")
		default:
		}
	}

	// Unblock both and ensure both complete
	runner.unblockWorkload("workload-a")
	runner.unblockWorkload("workload-b")

	runner.mu.Lock()
	assert.Equal(t, 1, runner.runs["workload-a"])
	assert.Equal(t, 1, runner.runs["workload-b"])
	runner.mu.Unlock()
}

func TestSchedulerRemovesDeletedWorkload(t *testing.T) {
	runner := newTestRunner()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := New(runner, []WorkloadSpec{
		{Name: "workload-to-remove", Interval: 10 * time.Minute, Enabled: true},
	}, Options{})
	sched.Start(ctx)
	defer sched.Stop()

	runner.waitForStart(t, "workload-to-remove")

	// Rescan with empty specs -> workload-to-remove should be canceled
	sched.UpdateSpecs([]WorkloadSpec{})

	runner.mu.Lock()
	canceledCh := runner.canceled["workload-to-remove"]
	runner.mu.Unlock()
	require.NotNil(t, canceledCh)

	select {
	case <-canceledCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for removed workload to be canceled")
	}
}
