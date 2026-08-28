package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	notificationservice "github.com/domainry/domainry-runtime/runtime/domain/notification/service"
	notificationvalidation "github.com/domainry/domainry-runtime/runtime/domain/notification/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type notificationHTTPClock struct{}

func (notificationHTTPClock) Now() time.Time { return time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) }

type notificationHTTPRepository struct {
	record          notificationmodel.NotificationTemplateRecord
	version         notificationmodel.NotificationTemplateVersion
	publications    []notificationmodel.NotificationPublicationRequest
	lastCreated     notificationmodel.NotificationPublicationRequest
	policy          notificationmodel.NotificationDeliveryPolicy
	preferences     []notificationmodel.NotificationRecipientPreference
	metrics         notificationmodel.NotificationDeliveryMetrics
	inboxMetrics    notificationmodel.NotificationInboxGovernanceMetrics
	inboxItem       notificationmodel.NotificationInboxItem
	delegations     []notificationmodel.NotificationInboxDelegation
	delegatedOwners []string
	lastScope       principalmodel.SystemScope
	lastWorkspaceID string
	lastSince       string
	err             error
	inboxListErr    error
	inboxFacetsErr  error
	inboxItems      []notificationmodel.NotificationInboxItem
	emptyInbox      bool
	inboxListErrors []error
	inboxListCalls  int
}

func (r *notificationHTTPRepository) GetRecipientPreference(context.Context, string, string) (notificationmodel.NotificationRecipientPreference, bool, error) {
	return r.policyPreference(), r.preferences != nil, r.err
}
func (r *notificationHTTPRepository) policyPreference() notificationmodel.NotificationRecipientPreference {
	if len(r.preferences) == 0 {
		return notificationmodel.NotificationRecipientPreference{}
	}
	return r.preferences[0]
}
func (r *notificationHTTPRepository) ListInboxItems(context.Context, notificationmodel.NotificationInboxQuery) ([]notificationmodel.NotificationInboxItem, bool, error) {
	call := r.inboxListCalls
	r.inboxListCalls++
	if call < len(r.inboxListErrors) && r.inboxListErrors[call] != nil {
		return nil, false, r.inboxListErrors[call]
	}
	if r.inboxListErr != nil {
		return nil, false, r.inboxListErr
	}
	if r.emptyInbox {
		return nil, false, r.err
	}
	if call < len(r.inboxItems) {
		return []notificationmodel.NotificationInboxItem{r.inboxItems[call]}, false, r.err
	}
	return []notificationmodel.NotificationInboxItem{r.inboxItem}, false, r.err
}
func (r *notificationHTTPRepository) GetInboxItem(context.Context, notificationmodel.NotificationInboxQuery, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, r.inboxItem.ID != "", r.err
}
func (r *notificationHTTPRepository) CountInboxFacets(context.Context, notificationmodel.NotificationInboxQuery) (notificationmodel.NotificationInboxFacets, error) {
	if r.inboxFacetsErr != nil {
		return notificationmodel.NotificationInboxFacets{}, r.inboxFacetsErr
	}
	return notificationmodel.NotificationInboxFacets{Unread: 3}, r.err
}
func (r *notificationHTTPRepository) SetInboxItemRead(context.Context, notificationmodel.NotificationInboxQuery, string, string, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, true, r.err
}
func (r *notificationHTTPRepository) SetInboxItemArchived(context.Context, notificationmodel.NotificationInboxQuery, string, string, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, true, r.err
}
func (r *notificationHTTPRepository) AcknowledgeInboxAlert(context.Context, notificationmodel.NotificationInboxQuery, string, string, string) (notificationmodel.NotificationInboxItem, bool, error) {
	return r.inboxItem, true, r.err
}
func (r *notificationHTTPRepository) MarkAllInboxItemsRead(context.Context, notificationmodel.NotificationInboxQuery, string) (int, error) {
	return 2, r.err
}
func (r *notificationHTTPRepository) ListInboxSavedViews(context.Context, string, string, string) ([]notificationmodel.NotificationInboxSavedView, error) {
	return []notificationmodel.NotificationInboxSavedView{{Key: "saved", Name: "Saved"}}, r.err
}
func (r *notificationHTTPRepository) SaveInboxSavedView(_ context.Context, _, _, _ string, value notificationmodel.NotificationInboxSavedView) (notificationmodel.NotificationInboxSavedView, error) {
	return value, r.err
}
func (r *notificationHTTPRepository) DeleteInboxSavedView(context.Context, string, string, string, string) (bool, error) {
	return true, r.err
}
func (r *notificationHTTPRepository) ListInboxDelegations(context.Context, string, string, string) ([]notificationmodel.NotificationInboxDelegation, error) {
	return append([]notificationmodel.NotificationInboxDelegation(nil), r.delegations...), r.err
}
func (r *notificationHTTPRepository) SaveInboxDelegation(_ context.Context, value notificationmodel.NotificationInboxDelegation) (notificationmodel.NotificationInboxDelegation, error) {
	return value, r.err
}
func (r *notificationHTTPRepository) DeleteInboxDelegation(context.Context, string, string, string) (bool, error) {
	return true, r.err
}
func (r *notificationHTTPRepository) ListActiveDelegatedOwnerIDs(context.Context, string, string, string, string) ([]string, error) {
	return append([]string(nil), r.delegatedOwners...), r.err
}

