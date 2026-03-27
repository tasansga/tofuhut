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

	runTotal    metric.Int64Counter
	runDuration metric.Float64Histogram
	runInFlight metric.Int64UpDownCounter
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

	defaultRunMetrics.mu.Lock()
	defer defaultRunMetrics.mu.Unlock()
	defaultRunMetrics.runTotal = runTotal
	defaultRunMetrics.runDuration = runDuration
	defaultRunMetrics.runInFlight = runInFlight
	defaultRunMetrics.inited = true
	return nil
}

func startRunMetric(ctx context.Context, workload, workloadType string) func(result string) {
	defaultRunMetrics.mu.RLock()
	if !defaultRunMetrics.inited {
		defaultRunMetrics.mu.RUnlock()
		return func(string) {}
	}
	runTotal := defaultRunMetrics.runTotal
	runDuration := defaultRunMetrics.runDuration
	runInFlight := defaultRunMetrics.runInFlight
	defaultRunMetrics.mu.RUnlock()

	trigger := TriggerSourceFromContext(ctx)
	if trigger == "" {
		trigger = "unknown"
	}
	attrs := metric.WithAttributes(
		attribute.String("workload", workload),
		attribute.String("workload_type", workloadType),
		attribute.String("trigger", trigger),
	)
	runInFlight.Add(ctx, 1, attrs)
	start := time.Now()
	return func(result string) {
		resultAttrs := metric.WithAttributes(
			attribute.String("workload", workload),
			attribute.String("workload_type", workloadType),
			attribute.String("trigger", trigger),
			attribute.String("result", result),
		)
		runTotal.Add(ctx, 1, resultAttrs)
		runDuration.Record(ctx, time.Since(start).Seconds(), resultAttrs)
		runInFlight.Add(ctx, -1, attrs)
	}
}
