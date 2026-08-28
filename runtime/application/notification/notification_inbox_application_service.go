package notification

import (
	"context"
	"strings"

	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func (s *NotificationApplicationService) PublishInboxEvent(ctx context.Context, value notificationmodel.NotificationEvent, scope principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
	if s.eventPublisher == nil {
		return notificationmodel.NotificationEvent{}, false, notificationInboxUnavailable()
	}
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return notificationmodel.NotificationEvent{}, false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	stored, created, err := s.eventPublisher.PublishEvent(ctx, moduleInboxEvent(value))
	return planeInboxEvent(stored), created, mapNotificationModuleError(err)
}

// PublishInboxIntent is the producer-facing use case used by Workflow,
// Scheduler, Integration, and other Runtime owners. Copy and actions are
// compiled from the published Event Type catalog inside Notification.
func (s *NotificationApplicationService) PublishInboxIntent(ctx context.Context, value notificationmodel.NotificationIntent, scope principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
	if s.eventPublisher == nil {
		return notificationmodel.NotificationEvent{}, false, notificationInboxUnavailable()
	}
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return notificationmodel.NotificationEvent{}, false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	stored, created, err := s.eventPublisher.PublishIntent(ctx, moduleInboxIntent(value))
	return planeInboxEvent(stored), created, mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) CompileInboxIntent(value notificationmodel.NotificationIntent, scope principalmodel.SystemScope) (notificationmodel.NotificationEvent, error) {
	if s.inboxCompiler == nil {
		return notificationmodel.NotificationEvent{}, notificationInboxUnavailable()
	}
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return notificationmodel.NotificationEvent{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	compiled, err := s.inboxCompiler.Compile(moduleInboxIntent(value))
	event := planeInboxEvent(compiled)
	if err == nil {
		intent := value
		event.PublicationIntent = &intent
	}
	return event, mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ProcessDueInboxEvents(ctx context.Context, limit int, scope principalmodel.SystemScope) (int, error) {
	if s.inboxProcessor == nil {
		return 0, notificationInboxUnavailable()
	}
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	processed, err := s.inboxProcessor.ProcessDue(ctx, limit)
	return processed, mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) GetMyNotificationPreference(ctx context.Context, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationRecipientPreference{}, err
	}
	var sourceValue sourcedelivery.RecipientPreference
	sourceValue, found, err := s.policyManager.GetRecipientPreference(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID))
	value := planeRecipientPreference(sourceValue)
	err = mapNotificationModuleError(err)
	if err != nil {
		return notificationmodel.NotificationRecipientPreference{}, err
	}
	if !found {
		return notificationmodel.NotificationRecipientPreference{RecipientKey: principal.UserID, EnabledChannels: map[string]bool{}}, nil
	}
	return value, nil
}

func (s *NotificationApplicationService) SaveMyNotificationPreference(ctx context.Context, value notificationmodel.NotificationRecipientPreference, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationRecipientPreference{}, err
	}
	value.RecipientKey = principal.UserID
	stored, err := s.policyManager.SaveRecipientPreference(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleRecipientPreference(value), principal.UserID)
	return planeRecipientPreference(stored), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ListInbox(ctx context.Context, query notificationmodel.NotificationInboxQuery, cursor string, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxPage, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxPage{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxPage{}, notificationInboxUnavailable()
	}
	notificationScopeInboxQuery(&query, principal, surface)
	if err := s.scopeDelegatedInboxQuery(ctx, &query, principal, surface); err != nil {
		return notificationmodel.NotificationInboxPage{}, err
	}
	page, err := s.mailboxManager.List(ctx, moduleInboxQuery(query), cursor)
	return planeInboxPage(page), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) GetInboxItem(ctx context.Context, id string, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxItem{}, notificationInboxUnavailable()
	}
	notificationScopeInboxQuery(&query, principal, surface)
	if err := s.scopeDelegatedInboxQuery(ctx, &query, principal, surface); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	item, err := s.mailboxManager.Get(ctx, moduleInboxQuery(query), id)
	return planeInboxItem(item), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) InboxFacets(ctx context.Context, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxFacets, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxFacets{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxFacets{}, notificationInboxUnavailable()
	}
	notificationScopeInboxQuery(&query, principal, surface)
	if err := s.scopeDelegatedInboxQuery(ctx, &query, principal, surface); err != nil {
		return notificationmodel.NotificationInboxFacets{}, err
	}
	facets, err := s.mailboxManager.Facets(ctx, moduleInboxQuery(query))
	return planeInboxFacets(facets), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) SetInboxRead(ctx context.Context, id string, read bool, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxItem{}, notificationInboxUnavailable()
	}
	query := notificationmodel.NotificationInboxQuery{Scope: notificationmodel.NotificationInboxScopeMine}
	notificationScopeInboxQuery(&query, principal, surface)
	item, err := s.mailboxManager.SetRead(ctx, moduleInboxQuery(query), id, read)
	return planeInboxItem(item), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ResolveInboxAction(ctx context.Context, id, actionKey string, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxResolvedAction, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxResolvedAction{}, err
	}
	if s.actionResolver == nil {
		return notificationmodel.NotificationInboxResolvedAction{}, notificationInboxUnavailable()
	}
	if scope := strings.TrimSpace(query.Scope); scope == notificationmodel.NotificationInboxScopeTeam || scope == notificationmodel.NotificationInboxScopeDelegated {
		return notificationmodel.NotificationInboxResolvedAction{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_team_action_forbidden"}
	}
	query.Scope = notificationmodel.NotificationInboxScopeMine
	notificationScopeInboxQuery(&query, principal, surface)
	sourceResolved, err := s.actionResolver.Resolve(ctx, moduleInboxQuery(query), id, actionKey)
	resolved := planeResolvedAction(sourceResolved)
	err = mapNotificationModuleError(err)
	if err != nil {
		return notificationmodel.NotificationInboxResolvedAction{}, err
	}
	if s.authorizeAction != nil {
		if err := s.authorizeAction(ctx, resolved, principal); err != nil {
			return notificationmodel.NotificationInboxResolvedAction{}, err
		}
	}
	return resolved, nil
}

