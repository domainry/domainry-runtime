package notificationfacade

import (
	"context"
	"strings"
	"sync"

	"github.com/domainry/domainry-foundation/apperror"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type InboxResourceAuthorizer func(context.Context, string, principalmodel.Principal) error
type InboxResolvedResourceAuthorizer func(context.Context, notificationmodel.NotificationInboxResolvedAction, principalmodel.Principal) error

// ActionAuthorizerRegistry is the Runtime-owned resource authorization seam.
// Notification resolves semantics; Runtime resource owners reauthorize the
// referenced business resource at request time.
type ActionAuthorizerRegistry struct {
	mu         sync.RWMutex
	authorizer map[string]InboxResolvedResourceAuthorizer
	frozen     bool
}

func NewActionAuthorizerRegistry() *ActionAuthorizerRegistry {
	return &ActionAuthorizerRegistry{authorizer: map[string]InboxResolvedResourceAuthorizer{}}
}

func (r *ActionAuthorizerRegistry) Register(resourceType string, authorizer InboxResourceAuthorizer) bool {
	if authorizer == nil {
		return false
	}
	return r.RegisterResolved(resourceType, func(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
		return authorizer(ctx, strings.TrimSpace(action.RouteParams["resource_id"]), principal)
	})
}

func (r *ActionAuthorizerRegistry) RegisterResolved(resourceType string, authorizer InboxResolvedResourceAuthorizer) bool {
	if r == nil || authorizer == nil {
		return false
	}
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen || r.authorizer[resourceType] != nil {
		return false
	}
	r.authorizer[resourceType] = authorizer
	return true
}

func (r *ActionAuthorizerRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *ActionAuthorizerRegistry) Authorize(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
	if r == nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_authorizer_unavailable"}
	}
	resourceType := strings.TrimSpace(action.RouteParams["resource_type"])
	r.mu.RLock()
	authorizer := r.authorizer[resourceType]
	r.mu.RUnlock()
	if authorizer == nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_authorizer_unavailable"}
	}
	return authorizer(ctx, action, principal)
}
