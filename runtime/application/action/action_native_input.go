package action

import "context"

import "strings"

type projectNativeInputContextKey struct{}

type projectNativeInput struct {
	actionKey string
	value     any
}

// WithProjectNativeInput retains a project-owned Go input only for the current
// synchronous Action call. It is never part of the domain invocation,
// persistence receipt, audit payload or idempotency fingerprint.
func WithProjectNativeInput(ctx context.Context, actionKey string, input any) context.Context {
	if input == nil {
		return ctx
	}
	return context.WithValue(ctx, projectNativeInputContextKey{}, projectNativeInput{actionKey: strings.TrimSpace(actionKey), value: input})
}

func projectNativeInputFromContext(ctx context.Context, actionKey string) any {
	if ctx == nil {
		return nil
	}
	input, ok := ctx.Value(projectNativeInputContextKey{}).(projectNativeInput)
	if !ok || input.actionKey != strings.TrimSpace(actionKey) {
		return nil
	}
	return input.value
}

func withoutProjectNativeInput(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectNativeInputContextKey{}, projectNativeInput{})
}
