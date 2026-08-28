// Package notificationfacade adapts the deployment-neutral Notification SDK
// Binding to Runtime-owned HTTP and business boundaries. It contains no
// Notification implementation, SQL, processor, or worker dependency.
package notificationfacade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	runtimecontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	runtimemodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type ActionAuthorizer func(context.Context, runtimemodel.NotificationInboxResolvedAction, principalmodel.Principal) error

type Service struct {
	binding         notificationsdk.Binding
	authorizeAction ActionAuthorizer
}

func New(binding notificationsdk.Binding, authorizeAction ActionAuthorizer) (*Service, error) {
	if binding == nil || binding.Templates() == nil || binding.Delivery() == nil || binding.Inbox() == nil || binding.Administration() == nil {
		return nil, errors.New("Notification SDK Binding returned incomplete Runtime ports")
	}
	return &Service{binding: binding, authorizeAction: authorizeAction}, nil
}

func authority(ctx context.Context, surface string) (notificationsdk.UserAuthority, error) {
	identity, ok := identitysdk.RequestIdentityFromContext(ctx)
	if !ok || strings.TrimSpace(identity.AccessToken) == "" {
		return notificationsdk.UserAuthority{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.user_authority_required"}
	}
	return notificationsdk.UserAuthority{AccessToken: identity.AccessToken, Surface: strings.TrimSpace(surface)}, nil
}

func convert[To any, From any](value From) (To, error) {
	var result To
	encoded, err := json.Marshal(value)
	if err != nil {
		return result, fmt.Errorf("encode Notification SDK boundary: %w", err)
	}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return result, fmt.Errorf("decode Notification SDK boundary: %w", err)
	}
	return result, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var sdkError *notificationsdk.Error
	if !errors.As(err, &sdkError) {
		return err
	}
	kind := apperror.KindInternal
	switch sdkError.StatusCode {
	case 400, 422:
		kind = apperror.KindBadRequest
	case 401, 403:
		kind = apperror.KindForbidden
	case 404:
		kind = apperror.KindNotFound
	case 409:
		kind = apperror.KindConflict
	case 429:
		kind = apperror.KindRateLimited
	case 502, 503, 504:
		kind = apperror.KindUnavailable
	}
	return &apperror.AppError{Kind: kind, Code: "backend." + strings.TrimPrefix(sdkError.Code, "backend."), Err: err}
}

func (s *Service) user(ctx context.Context, principal principalmodel.Principal) (notificationsdk.UserAuthority, error) {
	return authority(ctx, principal.SurfaceKey)
}

