package notification

import (
	"context"
	"time"

	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	notificationservice "github.com/domainry/domainry-runtime/runtime/domain/notification/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

// NotificationApplicationService is the authenticated boundary around the
// Notification domain service. The domain service is intentionally private so
// callers cannot bypass workspace authorization through promoted methods.
type NotificationApplicationService struct {
	deliveryMetrics      NotificationDeliveryMetricsReader
	providerCapabilities []notificationcontract.NotificationProviderCapability
	authorizeAction      NotificationInboxActionAuthorizer
	eventCatalog         *notificationservice.NotificationEventCatalog
	mailboxManager       *sourceinbox.MailboxManager
	actionResolver       *sourceinbox.ActionResolver
	inboxCompiler        *sourceinbox.Compiler
	eventPublisher       *sourceinbox.Publisher
	inboxProcessor       *sourceinbox.Processor
	policyManager        *sourcedelivery.PolicyManager
	deliveryProcessor    *sourcedelivery.Processor
	templateManager      *sourcetemplate.Manager
	publicationProcessor *sourcetemplate.PublicationProcessor
	publicationWakeups   chan NotificationPublicationLocator
	inboxWakeups         <-chan workerplatform.DurableTaskLocator
	channelWakeups       <-chan workerplatform.DurableTaskLocator
}

type NotificationModuleDependencies struct {
	Mailbox           *sourceinbox.MailboxManager
	Actions           *sourceinbox.ActionResolver
	Compiler          *sourceinbox.Compiler
	Publisher         *sourceinbox.Publisher
	Processor         *sourceinbox.Processor
	Policy            *sourcedelivery.PolicyManager
	DeliveryProcessor *sourcedelivery.Processor
	Templates         *sourcetemplate.Manager
	Publications      *sourcetemplate.PublicationProcessor
	DeliveryMetrics   NotificationDeliveryMetricsReader
	Capabilities      []notificationcontract.NotificationProviderCapability
}

type NotificationDeliveryMetricsReader interface {
	DeliveryMetrics(context.Context, string, string) (notificationmodel.NotificationDeliveryMetrics, error)
}
type NotificationInboxActionAuthorizer func(context.Context, notificationmodel.NotificationInboxResolvedAction, principalmodel.Principal) error

func NewNotificationApplicationService() *NotificationApplicationService {
	return &NotificationApplicationService{publicationWakeups: make(chan NotificationPublicationLocator, 64)}
}

// NewNotificationApplicationServiceWithModule composes module-owned
// notification capabilities with Plane-owned authorization and host adapters.
func NewNotificationApplicationServiceWithModule(authorizeAction NotificationInboxActionAuthorizer, eventCatalog *notificationservice.NotificationEventCatalog, module NotificationModuleDependencies) *NotificationApplicationService {
	result := NewNotificationApplicationService()
	result.authorizeAction, result.eventCatalog = authorizeAction, eventCatalog
	result.mailboxManager, result.actionResolver = module.Mailbox, module.Actions
	result.inboxCompiler, result.eventPublisher, result.inboxProcessor = module.Compiler, module.Publisher, module.Processor
	result.policyManager = module.Policy
	result.deliveryProcessor = module.DeliveryProcessor
	result.templateManager, result.publicationProcessor = module.Templates, module.Publications
	result.deliveryMetrics = module.DeliveryMetrics
	result.providerCapabilities = append([]notificationcontract.NotificationProviderCapability(nil), module.Capabilities...)
	return result
}

func (s *NotificationApplicationService) GovernanceCatalog(_ context.Context, principal principalmodel.Principal) (notificationmodel.NotificationGovernanceCatalog, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return notificationmodel.NotificationGovernanceCatalog{}, err
	}
	return s.eventCatalog.GovernanceCatalog(), nil
}

func (s *NotificationApplicationService) InboxGovernanceMetrics(ctx context.Context, since string, principal principalmodel.Principal) (notificationmodel.NotificationInboxGovernanceMetrics, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return notificationmodel.NotificationInboxGovernanceMetrics{}, err
	}
	if s.mailboxManager == nil {
		return notificationmodel.NotificationInboxGovernanceMetrics{}, notificationInboxUnavailable()
	}
	value, err := s.mailboxManager.GovernanceMetrics(ctx, moduleWorkspaceID(principal.WorkspaceID), since)
	return planeGovernanceMetrics(value), mapNotificationModuleError(err)
}

func notificationAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func notificationAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func (s *NotificationApplicationService) List(ctx context.Context, principal principalmodel.Principal) ([]notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	values, err := s.templateManager.List(ctx)
	return planeTemplateRecords(values), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) Get(ctx context.Context, key string, principal principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, bool, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, false, err
	}
	value, found, err := s.templateManager.Get(ctx, key)
	return planeTemplateRecord(value), found, mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ListVersions(ctx context.Context, key string, principal principalmodel.Principal) ([]notificationmodel.NotificationTemplateVersion, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	values, err := s.templateManager.ListVersions(ctx, key)
	return planeTemplateVersions(values), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ListPublicationRequests(ctx context.Context, key string, principal principalmodel.Principal) ([]notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	values, err := s.publicationProcessor.List(ctx, key)
	return planePublicationRequests(values), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) Capabilities(ctx context.Context, principal principalmodel.Principal) ([]notificationcontract.NotificationProviderCapability, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	return append([]notificationcontract.NotificationProviderCapability(nil), s.providerCapabilities...), nil
}

func (s *NotificationApplicationService) DeliveryMetrics(ctx context.Context, since string, principal principalmodel.Principal) (notificationmodel.NotificationDeliveryMetrics, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return notificationmodel.NotificationDeliveryMetrics{}, err
	}
	return s.deliveryMetrics.DeliveryMetrics(ctx, principal.WorkspaceID, since)
}