func (s *NotificationApplicationService) SetInboxArchived(ctx context.Context, id string, archived bool, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxItem{}, notificationInboxUnavailable()
	}
	query := notificationmodel.NotificationInboxQuery{Scope: notificationmodel.NotificationInboxScopeMine}
	notificationScopeInboxQuery(&query, principal, surface)
	item, err := s.mailboxManager.SetArchived(ctx, moduleInboxQuery(query), id, archived)
	return planeInboxItem(item), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) AcknowledgeInboxAlert(ctx context.Context, id string, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxItem{}, notificationInboxUnavailable()
	}
	query := notificationmodel.NotificationInboxQuery{Scope: notificationmodel.NotificationInboxScopeMine}
	notificationScopeInboxQuery(&query, principal, surface)
	item, err := s.mailboxManager.AcknowledgeAlert(ctx, moduleInboxQuery(query), id, moduleUserID(principal.UserID))
	return planeInboxItem(item), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) MarkAllInboxRead(ctx context.Context, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (int, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return 0, err
	}
	if s.mailboxManager == nil {
		return 0, notificationInboxUnavailable()
	}
	if scope := strings.TrimSpace(query.Scope); scope == notificationmodel.NotificationInboxScopeTeam || scope == notificationmodel.NotificationInboxScopeDelegated {
		return 0, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_team_mutation_forbidden"}
	}
	notificationScopeInboxQuery(&query, principal, surface)
	count, err := s.mailboxManager.MarkAllRead(ctx, moduleInboxQuery(query))
	return count, mapNotificationModuleError(err)
}

func notificationScopeInboxQuery(query *notificationmodel.NotificationInboxQuery, principal principalmodel.Principal, surface surfacemodel.ProductSurface) {
	query.WorkspaceID = principal.WorkspaceID
	query.ViewerUserID = principal.UserID
	query.Surface = string(surface)
	query.ReportingUserIDs = append([]string(nil), principal.ReportingUserIDs...)
}

