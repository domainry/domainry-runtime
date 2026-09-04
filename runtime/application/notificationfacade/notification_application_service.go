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

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	runtimemodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ActionAuthorizer func(context.Context, runtimemodel.NotificationInboxResolvedAction, principalmodel.Principal) error

type NotificationApplicationService struct {
	binding         notificationsdk.Binding
	authorizeAction ActionAuthorizer
}

func NewNotificationApplicationService(binding notificationsdk.Binding, authorizeAction ActionAuthorizer) (*NotificationApplicationService, error) {
	if binding == nil || binding.Templates() == nil || binding.Delivery() == nil || binding.Inbox() == nil || binding.Administration() == nil {
		return nil, errors.New("Notification SDK Binding returned incomplete Runtime ports")
	}
	return &NotificationApplicationService{binding: binding, authorizeAction: authorizeAction}, nil
}

func authority(ctx context.Context) (notificationsdk.UserAuthority, error) {
	identity, ok := identitysdk.RequestIdentityFromContext(ctx)
	if !ok || strings.TrimSpace(identity.AccessToken) == "" {
		return notificationsdk.UserAuthority{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.user_authority_required"}
	}
	return notificationsdk.UserAuthority{AccessToken: identity.AccessToken}, nil
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

func (s *NotificationApplicationService) user(ctx context.Context, _ principalmodel.Principal) (notificationsdk.UserAuthority, error) {
	return authority(ctx)
}

func (s *NotificationApplicationService) PublishInboxIntent(ctx context.Context, value runtimemodel.NotificationIntent, scope principalmodel.SystemScope) (runtimemodel.NotificationEvent, bool, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return runtimemodel.NotificationEvent{}, false, err
	}
	input, err := convert[sdkcontract.NotificationIntent](value)
	if err != nil {
		return runtimemodel.NotificationEvent{}, false, err
	}
	event, created, err := s.binding.Publisher().PublishIntent(ctx, input)
	if err != nil {
		return runtimemodel.NotificationEvent{}, false, mapError(err)
	}
	result, err := convert[runtimemodel.NotificationEvent](event)
	return result, created, err
}

func (s *NotificationApplicationService) GovernanceCatalog(ctx context.Context, principal principalmodel.Principal) (runtimemodel.NotificationGovernanceCatalog, error) {
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
func (s *NotificationApplicationService) InboxGovernanceMetrics(ctx context.Context, since string, principal principalmodel.Principal) (runtimemodel.NotificationInboxGovernanceMetrics, error) {
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
func (s *NotificationApplicationService) Capabilities(ctx context.Context, principal principalmodel.Principal) ([]sdkcontract.NotificationTemplateCapability, error) {
	a, err := s.user(ctx, principal)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Templates().Capabilities(ctx, a)
	if err != nil {
		return nil, mapError(err)
	}
	return v, nil
}
func (s *NotificationApplicationService) List(ctx context.Context, principal principalmodel.Principal) ([]runtimemodel.NotificationTemplateRecord, error) {
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
func (s *NotificationApplicationService) Get(ctx context.Context, key string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, bool, error) {
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
func (s *NotificationApplicationService) ListVersions(ctx context.Context, key string, principal principalmodel.Principal) ([]runtimemodel.NotificationTemplateVersion, error) {
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
func (s *NotificationApplicationService) ListPublicationRequests(ctx context.Context, key string, principal principalmodel.Principal) ([]runtimemodel.NotificationPublicationRequest, error) {
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
func (s *NotificationApplicationService) RestoreVersionDraft(ctx context.Context, key string, version int, expected string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, error) {
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
func (s *NotificationApplicationService) SaveDraft(ctx context.Context, key string, value runtimemodel.NotificationTemplate, expected string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, error) {
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
func (s *NotificationApplicationService) Disable(ctx context.Context, key, expected string, principal principalmodel.Principal) (runtimemodel.NotificationTemplateRecord, error) {
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
func (s *NotificationApplicationService) Preview(ctx context.Context, key, locale string, recipients []string, variables map[string]any, principal principalmodel.Principal) (runtimemodel.RenderedNotification, error) {
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
func (s *NotificationApplicationService) PreviewTemplate(ctx context.Context, value runtimemodel.NotificationTemplate, locale string, recipients []string, variables map[string]any, principal principalmodel.Principal) (runtimemodel.RenderedNotification, error) {
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
func (s *NotificationApplicationService) RequestPublication(ctx context.Context, key, scheduled, expected string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
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
func (s *NotificationApplicationService) ApprovePublication(ctx context.Context, id string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
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
func (s *NotificationApplicationService) RejectPublication(ctx context.Context, id, reason string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
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
func (s *NotificationApplicationService) CancelPublication(ctx context.Context, id string, principal principalmodel.Principal) (runtimemodel.NotificationPublicationRequest, error) {
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

func (s *NotificationApplicationService) DeliveryMetrics(ctx context.Context, since string, principal principalmodel.Principal) (runtimemodel.NotificationDeliveryMetrics, error) {
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
func (s *NotificationApplicationService) GetDeliveryPolicy(ctx context.Context, principal principalmodel.Principal) (runtimemodel.NotificationDeliveryPolicy, error) {
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
func (s *NotificationApplicationService) SaveDeliveryPolicy(ctx context.Context, value runtimemodel.NotificationDeliveryPolicy, principal principalmodel.Principal) (runtimemodel.NotificationDeliveryPolicy, error) {
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
func (s *NotificationApplicationService) ListRecipientPreferences(ctx context.Context, principal principalmodel.Principal) ([]runtimemodel.NotificationRecipientPreference, error) {
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
func (s *NotificationApplicationService) SaveRecipientPreference(ctx context.Context, value runtimemodel.NotificationRecipientPreference, principal principalmodel.Principal) (runtimemodel.NotificationRecipientPreference, error) {
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