func (s *Service) GovernanceCatalog(ctx context.Context, principal principalmodel.Principal) (runtimemodel.NotificationGovernanceCatalog, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationGovernanceCatalog{}, err
	}
	v, err := s.binding.Administration().GovernanceCatalog(ctx, a)
	if err != nil {
		return runtimemodel.NotificationGovernanceCatalog{}, mapError(err)
	}
	return convert[runtimemodel.NotificationGovernanceCatalog](v)
}
func (s *Service) InboxGovernanceMetrics(ctx context.Context, since string, principal principalmodel.Principal) (runtimemodel.NotificationInboxGovernanceMetrics, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationInboxGovernanceMetrics{}, err
	}
	v, err := s.binding.Administration().InboxGovernanceMetrics(ctx, a, since)
	if err != nil {
		return runtimemodel.NotificationInboxGovernanceMetrics{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxGovernanceMetrics](v)
}
func (s *Service) Capabilities(ctx context.Context, principal principalmodel.Principal) ([]runtimecontract.NotificationProviderCapability, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Templates().Capabilities(ctx, a)
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimecontract.NotificationProviderCapability](v)
}
func (s *Service) List(ctx context.Context, principal principalmodel.Principal) ([]runtimemodel.NotificationTemplateRecord, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Templates().List(ctx, a)
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationTemplateRecord](v)
}
func (s *Service) Get(ctx context.Context, key string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, bool, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, false, err
	}
	v, found, err := s.binding.Templates().Get(ctx, a, key)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, false, mapError(err)
	}
	out, err := convert[runtimemodel.NotificationTemplateRecord](v)
	return out, found, err
}
func (s *Service) ListVersions(ctx context.Context, key string, principal principalmodel.Principal) ([]runtimemodel.NotificationTemplateVersion, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Templates().ListVersions(ctx, a, key)
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationTemplateVersion](v)
}
func (s *Service) ListPublicationRequests(ctx context.Context, key string, principal principalmodel.Principal) ([]runtimemodel.NotificationPublicationRequest, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Templates().ListPublicationRequests(ctx, a, key)
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationPublicationRequest](v)
}
func (s *Service) RestoreVersionDraft(ctx context.Context, key string, version int, expected string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, err
	}
	v, err := s.binding.Templates().RestoreVersionDraft(ctx, a, key, version, expected)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, mapError(err)
	}
	return convert[runtimemodel.NotificationTemplateRecord](v)
}
func (s *Service) SaveDraft(ctx context.Context, key string, value runtimemodel.NotificationTemplate, expected string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, err
	}
	in, err := convert[sdkcontract.NotificationTemplate](value)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, err
	}
	v, err := s.binding.Templates().SaveDraft(ctx, a, key, in, expected)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, mapError(err)
	}
	return convert[runtimemodel.NotificationTemplateRecord](v)
}
func (s *Service) Disable(ctx context.Context, key, expected string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, err
	}
	v, err := s.binding.Templates().Disable(ctx, a, key, expected)
	if err != nil {
		return runtimemodel.NotificationTemplateRecord{}, mapError(err)
	}
	return convert[runtimemodel.NotificationTemplateRecord](v)
}
func (s *Service) Preview(ctx context.Context, key, locale string, recipients []string, variables map[string]any, principal principalmodel.Principal) (runtimemodel.RenderedNotification, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.RenderedNotification{}, err
	}
	v, err := s.binding.Templates().Preview(ctx, a, key, locale, recipients, variables)
	if err != nil {
		return runtimemodel.RenderedNotification{}, mapError(err)
	}
	return convert[runtimemodel.RenderedNotification](v)
}
func (s *Service) PreviewTemplate(ctx context.Context, value runtimemodel.NotificationTemplate, locale string, recipients []string, variables map[string]any, principal principalmodel.Principal) (runtimemodel.RenderedNotification, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.RenderedNotification{}, err
	}
	in, err := convert[sdkcontract.NotificationTemplate](value)
	if err != nil {
		return runtimemodel.RenderedNotification{}, err
	}
	v, err := s.binding.Templates().PreviewTemplate(ctx, a, in, locale, recipients, variables)
	if err != nil {
		return runtimemodel.RenderedNotification{}, mapError(err)
	}
	return convert[runtimemodel.RenderedNotification](v)
}
func (s *Service) RequestPublication(ctx context.Context, key, scheduled, expected string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, err
	}
	v, err := s.binding.Templates().RequestPublication(ctx, a, key, scheduled, expected)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, mapError(err)
	}
	return convert[runtimemodel.NotificationPublicationRequest](v)
}
func (s *Service) ApprovePublication(ctx context.Context, id string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, err
	}
	v, err := s.binding.Templates().ApprovePublication(ctx, a, id)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, mapError(err)
	}
	return convert[runtimemodel.NotificationPublicationRequest](v)
}
func (s *Service) RejectPublication(ctx context.Context, id, reason string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, err
	}
	v, err := s.binding.Templates().RejectPublication(ctx, a, id, reason)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, mapError(err)
	}
	return convert[runtimemodel.NotificationPublicationRequest](v)
}
func (s *Service) CancelPublication(ctx context.Context, id string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, err
	}
	v, err := s.binding.Templates().CancelPublication(ctx, a, id)
	if err != nil {
		return runtimemodel.NotificationPublicationRequest{}, mapError(err)
	}
	return convert[runtimemodel.NotificationPublicationRequest](v)
}

