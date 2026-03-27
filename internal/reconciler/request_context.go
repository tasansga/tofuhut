package reconciler

import "context"

type contextKey string

const requestIDContextKey contextKey = "tofuhut_request_id"
const forceReconcileContextKey contextKey = "tofuhut_force_reconcile"
const triggerSourceContextKey contextKey = "tofuhut_trigger_source"

// WithRequestID stores a request id in context for log correlation.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey, requestID)
}

// RequestIDFromContext returns a request id previously stored in context.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(requestIDContextKey).(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// WithForceReconcile marks a run context as force-triggered.
func WithForceReconcile(ctx context.Context, force bool) context.Context {
	if !force {
		return ctx
	}
	return context.WithValue(ctx, forceReconcileContextKey, true)
}

// ForceReconcileFromContext returns whether the run was explicitly force-triggered.
func ForceReconcileFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, ok := ctx.Value(forceReconcileContextKey).(bool)
	return ok && v
}

// WithTriggerSource stores the source of a reconcile trigger.
func WithTriggerSource(ctx context.Context, source string) context.Context {
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, triggerSourceContextKey, source)
}

// TriggerSourceFromContext returns the source of a reconcile trigger.
func TriggerSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, ok := ctx.Value(triggerSourceContextKey).(string)
	if !ok {
		return ""
	}
	return v
}