func (s *NotificationApplicationService) GetDeliveryPolicy(ctx context.Context, principal principalmodel.Principal) (notificationmodel.NotificationDeliveryPolicy, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return notificationmodel.NotificationDeliveryPolicy{}, err
	}
	value, err := s.policyManager.GetPolicy(ctx)
	return planeDeliveryPolicy(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ListRecipientPreferences(ctx context.Context, principal principalmodel.Principal) ([]notificationmodel.NotificationRecipientPreference, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	values, err := s.policyManager.ListRecipientPreferences(ctx, moduleWorkspaceID(principal.WorkspaceID))
	return planeRecipientPreferences(values), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) RestoreVersionDraft(ctx context.Context, key string, version int, expected string, principal principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	value, err := s.templateManager.RestoreVersionDraft(ctx, key, version, expected, principal.UserID)
	return planeTemplateRecord(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) SaveDraft(ctx context.Context, key string, template notificationmodel.NotificationTemplate, expected string, principal principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	value, err := s.templateManager.SaveDraft(ctx, key, moduleTemplate(template), expected, principal.UserID)
	return planeTemplateRecord(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) Disable(ctx context.Context, key, expected string, principal principalmodel.Principal) (notificationmodel.NotificationTemplateRecord, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationTemplateRecord{}, err
	}
	value, err := s.templateManager.Disable(ctx, key, expected, principal.UserID)
	return planeTemplateRecord(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) Preview(ctx context.Context, key, locale string, recipients []string, variables map[string]any, principal principalmodel.Principal) (notificationmodel.RenderedNotification, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return notificationmodel.RenderedNotification{}, err
	}
	value, err := s.templateManager.Preview(ctx, moduleWorkspaceID(principal.WorkspaceID), key, locale, moduleUserIDs(recipients), variables)
	return planeRenderedNotification(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) PreviewTemplate(ctx context.Context, template notificationmodel.NotificationTemplate, locale string, recipients []string, variables map[string]any, principal principalmodel.Principal) (notificationmodel.RenderedNotification, error) {
	if err := notificationAuthorizeQuery(principal); err != nil {
		return notificationmodel.RenderedNotification{}, err
	}
	value, err := s.templateManager.PreviewTemplate(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleTemplate(template), locale, moduleUserIDs(recipients), variables)
	return planeRenderedNotification(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) RequestPublication(ctx context.Context, key, scheduled, expected string, principal principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationPublicationRequest{}, err
	}
	value, err := s.publicationProcessor.Request(ctx, key, scheduled, expected, principal.UserID)
	return planePublicationRequest(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) ApprovePublication(ctx context.Context, id string, principal principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationPublicationRequest{}, err
	}
	value, moduleErr := s.publicationProcessor.Approve(ctx, id, principal.UserID)
	request, err := planePublicationRequest(value), mapNotificationModuleError(moduleErr)
	if err == nil && request.Status == string(sourcetemplate.PublicationScheduled) {
		s.WakePublication(ctx, NotificationPublicationLocator{RequestID: request.ID})
	}
	return request, err
}

func (s *NotificationApplicationService) RejectPublication(ctx context.Context, id, reason string, principal principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationPublicationRequest{}, err
	}
	value, err := s.publicationProcessor.Reject(ctx, id, principal.UserID, reason)
	return planePublicationRequest(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) CancelPublication(ctx context.Context, id string, principal principalmodel.Principal) (notificationmodel.NotificationPublicationRequest, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationPublicationRequest{}, err
	}
	value, err := s.publicationProcessor.Cancel(ctx, id, principal.UserID)
	return planePublicationRequest(value), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) SaveDeliveryPolicy(ctx context.Context, value notificationmodel.NotificationDeliveryPolicy, principal principalmodel.Principal) (notificationmodel.NotificationDeliveryPolicy, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationDeliveryPolicy{}, err
	}
	stored, err := s.policyManager.SavePolicy(ctx, moduleDeliveryPolicy(value), principal.UserID)
	return planeDeliveryPolicy(stored), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) SaveRecipientPreference(ctx context.Context, value notificationmodel.NotificationRecipientPreference, principal principalmodel.Principal) (notificationmodel.NotificationRecipientPreference, error) {
	if err := notificationAuthorizeCommand(principal); err != nil {
		return notificationmodel.NotificationRecipientPreference{}, err
	}
	stored, err := s.policyManager.SaveRecipientPreference(ctx, moduleWorkspaceID(principal.WorkspaceID), moduleRecipientPreference(value), principal.UserID)
	return planeRecipientPreference(stored), mapNotificationModuleError(err)
}

func (s *NotificationApplicationService) RefreshPublished(ctx context.Context, scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	if scope.Kind != principalmodel.SystemScopeInstallation {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required"}
	}
	return mapNotificationModuleError(s.templateManager.RefreshPublished(ctx))
}

func (s *NotificationApplicationService) ProcessDuePublications(ctx context.Context, limit int, scope principalmodel.SystemScope) (int, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required", Err: err}
	}
	if scope.Kind != principalmodel.SystemScopeInstallation {
		return 0, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.system_scope_required"}
	}
	var depth int
	var lag time.Duration
	var err error
	depth, lag, err = s.publicationProcessor.QueueStats(ctx, limit)
	if err != nil {
		workerplatform.ObserveOutcome("notification_publication", "failed")
		return 0, err
	}
	workerplatform.SetQueueMetrics("notification_publication", depth, lag)
	var processed int
	processed, err = s.publicationProcessor.ProcessDue(ctx, limit)
	if err != nil {
		workerplatform.ObserveOutcome("notification_publication", "failed")
		return processed, err
	}
	for index := 0; index < processed; index++ {
		workerplatform.ObserveOutcome("notification_publication", "claimed")
		workerplatform.ObserveOutcome("notification_publication", "completed")
	}
	return processed, nil
}

func (s *NotificationApplicationService) EvaluateDelivery(ctx context.Context, request notificationmodel.NotificationDeliveryEvaluationRequest) (notificationmodel.NotificationDeliveryDecision, error) {
	if _, err := principalmodel.NewWorkspaceCommandScope(request.WorkspaceID); err != nil {
		return notificationmodel.NotificationDeliveryDecision{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	decision, err := s.policyManager.EvaluateDelivery(ctx, moduleDeliveryEvaluation(request))
	return planeDeliveryDecision(decision), mapNotificationModuleError(err)
}