func (s *Service) DeliveryMetrics(ctx context.Context, since string, principal principalmodel.Principal) (runtimemodel.NotificationDeliveryMetrics, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationDeliveryMetrics{}, err
	}
	v, err := s.binding.Delivery().Metrics(ctx, a, since)
	if err != nil {
		return runtimemodel.NotificationDeliveryMetrics{}, mapError(err)
	}
	return convert[runtimemodel.NotificationDeliveryMetrics](v)
}
func (s *Service) GetDeliveryPolicy(ctx context.Context, principal principalmodel.Principal) (runtimemodel.NotificationDeliveryPolicy, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationDeliveryPolicy{}, err
	}
	v, err := s.binding.Delivery().GetPolicy(ctx, a)
	if err != nil {
		return runtimemodel.NotificationDeliveryPolicy{}, mapError(err)
	}
	return convert[runtimemodel.NotificationDeliveryPolicy](v)
}
func (s *Service) SaveDeliveryPolicy(ctx context.Context, value runtimemodel.NotificationDeliveryPolicy, principal principalmodel.Principal) (runtimemodel.NotificationDeliveryPolicy, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationDeliveryPolicy{}, err
	}
	in, err := convert[sdkcontract.NotificationDeliveryPolicy](value)
	if err != nil {
		return runtimemodel.NotificationDeliveryPolicy{}, err
	}
	v, err := s.binding.Delivery().SavePolicy(ctx, a, in)
	if err != nil {
		return runtimemodel.NotificationDeliveryPolicy{}, mapError(err)
	}
	return convert[runtimemodel.NotificationDeliveryPolicy](v)
}
func (s *Service) ListRecipientPreferences(ctx context.Context, principal principalmodel.Principal) ([]runtimemodel.NotificationRecipientPreference, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Delivery().ListRecipientPreferences(ctx, a)
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationRecipientPreference](v)
}
func (s *Service) SaveRecipientPreference(ctx context.Context, value runtimemodel.NotificationRecipientPreference, principal principalmodel.Principal) (runtimemodel.NotificationRecipientPreference, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	in, err := convert[sdkcontract.NotificationRecipientPreference](value)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	v, err := s.binding.Delivery().SaveRecipientPreference(ctx, a, in)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, mapError(err)
	}
	return convert[runtimemodel.NotificationRecipientPreference](v)
}

