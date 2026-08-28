package notification

import (
	"context"
	"strings"
	"sync"

	sourcenotification "github.com/domainry/domainry-notification"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// NotificationAudienceResolver is implemented by the source owner that can
// authoritatively map one semantic resolver key to current Workspace users.
// Notification owns orchestration, retry and evidence; the source owner keeps
// authorization/business ownership of the relationship being resolved.
type NotificationAudienceResolver func(context.Context, sourceinbox.Event) ([]sourcenotification.UserID, error)

// NotificationAudienceResolverRegistry is the integration seam for Workflow,
// Scheduler, Integration and generated source modules. Registration happens
// during composition and is frozen before workers start, avoiding a central
// event-type switch and mutable runtime resolver behavior.
type NotificationAudienceResolverRegistry struct {
	mu        sync.RWMutex
	resolvers map[string]NotificationAudienceResolver
	frozen    bool
}

func NewNotificationAudienceResolverRegistry() *NotificationAudienceResolverRegistry {
	return &NotificationAudienceResolverRegistry{resolvers: map[string]NotificationAudienceResolver{}}
}

func (r *NotificationAudienceResolverRegistry) Register(key string, resolver NotificationAudienceResolver) bool {
	if r == nil || resolver == nil {
		return false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen || r.resolvers[key] != nil {
		return false
	}
	r.resolvers[key] = resolver
	return true
}

func (r *NotificationAudienceResolverRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *NotificationAudienceResolverRegistry) ResolveAudience(ctx context.Context, key string, event sourceinbox.Event) ([]sourcenotification.UserID, error) {
	if r == nil {
		return nil, &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.notification.inbox_audience_resolver_unavailable"}
	}
	r.mu.RLock()
	resolver := r.resolvers[strings.TrimSpace(key)]
	r.mu.RUnlock()
	if resolver == nil {
		return nil, &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.notification.inbox_audience_resolver_unavailable"}
	}
	return resolver(ctx, event)
}
