package notification

import (
	"context"
	"strings"
	"sync"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type NotificationInboxResourceAuthorizer func(context.Context, string, principalmodel.Principal) error
type NotificationInboxResolvedResourceAuthorizer func(context.Context, notificationmodel.NotificationInboxResolvedAction, principalmodel.Principal) error

// NotificationInboxActionAuthorizerRegistry is the host composition seam for
// host-owned resource checks. Notification resolves the semantic action;
// resource owners register current authorization without a central switch.
type NotificationInboxActionAuthorizerRegistry struct {
	mu         sync.RWMutex
	authorizer map[string]NotificationInboxResolvedResourceAuthorizer
	frozen     bool
}

func NewNotificationInboxActionAuthorizerRegistry() *NotificationInboxActionAuthorizerRegistry {
	return &NotificationInboxActionAuthorizerRegistry{authorizer: map[string]NotificationInboxResolvedResourceAuthorizer{}}
}

func (r *NotificationInboxActionAuthorizerRegistry) Register(resourceType string, authorizer NotificationInboxResourceAuthorizer) bool {
	if authorizer == nil {
		return false
	}
	return r.RegisterResolved(resourceType, func(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
		return authorizer(ctx, strings.TrimSpace(action.RouteParams["resource_id"]), principal)
	})
}

func (r *NotificationInboxActionAuthorizerRegistry) RegisterResolved(resourceType string, authorizer NotificationInboxResolvedResourceAuthorizer) bool {
	if r == nil || authorizer == nil {
		return false
	}
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return false
	}
	if _, exists := r.authorizer[resourceType]; exists {
		return false
	}
	r.authorizer[resourceType] = authorizer
	return true
}

func (r *NotificationInboxActionAuthorizerRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *NotificationInboxActionAuthorizerRegistry) Authorize(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
	resourceType := strings.TrimSpace(action.RouteParams["resource_type"])
	r.mu.RLock()
	authorizer := r.authorizer[resourceType]
	r.mu.RUnlock()
	if authorizer == nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_authorizer_unavailable"}
	}
	return authorizer(ctx, action, principal)
}