func (r *notificationHTTPRepository) InboxGovernanceMetrics(_ context.Context, workspaceID, since string) (notificationmodel.NotificationInboxGovernanceMetrics, error) {
	r.lastWorkspaceID, r.lastSince = workspaceID, since
	return r.inboxMetrics, r.err
}

func (r *notificationHTTPRepository) List(context.Context, principalmodel.SystemScope) ([]notificationmodel.NotificationTemplateRecord, error) {
	return []notificationmodel.NotificationTemplateRecord{r.record}, r.err
}
func (r *notificationHTTPRepository) Get(_ context.Context, _ principalmodel.SystemScope, key string) (notificationmodel.NotificationTemplateRecord, bool, error) {
	return r.record, r.record.Key == key, r.err
}
func (r *notificationHTTPRepository) ListVersions(context.Context, principalmodel.SystemScope, string) ([]notificationmodel.NotificationTemplateVersion, error) {
	return []notificationmodel.NotificationTemplateVersion{r.version}, r.err
}
func (r *notificationHTTPRepository) GetVersion(_ context.Context, _ principalmodel.SystemScope, key string, version int) (notificationmodel.NotificationTemplateVersion, bool, error) {
	return r.version, r.version.TemplateKey == key && r.version.Version == version, r.err
}
func (r *notificationHTTPRepository) ListPublicationRequests(context.Context, principalmodel.SystemScope, string) ([]notificationmodel.NotificationPublicationRequest, error) {
	return append([]notificationmodel.NotificationPublicationRequest(nil), r.publications...), r.err
}
func (r *notificationHTTPRepository) GetPublicationRequest(_ context.Context, _ principalmodel.SystemScope, id string) (notificationmodel.NotificationPublicationRequest, bool, error) {
	for _, publication := range r.publications {
		if publication.ID == id {
			return publication, true, r.err
		}
	}
	return notificationmodel.NotificationPublicationRequest{}, false, r.err
}
func (r *notificationHTTPRepository) CreatePublicationRequest(_ context.Context, _ principalmodel.SystemScope, value notificationmodel.NotificationPublicationRequest) error {
	r.lastCreated = value
	return r.err
}
func (r *notificationHTTPRepository) TransitionPublicationRequest(_ context.Context, _ principalmodel.SystemScope, id, _ string, transition notificationmodel.NotificationPublicationTransition) (notificationmodel.NotificationPublicationRequest, error) {
	value, _, err := r.GetPublicationRequest(context.Background(), principalmodel.SystemScope{}, id)
	value.Status, value.ReviewedBy, value.Failure = transition.Status, transition.ReviewedBy, transition.Failure
	return value, err
}
func (r *notificationHTTPRepository) HasOpenPublicationRequest(context.Context, principalmodel.SystemScope, string) (bool, error) {
	return false, r.err
}
func (r *notificationHTTPRepository) SaveDraft(_ context.Context, _ principalmodel.SystemScope, template notificationmodel.NotificationTemplate, _, actor string) (notificationmodel.NotificationTemplateRecord, error) {
	value := notificationmodel.NotificationTemplateRecord{Key: template.Key, Draft: &template, Status: "draft", UpdatedBy: actor}
	return value, r.err
}
func (r *notificationHTTPRepository) Disable(_ context.Context, _ principalmodel.SystemScope, key, _, actor string) (notificationmodel.NotificationTemplateRecord, error) {
	return notificationmodel.NotificationTemplateRecord{Key: key, Status: "disabled", UpdatedBy: actor}, r.err
}
func (r *notificationHTTPRepository) DeliveryMetrics(_ context.Context, workspaceID, since string) (notificationmodel.NotificationDeliveryMetrics, error) {
	r.lastWorkspaceID, r.lastSince = workspaceID, since
	return r.metrics, r.err
}
func (r *notificationHTTPRepository) GetDeliveryPolicy(_ context.Context, scope principalmodel.SystemScope) (notificationmodel.NotificationDeliveryPolicy, error) {
	r.lastScope = scope
	return r.policy, r.err
}
func (r *notificationHTTPRepository) SaveDeliveryPolicy(_ context.Context, scope principalmodel.SystemScope, value notificationmodel.NotificationDeliveryPolicy) (notificationmodel.NotificationDeliveryPolicy, error) {
	r.lastScope, r.policy = scope, value
	return value, r.err
}
func (r *notificationHTTPRepository) ListRecipientPreferences(_ context.Context, workspaceID string) ([]notificationmodel.NotificationRecipientPreference, error) {
	r.lastWorkspaceID = workspaceID
	return append([]notificationmodel.NotificationRecipientPreference(nil), r.preferences...), r.err
}
func (r *notificationHTTPRepository) SaveRecipientPreference(_ context.Context, workspaceID string, value notificationmodel.NotificationRecipientPreference) (notificationmodel.NotificationRecipientPreference, error) {
	r.lastWorkspaceID = workspaceID
	r.preferences = append(r.preferences, value)
	return value, r.err
}