func (s *NotificationApplicationService) scopeDelegatedInboxQuery(ctx context.Context, query *notificationmodel.NotificationInboxQuery, principal principalmodel.Principal, surface surfacemodel.ProductSurface) error {
	if strings.TrimSpace(query.Scope) != notificationmodel.NotificationInboxScopeDelegated {
		return nil
	}
	sourceOwners, err := s.mailboxManager.ActiveDelegatedOwnerIDs(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID), moduleSurface(surface))
	owners := planeUserIDs(sourceOwners)
	err = mapNotificationModuleError(err)
	if err != nil {
		return err
	}
	query.DelegatedUserIDs = owners
	return nil
}

func (s *NotificationApplicationService) ListMyInboxDelegations(ctx context.Context, surface surfacemodel.ProductSurface, principal principalmodel.Principal) ([]notificationmodel.NotificationInboxDelegation, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return nil, err
	}
	if s.mailboxManager == nil {
		return nil, notificationInboxUnavailable()
	}
	values, err := s.mailboxManager.ListDelegations(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID), moduleSurface(surface))
	return planeDelegations(values), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) SaveMyInboxDelegation(ctx context.Context, value notificationmodel.NotificationInboxDelegation, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxDelegation, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return value, err
	}
	if s.mailboxManager == nil {
		return value, notificationInboxUnavailable()
	}
	value.WorkspaceID, value.OwnerUserID, value.Surface = principal.WorkspaceID, principal.UserID, string(surface)
	stored, err := s.mailboxManager.SaveDelegation(ctx, moduleDelegation(value))
	return planeDelegation(stored), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) DeleteMyInboxDelegation(ctx context.Context, id string, surface surfacemodel.ProductSurface, principal principalmodel.Principal) error {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return err
	}
	if s.mailboxManager == nil {
		return notificationInboxUnavailable()
	}
	return mapNotificationModuleError(s.mailboxManager.DeleteDelegation(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID), id))
}

func (s *NotificationApplicationService) ListMyDelegatedInboxOwners(ctx context.Context, surface surfacemodel.ProductSurface, principal principalmodel.Principal) ([]string, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return nil, err
	}
	if s.mailboxManager == nil {
		return nil, notificationInboxUnavailable()
	}
	values, err := s.mailboxManager.ActiveDelegatedOwnerIDs(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID), moduleSurface(surface))
	return planeUserIDs(values), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ListInboxSavedViews(ctx context.Context, surface surfacemodel.ProductSurface, principal principalmodel.Principal) ([]notificationmodel.NotificationInboxSavedView, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return nil, err
	}
	if s.mailboxManager == nil {
		return nil, notificationInboxUnavailable()
	}
	values, err := s.mailboxManager.ListSavedViews(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID), moduleSurface(surface))
	return planeSavedViews(values), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) SaveInboxSavedView(ctx context.Context, value notificationmodel.NotificationInboxSavedView, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationmodel.NotificationInboxSavedView, error) {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return notificationmodel.NotificationInboxSavedView{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxSavedView{}, notificationInboxUnavailable()
	}
	stored, err := s.mailboxManager.SaveSavedView(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID), moduleSurface(surface), moduleSavedView(value))
	return planeSavedView(stored), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) DeleteInboxSavedView(ctx context.Context, key string, surface surfacemodel.ProductSurface, principal principalmodel.Principal) error {
	if err := notificationAuthorizeInbox(principal, surface); err != nil {
		return err
	}
	if s.mailboxManager == nil {
		return notificationInboxUnavailable()
	}
	return mapNotificationModuleError(s.mailboxManager.DeleteSavedView(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleUserID(principal.UserID), moduleSurface(surface), key))
}

func notificationAuthorizeInbox(principal principalmodel.Principal, surface surfacemodel.ProductSurface) error {
	if !principal.Known || len(strings.TrimSpace(principal.WorkspaceID)) == 0 || len(strings.TrimSpace(principal.UserID)) == 0 {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required"}
	}
	return nil
}

func notificationInboxUnavailable() error {
	return &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.notification.inbox_unavailable"}
}
