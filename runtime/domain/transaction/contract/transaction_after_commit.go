package contract

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type AfterCommitPurpose string

const (
	AfterCommitDurableWorkWakeup      AfterCommitPurpose = "durable_work_wakeup"
	AfterCommitCacheInvalidation      AfterCommitPurpose = "cache_invalidation"
	AfterCommitLocalProjectionRefresh AfterCommitPurpose = "local_projection_refresh"
)

var (
	ErrAfterCommitRegistryMissing  = errors.New("after-commit registry is missing from transaction context")
	ErrAfterCommitHookInvalid      = errors.New("after-commit hook is invalid")
	ErrAfterCommitRecoveryRequired = errors.New("after-commit hook requires durable recovery evidence")
)

type AfterCommitHook struct {
	Name            string
	Purpose         AfterCommitPurpose
	DurableRecovery bool
	Run             func(context.Context) error
}

type AfterCommitFailure struct {
	Name            string
	Purpose         AfterCommitPurpose
	DurableRecovery bool
	CorrelationID   string
	Cause           error
}

type afterCommitRegistryContextKey struct{}

type AfterCommitRegistry struct {
	mu    sync.Mutex
	hooks []AfterCommitHook
}

func WithAfterCommitRegistry(ctx context.Context) (context.Context, *AfterCommitRegistry) {
	registry := &AfterCommitRegistry{}
	return context.WithValue(ctx, afterCommitRegistryContextKey{}, registry), registry
}

func RegisterAfterCommit(ctx context.Context, hook AfterCommitHook) error {
	registry, _ := ctx.Value(afterCommitRegistryContextKey{}).(*AfterCommitRegistry)
	if registry == nil {
		return ErrAfterCommitRegistryMissing
	}
	hook.Name = strings.TrimSpace(hook.Name)
	if hook.Name == "" || hook.Run == nil || !allowedAfterCommitPurpose(hook.Purpose) {
		return ErrAfterCommitHookInvalid
	}
	if !hook.DurableRecovery {
		return ErrAfterCommitRecoveryRequired
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.hooks = append(registry.hooks, hook)
	return nil
}

func (registry *AfterCommitRegistry) Hooks() []AfterCommitHook {
	if registry == nil {
		return nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return append([]AfterCommitHook(nil), registry.hooks...)
}

func allowedAfterCommitPurpose(purpose AfterCommitPurpose) bool {
	switch purpose {
	case AfterCommitDurableWorkWakeup, AfterCommitCacheInvalidation, AfterCommitLocalProjectionRefresh:
		return true
	default:
		return false
	}
}