// notificationHTTPApplication is a transport-local fake. Handler tests verify
// the Runtime BFF contract and must not reconstruct Notification's source-owned
// managers, stores, or workers.
type notificationHTTPApplication struct {
	NotificationApplication
	repo    *notificationHTTPRepository
	catalog *notificationservice.NotificationEventCatalog
}

func notificationHTTPAuthorize(principal principalmodel.Principal) error {
	if !principal.Known || strings.TrimSpace(principal.WorkspaceID) == "" {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required"}
	}
	return nil
}

func (a *notificationHTTPApplication) GovernanceCatalog(_ context.Context, p principalmodel.Principal) (notificationmodel.NotificationGovernanceCatalog, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationGovernanceCatalog{}, err
	}
	return a.catalog.GovernanceCatalog(), nil
}
func (a *notificationHTTPApplication) InboxGovernanceMetrics(ctx context.Context, since string, p principalmodel.Principal) (notificationmodel.NotificationInboxGovernanceMetrics, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxGovernanceMetrics{}, err
	}
	return a.repo.InboxGovernanceMetrics(ctx, p.WorkspaceID, since)
}
func (a *notificationHTTPApplication) List(ctx context.Context, p principalmodel.Principal) ([]notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return a.repo.List(ctx, principalmodel.SystemScope{})
}
func (a *notificationHTTPApplication) Get(ctx context.Context, key string, p principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, bool, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, false, err
	}
	return a.repo.Get(ctx, principalmodel.SystemScope{}, key)
}
func (a *notificationHTTPApplication) ListVersions(ctx context.Context, key string, p principalmodel.Principal) ([]notificationmodel.NotificationTemplateVersion, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return a.repo.ListVersions(ctx, principalmodel.SystemScope{}, key)
}
func (a *notificationHTTPApplication) ListPublicationRequests(ctx context.Context, key string, p principalmodel.Principal) ([]notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return a.repo.ListPublicationRequests(ctx, principalmodel.SystemScope{}, key)
}
func (a *notificationHTTPApplication) Capabilities(_ context.Context, p principalmodel.Principal) ([]notificationcontract.NotificationProviderCapability, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return notificationcontract.NotificationProviderCapabilities(), nil
}
func (a *notificationHTTPApplication) RestoreVersionDraft(ctx context.Context, key string, version int, _ string, p principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	v, found, err := a.repo.GetVersion(ctx, principalmodel.SystemScope{}, key, version)
	if err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	if !found {
		return notificationmodel.NotificationTemplateRecord{}, &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.template_version_not_found"}
	}
	return a.repo.SaveDraft(ctx, principalmodel.SystemScope{}, v.Template, "", p.UserID)
}
func (a *notificationHTTPApplication) SaveDraft(ctx context.Context, key string, value notificationmodel.NotificationTemplate, expected string, p principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	value.Key = key
	if strings.TrimSpace(value.Status) == "" {
		value.Status = "draft"
	}
	if err := notificationvalidation.NotificationValidateEditableTemplate(value); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	return a.repo.SaveDraft(ctx, principalmodel.SystemScope{}, value, expected, p.UserID)
}
func (a *notificationHTTPApplication) Disable(ctx context.Context, key, expected string, p principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	return a.repo.Disable(ctx, principalmodel.SystemScope{}, key, expected, p.UserID)
}
func (a *notificationHTTPApplication) Preview(_ context.Context, key, locale string, recipients []string, _ map[string]any, p principalmodel.Principal) (notificationmodel.RenderedNotification, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.RenderedNotification{}, err
	}
	if len(recipients) == 0 {
		recipients = []string{p.UserID}
	}
	return notificationmodel.RenderedNotification{TemplateKey: key, TemplateLocale: locale, Recipients: recipients}, a.repo.err
}
func (a *notificationHTTPApplication) PreviewTemplate(_ context.Context, value notificationmodel.NotificationTemplate, locale string, recipients []string, _ map[string]any, p principalmodel.Principal) (notificationmodel.RenderedNotification, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.RenderedNotification{}, err
	}
	if len(recipients) == 0 {
		recipients = []string{p.UserID}
	}
	return notificationmodel.RenderedNotification{TemplateKey: value.Key, TemplateLocale: locale, Recipients: recipients}, a.repo.err
}
func (a *notificationHTTPApplication) RequestPublication(ctx context.Context, key, scheduled, _ string, p principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationPublicationRequest{}, err
	}
	v := notificationmodel.NotificationPublicationRequest{ID: "requested-1", TemplateKey: key, Status: "pending", ScheduledFor: scheduled, RequestedBy: p.UserID}
	if err := a.repo.CreatePublicationRequest(ctx, principalmodel.SystemScope{}, v); err != nil {
		return v, err
	}
	return v, nil
}
func (a *notificationHTTPApplication) transitionPublication(ctx context.Context, id, status, reason string, p principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationPublicationRequest{}, err
	}
	return a.repo.TransitionPublicationRequest(ctx, principalmodel.SystemScope{}, id, "", notificationmodel.NotificationPublicationTransition{Status: status, ReviewedBy: p.UserID, Failure: reason})
}
func (a *notificationHTTPApplication) ApprovePublication(ctx context.Context, id string, p principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	return a.transitionPublication(ctx, id, "published", "", p)
}
func (a *notificationHTTPApplication) RejectPublication(ctx context.Context, id, reason string, p principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	return a.transitionPublication(ctx, id, "rejected", reason, p)
}
func (a *notificationHTTPApplication) CancelPublication(ctx context.Context, id string, p principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	return a.transitionPublication(ctx, id, "cancelled", "", p)
}
func (a *notificationHTTPApplication) DeliveryMetrics(ctx context.Context, since string, p principalmodel.Principal) (notificationmodel.NotificationDeliveryMetrics, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationDeliveryMetrics{}, err
	}
	return a.repo.DeliveryMetrics(ctx, p.WorkspaceID, since)
}
func (a *notificationHTTPApplication) GetDeliveryPolicy(ctx context.Context, p principalmodel.Principal) (notificationmodel.NotificationDeliveryPolicy, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationDeliveryPolicy{}, err
	}
	return a.repo.GetDeliveryPolicy(ctx, principalmodel.SystemScope{})
}
func (a *notificationHTTPApplication) SaveDeliveryPolicy(ctx context.Context, value notificationmodel.NotificationDeliveryPolicy, p principalmodel.Principal) (notificationmodel.NotificationDeliveryPolicy, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return value, err
	}
	if err := validateNotificationHTTPDeliveryPolicy(value); err != nil {
		return value, err
	}
	value.UpdatedBy, value.UpdatedAt = p.UserID, notificationHTTPClock{}.Now().Format(time.RFC3339)
	return a.repo.SaveDeliveryPolicy(ctx, principalmodel.SystemScope{}, value)
}

