package contract

import (
	"context"
	"strings"
)

type builderTaskIDKey struct{}

// WithBuilderTaskID records a Builder task identity only after the Runtime
// lifecycle gate has verified it. Raw HTTP headers must never call this port.
func WithBuilderTaskID(ctx context.Context, taskID string) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, builderTaskIDKey{}, strings.TrimSpace(taskID))
}

func BuilderTaskID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(builderTaskIDKey{}).(string)
	return strings.TrimSpace(value)
}