func inboxAuthority(ctx context.Context, surface surfacemodel.ProductSurface) (notificationsdk.UserAuthority, error) {
	return authority(ctx, string(surface))
}
func (s *Service) GetMyNotificationPreference(ctx context.Context, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationRecipientPreference, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	v, err := s.binding.Inbox().GetPreference(ctx, a, string(surface))
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, mapError(err)
	}
	return convert[runtimemodel.NotificationRecipientPreference](v)
}
func (s *Service) SaveMyNotificationPreference(ctx context.Context, value runtimemodel.NotificationRecipientPreference, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationRecipientPreference, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	in, err := convert[sdkcontract.NotificationRecipientPreference](value)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	v, err := s.binding.Inbox().SavePreference(ctx, a, string(surface), in)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, mapError(err)
	}
	return convert[runtimemodel.NotificationRecipientPreference](v)
}
func (s *Service) ListInbox(ctx context.Context, query runtimemodel.NotificationInboxQuery, cursor string, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxPage, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxPage{}, err
	}
	in, err := convert[sdkcontract.NotificationInboxQuery](query)
	if err != nil {
		return runtimemodel.NotificationInboxPage{}, err
	}
	v, err := s.binding.Inbox().List(ctx, a, in, cursor)
	if err != nil {
		return runtimemodel.NotificationInboxPage{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxPage](v)
}
func (s *Service) GetInboxItem(ctx context.Context, id string, query runtimemodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	in, err := convert[sdkcontract.NotificationInboxQuery](query)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	v, err := s.binding.Inbox().Get(ctx, a, id, in)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxItem](v)
}
func (s *Service) InboxFacets(ctx context.Context, query runtimemodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxFacets, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxFacets{}, err
	}
	in, err := convert[sdkcontract.NotificationInboxQuery](query)
	if err != nil {
		return runtimemodel.NotificationInboxFacets{}, err
	}
	v, err := s.binding.Inbox().Facets(ctx, a, in)
	if err != nil {
		return runtimemodel.NotificationInboxFacets{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxFacets](v)
}
func (s *Service) SetInboxRead(ctx context.Context, id string, value bool, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	v, err := s.binding.Inbox().SetRead(ctx, a, id, value)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxItem](v)
}
func (s *Service) SetInboxArchived(ctx context.Context, id string, value bool, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	v, err := s.binding.Inbox().SetArchived(ctx, a, id, value)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxItem](v)
}
func (s *Service) AcknowledgeInboxAlert(ctx context.Context, id string, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	v, err := s.binding.Inbox().AcknowledgeAlert(ctx, a, id)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxItem](v)
}
func (s *Service) MarkAllInboxRead(ctx context.Context, query runtimemodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (int, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return 0, err
	}
	in, err := convert[sdkcontract.NotificationInboxQuery](query)
	if err != nil {
		return 0, err
	}
	v, err := s.binding.Inbox().MarkAllRead(ctx, a, in)
	return v, mapError(err)
}
func (s *Service) ResolveInboxAction(ctx context.Context, id, key string, query runtimemodel.NotificationInboxQuery, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (runtimemodel.NotificationInboxResolvedAction, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxResolvedAction{}, err
	}
	in, err := convert[sdkcontract.NotificationInboxQuery](query)
	if err != nil {
		return runtimemodel.NotificationInboxResolvedAction{}, err
	}
	v, err := s.binding.Inbox().ResolveAction(ctx, a, id, key, in)
	if err != nil {
		return runtimemodel.NotificationInboxResolvedAction{}, mapError(err)
	}
	out, err := convert[runtimemodel.NotificationInboxResolvedAction](v)
	if err != nil {
		return out, err
	}
	if s.authorizeAction == nil {
		return out, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
	}
	if err := s.authorizeAction(ctx, out, principal); err != nil {
		return runtimemodel.NotificationInboxResolvedAction{}, err
	}
	return out, nil
}
func (s *Service) ListMyInboxDelegations(ctx context.Context, surface surfacemodel.ProductSurface, _ principalmodel.Principal) ([]runtimemodel.NotificationInboxDelegation, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Inbox().ListDelegations(ctx, a, string(surface))
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationInboxDelegation](v)
}
func (s *Service) SaveMyInboxDelegation(ctx context.Context, value runtimemodel.NotificationInboxDelegation, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxDelegation, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxDelegation{}, err
	}
	in, err := convert[sdkcontract.NotificationInboxDelegation](value)
	if err != nil {
		return runtimemodel.NotificationInboxDelegation{}, err
	}
	v, err := s.binding.Inbox().SaveDelegation(ctx, a, in)
	if err != nil {
		return runtimemodel.NotificationInboxDelegation{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxDelegation](v)
}
func (s *Service) DeleteMyInboxDelegation(ctx context.Context, id string, surface surfacemodel.ProductSurface, _ principalmodel.Principal) error {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return err
	}
	return mapError(s.binding.Inbox().DeleteDelegation(ctx, a, id))
}
func (s *Service) ListMyDelegatedInboxOwners(ctx context.Context, surface surfacemodel.ProductSurface, _ principalmodel.Principal) ([]string, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Inbox().ListDelegatedOwnerIDs(ctx, a, string(surface))
	return v, mapError(err)
}
func (s *Service) ListInboxSavedViews(ctx context.Context, surface surfacemodel.ProductSurface, _ principalmodel.Principal) ([]runtimemodel.NotificationInboxSavedView, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Inbox().ListSavedViews(ctx, a, string(surface))
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationInboxSavedView](v)
}
func (s *Service) SaveInboxSavedView(ctx context.Context, value runtimemodel.NotificationInboxSavedView, surface surfacemodel.ProductSurface, _ principalmodel.Principal) (runtimemodel.NotificationInboxSavedView, error) {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return runtimemodel.NotificationInboxSavedView{}, err
	}
	in, err := convert[sdkcontract.NotificationInboxSavedView](value)
	if err != nil {
		return runtimemodel.NotificationInboxSavedView{}, err
	}
	v, err := s.binding.Inbox().SaveSavedView(ctx, a, in)
	if err != nil {
		return runtimemodel.NotificationInboxSavedView{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxSavedView](v)
}
func (s *Service) DeleteInboxSavedView(ctx context.Context, key string, surface surfacemodel.ProductSurface, _ principalmodel.Principal) error {
	a, err := inboxAuthority(ctx, surface)
	if err != nil {
		return err
	}
	return mapError(s.binding.Inbox().DeleteSavedView(ctx, a, key))
}