func validateNotificationHTTPDeliveryPolicy(value notificationmodel.NotificationDeliveryPolicy) error {
	if value.MaxPerRecipientPerHour < 1 || value.MaxPerRecipientPerHour > 10000 {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.policy_frequency_invalid"}
	}
	if value.DedupeWindowSeconds < 0 || value.DedupeWindowSeconds > 86400 {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.policy_dedupe_invalid"}
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.policy_timezone_invalid"}
	}
	for _, clock := range []string{value.QuietStart, value.QuietEnd} {
		if _, err := time.Parse("15:04", clock); err != nil {
			return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.policy_quiet_hours_invalid"}
		}
	}
	return nil
}
func (a *notificationHTTPApplication) ListRecipientPreferences(ctx context.Context, p principalmodel.Principal) ([]notificationmodel.NotificationRecipientPreference, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return a.repo.ListRecipientPreferences(ctx, p.WorkspaceID)
}
func (a *notificationHTTPApplication) SaveRecipientPreference(ctx context.Context, value notificationmodel.NotificationRecipientPreference, p principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return value, err
	}
	if strings.TrimSpace(value.RecipientKey) == "" {
		return value, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.notification.preference_identity_required"}
	}
	if value.EnabledChannels == nil {
		value.EnabledChannels = map[string]bool{}
	}
	value.UpdatedBy, value.UpdatedAt = p.UserID, notificationHTTPClock{}.Now().Format(time.RFC3339)
	return a.repo.SaveRecipientPreference(ctx, p.WorkspaceID, value)
}

