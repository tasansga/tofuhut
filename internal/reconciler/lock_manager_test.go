package reconciler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func waitForWaiters(t *testing.T, lm *LockManager, group string, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		lm.mu.Lock()
		defer lm.mu.Unlock()
		g, ok := lm.groups[group]
		if !ok {
			return count == 0
		}
		return len(g.waiters) == count
	}, time.Second, time.Millisecond)
}

func TestLockManagerNilAndEmptyGroup(t *testing.T) {
	ctx := context.Background()

	var nilLM *LockManager
	unlockNil, err := nilLM.Acquire(ctx, "apt", "w1")
	require.NoError(t, err)
	unlockNil()

	lm := NewLockManager()
	unlock1, err := lm.Acquire(ctx, "", "w1")
	require.NoError(t, err)
	unlock2, err := lm.Acquire(ctx, "", "w2")
	require.NoError(t, err)

	unlock2()
	unlock1()
}

func TestLockManagerPreCancelledContext(t *testing.T) {
	lm := NewLockManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	unlock, err := lm.Acquire(ctx, "apt", "w1")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, unlock)
}

func TestLockManagerSerializesSameGroupAndAllowsDifferentGroups(t *testing.T) {
	lm := NewLockManager()
	ctx := context.Background()

	unlockA, err := lm.Acquire(ctx, "apt", "workload-a")
	require.NoError(t, err)

	// Different group acquires immediately without blocking.
	unlockOther, err := lm.Acquire(ctx, "dns", "workload-dns")
	require.NoError(t, err)
	unlockOther()

	var bAcquired atomic.Bool
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		unlockB, err := lm.Acquire(ctx, "apt", "workload-b")
		require.NoError(t, err)
		bAcquired.Store(true)
		unlockB()
	}()

	waitForWaiters(t, lm, "apt", 1)
	assert.False(t, bAcquired.Load(), "workload-b must wait while workload-a holds lock")

	unlockA()
	// Calling unlock twice must be safe.
	unlockA()

	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("workload-b did not acquire lock after workload-a released it")
	}
	assert.True(t, bAcquired.Load())
}

func TestLockManagerFIFOOrder(t *testing.T) {
	lm := NewLockManager()
	ctx := context.Background()

	unlockA, err := lm.Acquire(ctx, "apt", "a")
	require.NoError(t, err)

	var mu sync.Mutex
	var order []string

	bDone := make(chan struct{})
	cDone := make(chan struct{})

	go func() {
		defer close(bDone)
		unlockB, err := lm.Acquire(ctx, "apt", "b")
		require.NoError(t, err)
		mu.Lock()
		order = append(order, "b")
		mu.Unlock()
		unlockB()
	}()

	waitForWaiters(t, lm, "apt", 1)

	go func() {
		defer close(cDone)
		unlockC, err := lm.Acquire(ctx, "apt", "c")
		require.NoError(t, err)
		mu.Lock()
		order = append(order, "c")
		mu.Unlock()
		unlockC()
	}()

	waitForWaiters(t, lm, "apt", 2)

	unlockA()

	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("b timed out")
	}
	select {
	case <-cDone:
	case <-time.After(time.Second):
		t.Fatal("c timed out")
	}

	assert.Equal(t, []string{"b", "c"}, order)
}

func TestLockManagerContextCancelWhileWaiting(t *testing.T) {
	lm := NewLockManager()

	unlockA, err := lm.Acquire(context.Background(), "apt", "a")
	require.NoError(t, err)

	cancelCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	unlockB, err := lm.Acquire(cancelCtx, "apt", "b")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, unlockB)

	// Next waiter should still acquire cleanly once A releases.
	cDone := make(chan struct{})
	go func() {
		defer close(cDone)
		unlockC, err := lm.Acquire(context.Background(), "apt", "c")
		require.NoError(t, err)
		unlockC()
	}()

	waitForWaiters(t, lm, "apt", 1)
	unlockA()

	select {
	case <-cDone:
	case <-time.After(time.Second):
		t.Fatal("c did not acquire lock after cancelled b and released a")
	}
}

func TestLockManagerMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		defaultRunMetrics.mu.Lock()
		defaultRunMetrics.inited = false
		defaultRunMetrics.mu.Unlock()
	})

	require.NoError(t, InitMetrics(provider.Meter("tofuhut-test")))

	lm := NewLockManager()
	ctx := context.Background()

	// Uncontended acquire
	unlockA, err := lm.Acquire(ctx, "apt", "workload-a")
	require.NoError(t, err)

	// Contended waiter that gets cancelled
	cancelCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	_, err = lm.Acquire(cancelCtx, "apt", "workload-b")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Contended waiter that succeeds after A releases
	cDone := make(chan struct{})
	go func() {
		defer close(cDone)
		unlockC, err := lm.Acquire(ctx, "apt", "workload-c")
		require.NoError(t, err)
		unlockC()
	}()

	waitForWaiters(t, lm, "apt", 1)

	// While C is waiting and A is holding, verify gauges.
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(ctx, &rm))
	assert.Equal(t, int64(1), sumInt64Metric(t, rm, "tofuhut_reconcile_lock_held", attribute.String("lock_group", "apt")))
	assert.Equal(t, int64(1), sumInt64Metric(t, rm, "tofuhut_reconcile_lock_waiting", attribute.String("lock_group", "apt")))

	unlockA()
	select {
	case <-cDone:
	case <-time.After(time.Second):
		t.Fatal("workload-c did not complete")
	}

	require.NoError(t, reader.Collect(ctx, &rm))
	assert.Equal(t, int64(0), sumInt64Metric(t, rm, "tofuhut_reconcile_lock_held", attribute.String("lock_group", "apt")))
	assert.Equal(t, int64(0), sumInt64Metric(t, rm, "tofuhut_reconcile_lock_waiting", attribute.String("lock_group", "apt")))

	assert.Equal(t, int64(1), sumInt64Metric(t, rm, "tofuhut_reconcile_lock_acquisitions_total",
		attribute.String("lock_group", "apt"),
		attribute.String("workload", "workload-a"),
		attribute.String("result", "acquired"),
		attribute.String("contended", "false"),
	))
	assert.Equal(t, int64(1), sumInt64Metric(t, rm, "tofuhut_reconcile_lock_acquisitions_total",
		attribute.String("lock_group", "apt"),
		attribute.String("workload", "workload-b"),
		attribute.String("result", "cancelled"),
		attribute.String("contended", "true"),
	))
	assert.Equal(t, int64(1), sumInt64Metric(t, rm, "tofuhut_reconcile_lock_acquisitions_total",
		attribute.String("lock_group", "apt"),
		attribute.String("workload", "workload-c"),
		attribute.String("result", "acquired"),
		attribute.String("contended", "true"),
	))

	// Wait duration histogram should only record contended waits (workload-b and workload-c), not uncontended workload-a.
	assert.Equal(t, uint64(0), histogramCount(t, rm, "tofuhut_reconcile_lock_wait_duration_seconds", attribute.String("workload", "workload-a")))
	assert.Equal(t, uint64(1), histogramCount(t, rm, "tofuhut_reconcile_lock_wait_duration_seconds", attribute.String("workload", "workload-b")))
	assert.Equal(t, uint64(1), histogramCount(t, rm, "tofuhut_reconcile_lock_wait_duration_seconds", attribute.String("workload", "workload-c")))
}

func histogramCount(t *testing.T, rm metricdata.ResourceMetrics, name string, match ...attribute.KeyValue) uint64 {
	t.Helper()
	var count uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok, "metric %s is not Histogram[float64]", name)
			for _, dp := range hist.DataPoints {
				if attrsMatch(dp.Attributes, match) {
					count += dp.Count
				}
			}
		}
	}
	return count
}

func sumInt64Metric(t *testing.T, rm metricdata.ResourceMetrics, name string, match ...attribute.KeyValue) int64 {
	t.Helper()
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "metric %s is not Sum[int64]", name)
			for _, dp := range sum.DataPoints {
				if attrsMatch(dp.Attributes, match) {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func attrsMatch(set attribute.Set, match []attribute.KeyValue) bool {
	for _, kv := range match {
		v, ok := set.Value(kv.Key)
		if !ok || v != kv.Value {
			return false
		}
	}
	return true
}
