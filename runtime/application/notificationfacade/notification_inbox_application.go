package notificationfacade

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	runtimemodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func inboxAuthority(ctx context.Context) (notificationsdk.UserAuthority, error) {
	return authority(ctx)
}
func (s *NotificationApplicationService) GetMyNotificationPreference(ctx context.Context, _ principalmodel.Principal) (runtimemodel.NotificationRecipientPreference, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	v, err := s.binding.Inbox().GetPreference(ctx, a)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, mapError(err)
	}
	return convert[runtimemodel.NotificationRecipientPreference](v)
}
func (s *NotificationApplicationService) SaveMyNotificationPreference(ctx context.Context, value runtimemodel.NotificationRecipientPreference, _ principalmodel.Principal) (runtimemodel.NotificationRecipientPreference, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	in, err := convert[sdkcontract.NotificationRecipientPreference](value)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, err
	}
	v, err := s.binding.Inbox().SavePreference(ctx, a, in)
	if err != nil {
		return runtimemodel.NotificationRecipientPreference{}, mapError(err)
	}
	return convert[runtimemodel.NotificationRecipientPreference](v)
}
func (s *NotificationApplicationService) ListInbox(ctx context.Context, query runtimemodel.NotificationInboxQuery, cursor string, _ principalmodel.Principal) (runtimemodel.NotificationInboxPage, error) {
	a, err := inboxAuthority(ctx)
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
func (s *NotificationApplicationService) GetInboxItem(ctx context.Context, id string, query runtimemodel.NotificationInboxQuery, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx)
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
func (s *NotificationApplicationService) InboxFacets(ctx context.Context, query runtimemodel.NotificationInboxQuery, _ principalmodel.Principal) (runtimemodel.NotificationInboxFacets, error) {
	a, err := inboxAuthority(ctx)
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
func (s *NotificationApplicationService) SetInboxRead(ctx context.Context, id string, value bool, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	v, err := s.binding.Inbox().SetRead(ctx, a, id, value)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxItem](v)
}
func (s *NotificationApplicationService) SetInboxArchived(ctx context.Context, id string, value bool, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	v, err := s.binding.Inbox().SetArchived(ctx, a, id, value)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxItem](v)
}
func (s *NotificationApplicationService) AcknowledgeInboxAlert(ctx context.Context, id string, _ principalmodel.Principal) (runtimemodel.NotificationInboxItem, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, err
	}
	v, err := s.binding.Inbox().AcknowledgeAlert(ctx, a, id)
	if err != nil {
		return runtimemodel.NotificationInboxItem{}, mapError(err)
	}
	return convert[runtimemodel.NotificationInboxItem](v)
}
func (s *NotificationApplicationService) MarkAllInboxRead(ctx context.Context, query runtimemodel.NotificationInboxQuery, _ principalmodel.Principal) (int, error) {
	a, err := inboxAuthority(ctx)
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
func (s *NotificationApplicationService) ResolveInboxAction(ctx context.Context, id, key string, query runtimemodel.NotificationInboxQuery, principal principalmodel.Principal) (runtimemodel.NotificationInboxResolvedAction, error) {
	a, err := inboxAuthority(ctx)
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
func (s *NotificationApplicationService) ListMyInboxDelegations(ctx context.Context, _ principalmodel.Principal) ([]runtimemodel.NotificationInboxDelegation, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Inbox().ListDelegations(ctx, a)
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationInboxDelegation](v)
}
func (s *NotificationApplicationService) SaveMyInboxDelegation(ctx context.Context, value runtimemodel.NotificationInboxDelegation, _ principalmodel.Principal) (runtimemodel.NotificationInboxDelegation, error) {
	a, err := inboxAuthority(ctx)
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
func (s *NotificationApplicationService) DeleteMyInboxDelegation(ctx context.Context, id string, _ principalmodel.Principal) error {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return err
	}
	return mapError(s.binding.Inbox().DeleteDelegation(ctx, a, id))
}
func (s *NotificationApplicationService) ListMyDelegatedInboxOwners(ctx context.Context, _ principalmodel.Principal) ([]string, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Inbox().ListDelegatedOwnerIDs(ctx, a)
	return v, mapError(err)
}
func (s *NotificationApplicationService) ListInboxSavedViews(ctx context.Context, _ principalmodel.Principal) ([]runtimemodel.NotificationInboxSavedView, error) {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.binding.Inbox().ListSavedViews(ctx, a)
	if err != nil {
		return nil, mapError(err)
	}
	return convert[[]runtimemodel.NotificationInboxSavedView](v)
}
func (s *NotificationApplicationService) SaveInboxSavedView(ctx context.Context, value runtimemodel.NotificationInboxSavedView, _ principalmodel.Principal) (runtimemodel.NotificationInboxSavedView, error) {
	a, err := inboxAuthority(ctx)
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
func (s *NotificationApplicationService) DeleteInboxSavedView(ctx context.Context, key string, _ principalmodel.Principal) error {
	a, err := inboxAuthority(ctx)
	if err != nil {
		return err
	}
	return mapError(s.binding.Inbox().DeleteSavedView(ctx, a, key))
}