func (a *notificationHTTPApplication) GetMyNotificationPreference(ctx context.Context, _ surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationRecipientPreference{}, err
	}
	v, found, err := a.repo.GetRecipientPreference(ctx, p.WorkspaceID, p.UserID)
	if !found {
		v = notificationmodel.NotificationRecipientPreference{RecipientKey: p.UserID, EnabledChannels: map[string]bool{}}
	}
	return v, err
}
func (a *notificationHTTPApplication) SaveMyNotificationPreference(ctx context.Context, value notificationmodel.NotificationRecipientPreference, _ surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error) {
	value.RecipientKey = p.UserID
	return a.SaveRecipientPreference(ctx, value, p)
}
func (a *notificationHTTPApplication) scopedQuery(query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, p principalmodel.Principal) notificationmodel.NotificationInboxQuery {
	query.WorkspaceID, query.ViewerUserID, query.Surface = p.WorkspaceID, p.UserID, string(surface)
	query.ReportingUserIDs = append([]string(nil), p.ReportingUserIDs...)
	if query.Scope == notificationmodel.NotificationInboxScopeDelegated {
		query.DelegatedUserIDs = append([]string(nil), a.repo.delegatedOwners...)
	}
	return query
}
func (a *notificationHTTPApplication) ListInbox(ctx context.Context, query notificationmodel.NotificationInboxQuery, _ string, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxPage, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxPage{}, err
	}
	query = a.scopedQuery(query, surface, p)
	values, more, err := a.repo.ListInboxItems(ctx, query)
	return notificationmodel.NotificationInboxPage{Items: values, HasMore: more}, err
}
func (a *notificationHTTPApplication) GetInboxItem(ctx context.Context, id string, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	v, found, err := a.repo.GetInboxItem(ctx, a.scopedQuery(query, surface, p), id)
	if err == nil && !found {
		err = &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_item_not_found"}
	}
	return v, err
}
func (a *notificationHTTPApplication) InboxFacets(ctx context.Context, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxFacets, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxFacets{}, err
	}
	return a.repo.CountInboxFacets(ctx, a.scopedQuery(query, surface, p))
}
func (a *notificationHTTPApplication) SetInboxRead(ctx context.Context, id string, read bool, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	v, _, err := a.repo.SetInboxItemRead(ctx, a.scopedQuery(notificationmodel.NotificationInboxQuery{Scope: notificationmodel.NotificationInboxScopeMine}, surface, p), id, "", "")
	if !read {
		v.ReadAt = ""
	}
	return v, err
}
func (a *notificationHTTPApplication) SetInboxArchived(ctx context.Context, id string, archived bool, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	v, _, err := a.repo.SetInboxItemArchived(ctx, a.scopedQuery(notificationmodel.NotificationInboxQuery{Scope: notificationmodel.NotificationInboxScopeMine}, surface, p), id, "", "")
	if !archived {
		v.ArchivedAt = ""
	}
	return v, err
}
func (a *notificationHTTPApplication) AcknowledgeInboxAlert(ctx context.Context, id string, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxItem, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxItem{}, err
	}
	v, _, err := a.repo.AcknowledgeInboxAlert(ctx, a.scopedQuery(notificationmodel.NotificationInboxQuery{Scope: notificationmodel.NotificationInboxScopeMine}, surface, p), id, p.UserID, "")
	return v, err
}
func (a *notificationHTTPApplication) MarkAllInboxRead(ctx context.Context, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, p principalmodel.Principal) (int, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return 0, err
	}
	if query.Scope == notificationmodel.NotificationInboxScopeTeam || query.Scope == notificationmodel.NotificationInboxScopeDelegated {
		return 0, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_team_mutation_forbidden"}
	}
	return a.repo.MarkAllInboxItemsRead(ctx, a.scopedQuery(query, surface, p), "")
}
func (a *notificationHTTPApplication) ResolveInboxAction(_ context.Context, id, key string, query notificationmodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxResolvedAction, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return notificationmodel.NotificationInboxResolvedAction{}, err
	}
	if a.repo.err != nil {
		return notificationmodel.NotificationInboxResolvedAction{}, a.repo.err
	}
	if query.Scope == notificationmodel.NotificationInboxScopeTeam || query.Scope == notificationmodel.NotificationInboxScopeDelegated {
		return notificationmodel.NotificationInboxResolvedAction{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_team_action_forbidden"}
	}
	for _, action := range a.repo.inboxItem.Actions {
		if action.Key == key {
			return notificationmodel.NotificationInboxResolvedAction{Key: key, Label: action.Label, Style: action.Style, NavigationKind: "route", RouteKey: "business.test", RouteParams: map[string]string{"resource_id": action.ResourceID}, Status: "available"}, nil
		}
	}
	_ = id
	_ = surface
	return notificationmodel.NotificationInboxResolvedAction{}, &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_not_found"}
}
func (a *notificationHTTPApplication) ListMyInboxDelegations(ctx context.Context, surface surfacemodel.ProductSurface, p principalmodel.Principal) ([]notificationmodel.NotificationInboxDelegation, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return a.repo.ListInboxDelegations(ctx, p.WorkspaceID, p.UserID, string(surface))
}
func (a *notificationHTTPApplication) SaveMyInboxDelegation(ctx context.Context, value notificationmodel.NotificationInboxDelegation, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxDelegation, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return value, err
	}
	value.WorkspaceID, value.OwnerUserID, value.Surface = p.WorkspaceID, p.UserID, string(surface)
	return a.repo.SaveInboxDelegation(ctx, value)
}
func (a *notificationHTTPApplication) DeleteMyInboxDelegation(ctx context.Context, id string, _ surfacemodel.ProductSurface, p principalmodel.Principal) error {
	if err := notificationHTTPAuthorize(p); err != nil {
		return err
	}
	_, err := a.repo.DeleteInboxDelegation(ctx, p.WorkspaceID, p.UserID, id)
	return err
}
func (a *notificationHTTPApplication) ListMyDelegatedInboxOwners(ctx context.Context, surface surfacemodel.ProductSurface, p principalmodel.Principal) ([]string, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return a.repo.ListActiveDelegatedOwnerIDs(ctx, p.WorkspaceID, p.UserID, string(surface), "")
}
func (a *notificationHTTPApplication) ListInboxSavedViews(ctx context.Context, surface surfacemodel.ProductSurface, p principalmodel.Principal) ([]notificationmodel.NotificationInboxSavedView, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return nil, err
	}
	return a.repo.ListInboxSavedViews(ctx, p.WorkspaceID, p.UserID, string(surface))
}
func (a *notificationHTTPApplication) SaveInboxSavedView(ctx context.Context, value notificationmodel.NotificationInboxSavedView, surface surfacemodel.ProductSurface, p principalmodel.Principal) (notificationmodel.NotificationInboxSavedView, error) {
	if err := notificationHTTPAuthorize(p); err != nil {
		return value, err
	}
	return a.repo.SaveInboxSavedView(ctx, p.WorkspaceID, p.UserID, string(surface), value)
}
func (a *notificationHTTPApplication) DeleteInboxSavedView(ctx context.Context, key string, surface surfacemodel.ProductSurface, p principalmodel.Principal) error {
	if err := notificationHTTPAuthorize(p); err != nil {
		return err
	}
	_, err := a.repo.DeleteInboxSavedView(ctx, p.WorkspaceID, p.UserID, string(surface), key)
	return err
}

