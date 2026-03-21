package reconciler

import "context"

type contextKey string

const requestIDContextKey contextKey = "tofuhut_request_id"

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
