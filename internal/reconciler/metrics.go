package reconciler

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type runMetrics struct {
	mu sync.RWMutex

	inited bool

	runTotal         metric.Int64Counter
	runDuration      metric.Float64Histogram
	runInFlight      metric.Int64UpDownCounter
	lockAcquisitions metric.Int64Counter
	lockWaitDuration metric.Float64Histogram
	lockWaiting      metric.Int64UpDownCounter
	lockHeld         metric.Int64UpDownCounter
}

var defaultRunMetrics runMetrics

// InitMetrics initializes reconcile metrics instruments for this process.
func InitMetrics(meter metric.Meter) error {
	runTotal, err := meter.Int64Counter(
		"tofuhut_reconcile_runs_total",
		metric.WithDescription("Total reconciliation runs"),
	)
	if err != nil {
		return err
	}
	runDuration, err := meter.Float64Histogram(
		"tofuhut_reconcile_duration_seconds",
		metric.WithDescription("Reconciliation run duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}
	runInFlight, err := meter.Int64UpDownCounter(
		"tofuhut_reconcile_inflight",
		metric.WithDescription("Current in-flight reconciliation runs"),
	)
	if err != nil {
		return err
	}
	lockAcquisitions, err := meter.Int64Counter(
		"tofuhut_reconcile_lock_acquisitions_total",
		metric.WithDescription("Total lock group acquisition attempts"),
	)
	if err != nil {
		return err
	}
	lockWaitDuration, err := meter.Float64Histogram(
		"tofuhut_reconcile_lock_wait_duration_seconds",
		metric.WithDescription("Time spent waiting to acquire a lock group in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return err
	}
	lockWaiting, err := meter.Int64UpDownCounter(
		"tofuhut_reconcile_lock_waiting",
		metric.WithDescription("Current workloads waiting for a lock group"),
	)
	if err != nil {
		return err
	}
	lockHeld, err := meter.Int64UpDownCounter(
		"tofuhut_reconcile_lock_held",
		metric.WithDescription("Current lock groups held by running workloads"),
	)
	if err != nil {
		return err
	}

	defaultRunMetrics.mu.Lock()
	defer defaultRunMetrics.mu.Unlock()
	defaultRunMetrics.runTotal = runTotal
	defaultRunMetrics.runDuration = runDuration
	defaultRunMetrics.runInFlight = runInFlight
	defaultRunMetrics.lockAcquisitions = lockAcquisitions
	defaultRunMetrics.lockWaitDuration = lockWaitDuration
	defaultRunMetrics.lockWaiting = lockWaiting
	defaultRunMetrics.lockHeld = lockHeld
	defaultRunMetrics.inited = true
	return nil
}

func getRunMetricsSnapshot() (runMetrics, bool) {
	defaultRunMetrics.mu.RLock()
	defer defaultRunMetrics.mu.RUnlock()
	if !defaultRunMetrics.inited {
		return runMetrics{}, false
	}
	return runMetrics{
		runTotal:         defaultRunMetrics.runTotal,
		runDuration:      defaultRunMetrics.runDuration,
		runInFlight:      defaultRunMetrics.runInFlight,
		lockAcquisitions: defaultRunMetrics.lockAcquisitions,
		lockWaitDuration: defaultRunMetrics.lockWaitDuration,
		lockWaiting:      defaultRunMetrics.lockWaiting,
		lockHeld:         defaultRunMetrics.lockHeld,
	}, true
}

func startRunMetric(ctx context.Context, workload, workloadType string) func(result string) {
	m, ok := getRunMetricsSnapshot()
	if !ok {
		return func(string) {}
	}

	trigger := TriggerSourceFromContext(ctx)
	if trigger == "" {
		trigger = "unknown"
	}
	attrs := metric.WithAttributes(
		attribute.String("workload", workload),
		attribute.String("workload_type", workloadType),
		attribute.String("trigger", trigger),
	)
	m.runInFlight.Add(ctx, 1, attrs)
	start := time.Now()
	return func(result string) {
		resultAttrs := metric.WithAttributes(
			attribute.String("workload", workload),
			attribute.String("workload_type", workloadType),
			attribute.String("trigger", trigger),
			attribute.String("result", result),
		)
		m.runTotal.Add(ctx, 1, resultAttrs)
		m.runDuration.Record(ctx, time.Since(start).Seconds(), resultAttrs)
		m.runInFlight.Add(ctx, -1, attrs)
	}
}

func startLockWaitMetric(ctx context.Context, group, workload string) func(result string) {
	m, ok := getRunMetricsSnapshot()
	if !ok {
		return func(string) {}
	}

	metricCtx := context.WithoutCancel(ctx)
	attrs := metric.WithAttributes(
		attribute.String("lock_group", group),
		attribute.String("workload", workload),
	)
	m.lockWaiting.Add(metricCtx, 1, attrs)
	start := time.Now()
	return func(result string) {
		m.lockWaiting.Add(metricCtx, -1, attrs)
		resultAttrs := metric.WithAttributes(
			attribute.String("lock_group", group),
			attribute.String("workload", workload),
			attribute.String("result", result),
			attribute.String("contended", "true"),
		)
		m.lockAcquisitions.Add(metricCtx, 1, resultAttrs)
		m.lockWaitDuration.Record(metricCtx, time.Since(start).Seconds(), metric.WithAttributes(
			attribute.String("lock_group", group),
			attribute.String("workload", workload),
			attribute.String("result", result),
		))
	}
}

func recordLockAcquiredImmediate(ctx context.Context, group, workload string) {
	m, ok := getRunMetricsSnapshot()
	if !ok {
		return
	}

	metricCtx := context.WithoutCancel(ctx)
	m.lockAcquisitions.Add(metricCtx, 1, metric.WithAttributes(
		attribute.String("lock_group", group),
		attribute.String("workload", workload),
		attribute.String("result", "acquired"),
		attribute.String("contended", "false"),
	))
}

func startLockHeldMetric(ctx context.Context, group, workload string) func() {
	m, ok := getRunMetricsSnapshot()
	if !ok {
		return func() {}
	}

	metricCtx := context.WithoutCancel(ctx)
	attrs := metric.WithAttributes(
		attribute.String("lock_group", group),
		attribute.String("workload", workload),
	)
	m.lockHeld.Add(metricCtx, 1, attrs)
	return func() {
		m.lockHeld.Add(metricCtx, -1, attrs)
	}
}