type notificationHTTPResponse struct {
	status int
	code   string
	value  any
	err    error
}

func notificationHTTPTemplate() notificationmodel.NotificationTemplate {
	return notificationmodel.NotificationTemplate{
		Key: "order-ready", Name: "Order ready", Channel: "email", Status: "draft", Version: 1, DefaultLocale: "en-US",
		Locales: map[string]notificationmodel.NotificationTemplateContent{"en-US": {Subject: "Order ready", Text: "Your order is ready"}},
	}
}

func newNotificationHTTPHandler(repo *notificationHTTPRepository) (*NotificationsHandler, *notificationHTTPResponse, *principalmodel.Principal) {
	template := notificationHTTPTemplate()
	catalog, err := notificationservice.NewNotificationEventCatalog([]notificationmodel.NotificationEventType{{
		Key: "test.event", Source: "test", Category: "system", DefaultSeverity: "info", Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: "builtin.test.event", DefaultLocale: "en-US", Locales: map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": {Title: "Test", Body: "Test body", ActionLabels: map[string]string{"test.open": "Open"}}}, Version: 1, Status: "published",
		Actions: []notificationmodel.NotificationInboxActionDescriptor{{Key: "test.open", Kind: "route", ResourceType: "test", SurfaceRoutes: map[string]string{"business_workspace": "business.test"}}},
	}})
	if err != nil {
		panic(err)
	}
	application := &notificationHTTPApplication{repo: repo, catalog: catalog}
	response := &notificationHTTPResponse{}
	principal := accessfixture.AttachPointer(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reviewer", WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{
		notificationcontract.PermissionTemplateRead, notificationcontract.PermissionTemplateManage,
		notificationcontract.PermissionTemplatePublish, notificationcontract.PermissionTemplateApprove, notificationcontract.PermissionTemplateTest,
		notificationcontract.PermissionPolicyRead, notificationcontract.PermissionPolicyManage,
	}},
	)
	if repo.record.Key == "" {
		repo.record = notificationmodel.NotificationTemplateRecord{Key: template.Key, Draft: &template, Status: "draft", UpdatedAt: "revision-1"}
	}
	if repo.version.TemplateKey == "" {
		repo.version = notificationmodel.NotificationTemplateVersion{TemplateKey: template.Key, Version: 1, Template: template}
	}
	return NewNotificationsHandler(NotificationsDependencies{
		Management: application, Delivery: application, Inbox: application,
		Principal: func(*http.Request) principalmodel.Principal { return *principal },
		WriteJSON: func(_ http.ResponseWriter, status int, value any) { response.status, response.value = status, value },
		WriteError: func(_ http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			response.status, response.code = status, code
		},
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) {
			response.status, response.err = http.StatusInternalServerError, err
		},
		DecodeJSON: func(_ http.ResponseWriter, request *http.Request, value any) bool {
			if err := json.NewDecoder(request.Body).Decode(value); err != nil {
				response.status, response.err = http.StatusBadRequest, err
				return false
			}
			return true
		},
		Authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
	}), response, principal
}

func notificationHTTPPublications() []notificationmodel.NotificationPublicationRequest {
	return []notificationmodel.NotificationPublicationRequest{
		{ID: "approve-1", TemplateKey: "order-ready", Status: "pending", RequestedBy: "author", ScheduledFor: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)},
		{ID: "reject-1", TemplateKey: "order-ready", Status: "pending", RequestedBy: "author"},
		{ID: "cancel-1", TemplateKey: "order-ready", Status: "pending", RequestedBy: "author"},
	}
}
