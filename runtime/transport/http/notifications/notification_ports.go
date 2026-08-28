package notifications

import (
	"context"

	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

// NotificationManagement is the topology-neutral BFF port. Both the legacy
// in-process application service and the Notification SDK facade implement it;
// HTTP route ownership therefore stays in Runtime without importing a concrete
// Notification deployment implementation.
type NotificationManagement interface {
	GovernanceCatalog(context.Context, principalmodel.Principal) (notificationmodel.NotificationGovernanceCatalog, error)
	List(context.Context, principalmodel.Principal) ([]notificationmodel.NotificationTemplateRecord, error)
	Get(context.Context, string, principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, bool, error)
	ListVersions(context.Context, string, principalmodel.Principal) ([]notificationmodel.NotificationTemplateVersion, error)
	ListPublicationRequests(context.Context, string, principalmodel.Principal) ([]notificationmodel.NotificationPublicationRequest, error)
	Capabilities(context.Context, principalmodel.Principal) ([]notificationcontract.NotificationProviderCapability, error)
	RestoreVersionDraft(context.Context, string, int, string, principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error)
	SaveDraft(context.Context, string, notificationmodel.NotificationTemplate, string, principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error)
	Disable(context.Context, string, string, principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error)
	Preview(context.Context, string, string, []string, map[string]any, principalmodel.Principal) (notificationmodel.RenderedNotification, error)
	PreviewTemplate(context.Context, notificationmodel.NotificationTemplate, string, []string, map[string]any, principalmodel.Principal) (notificationmodel.RenderedNotification, error)
	RequestPublication(context.Context, string, string, string, principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error)
	ApprovePublication(context.Context, string, principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error)
	RejectPublication(context.Context, string, string, principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error)
	CancelPublication(context.Context, string, principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error)
}

type NotificationDelivery interface {
	DeliveryMetrics(context.Context, string, principalmodel.Principal) (notificationmodel.NotificationDeliveryMetrics, error)
	GetDeliveryPolicy(context.Context, principalmodel.Principal) (notificationmodel.NotificationDeliveryPolicy, error)
	ListRecipientPreferences(context.Context, principalmodel.Principal) ([]notificationmodel.NotificationRecipientPreference, error)
	SaveDeliveryPolicy(context.Context, notificationmodel.NotificationDeliveryPolicy, principalmodel.Principal) (notificationmodel.NotificationDeliveryPolicy, error)
	SaveRecipientPreference(context.Context, notificationmodel.NotificationRecipientPreference, principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error)
}

type NotificationInbox interface {
	InboxGovernanceMetrics(context.Context, string, principalmodel.Principal) (notificationmodel.NotificationInboxGovernanceMetrics, error)
	GetMyNotificationPreference(context.Context, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error)
	SaveMyNotificationPreference(context.Context, notificationmodel.NotificationRecipientPreference, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error)
	ListInbox(context.Context, notificationmodel.NotificationInboxQuery, string, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxPage, error)
	GetInboxItem(context.Context, string, notificationmodel.NotificationInboxQuery, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxItem, error)
	InboxFacets(context.Context, notificationmodel.NotificationInboxQuery, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxFacets, error)
	SetInboxRead(context.Context, string, bool, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxItem, error)
	ResolveInboxAction(context.Context, string, string, notificationmodel.NotificationInboxQuery, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxResolvedAction, error)
	SetInboxArchived(context.Context, string, bool, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxItem, error)
	AcknowledgeInboxAlert(context.Context, string, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxItem, error)
	MarkAllInboxRead(context.Context, notificationmodel.NotificationInboxQuery, surfacemodel.ProductSurface, principalmodel.Principal) (int, error)
	ListMyInboxDelegations(context.Context, surfacemodel.ProductSurface, principalmodel.Principal) ([]notificationmodel.NotificationInboxDelegation, error)
	SaveMyInboxDelegation(context.Context, notificationmodel.NotificationInboxDelegation, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxDelegation, error)
	DeleteMyInboxDelegation(context.Context, string, surfacemodel.ProductSurface, principalmodel.Principal) error
	ListMyDelegatedInboxOwners(context.Context, surfacemodel.ProductSurface, principalmodel.Principal) ([]string, error)
	ListInboxSavedViews(context.Context, surfacemodel.ProductSurface, principalmodel.Principal) ([]notificationmodel.NotificationInboxSavedView, error)
	SaveInboxSavedView(context.Context, notificationmodel.NotificationInboxSavedView, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxSavedView, error)
	DeleteInboxSavedView(context.Context, string, surfacemodel.ProductSurface, principalmodel.Principal) error
}

type NotificationApplication interface {
	NotificationManagement
	NotificationDelivery
	NotificationInbox
}
