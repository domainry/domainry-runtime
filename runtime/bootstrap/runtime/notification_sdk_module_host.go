package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	runtimecontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type notificationSDKModuleHost struct {
	store     *persistence.RuntimeStore
	identity  identitysdk.Binding
	clock     modulehost.Clock
	workerID  string
	catalog   modulehost.Catalog
	directory identitysdk.Directory
	workflow  workflowTaskLookup
	delivery  modulehost.DeliveryGateway
	metrics   modulehost.DeliveryMetrics
	validator modulehost.ProviderTemplateValidator
}

func notificationSDKCatalog(defaultLocale string, manifest manifestmodel.ManifestSchema, eventTypes []notificationmodel.NotificationEventType) (modulehost.Catalog, error) {
	templates, err := notificationSDKConvert[[]contract.NotificationTemplate](manifest.NotificationTemplates)
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification templates to SDK catalog: %w", err)
	}
	events, err := notificationSDKConvert[[]contract.NotificationEventType](eventTypes)
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification event types to SDK catalog: %w", err)
	}
	rules, err := notificationSDKConvert[[]contract.NotificationRule](manifest.NotificationRules)
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification rules to SDK catalog: %w", err)
	}
	capabilities, err := notificationSDKConvert[[]contract.NotificationTemplateCapability](runtimecontract.NotificationProviderCapabilities())
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification provider capabilities to SDK catalog: %w", err)
	}
	return modulehost.Catalog{
		DefaultLocale:    defaultLocale,
		Surfaces:         []string{runtimeext.SurfaceBusinessWorkspace, runtimeext.SurfaceConsumerPortal},
		ExternalChannels: notificationModuleChannels(manifest.NotificationRules), Templates: templates,
		TemplateCapabilities: capabilities, EventTypes: events, Rules: rules,
	}, nil
}

func (h notificationSDKModuleHost) Database() modulehost.Database             { return h.store.DB() }
func (h notificationSDKModuleHost) Migrations() modulehost.MigrationRegistrar { return h.store }
func (h notificationSDKModuleHost) Dialect() modulehost.Dialect {
	return notificationSDKDialect{h.store}
}
func (h notificationSDKModuleHost) WorkspaceScope() modulehost.WorkspaceScope {
	return notificationSDKWorkspaceScope{}
}
func (h notificationSDKModuleHost) QueueScopes() modulehost.QueueScopeIndex {
	return notificationSDKQueueScopes{h.store}
}
func (h notificationSDKModuleHost) Identity() identitysdk.Binding { return h.identity }
func (h notificationSDKModuleHost) Clock() modulehost.Clock       { return h.clock }
func (h notificationSDKModuleHost) WorkerID() string              { return h.workerID }
func (h notificationSDKModuleHost) Catalog() modulehost.Catalog   { return h.catalog }
func (h notificationSDKModuleHost) WorkNotifier() modulehost.WorkNotifier {
	return notificationSDKWorkNotifier{h.store.WorkerWakeups()}
}
func (h notificationSDKModuleHost) RecipientDirectory() modulehost.RecipientDirectory {
	return notificationSDKRecipientDirectory{h.directory}
}
func (h notificationSDKModuleHost) AudienceResolver() modulehost.AudienceResolver {
	return notificationSDKWorkflowAudience{h.workflow}
}
func (h notificationSDKModuleHost) DeliveryGateway() modulehost.DeliveryGateway { return h.delivery }
func (h notificationSDKModuleHost) DeliveryMetrics() modulehost.DeliveryMetrics { return h.metrics }
func (h notificationSDKModuleHost) ProviderTemplateValidator() modulehost.ProviderTemplateValidator {
	return h.validator
}

type notificationSDKDialect struct{ store *persistence.RuntimeStore }

func (d notificationSDKDialect) Identifier(value string) string { return d.store.Identifier(value) }
func (d notificationSDKDialect) Table(value string) string      { return d.store.TableIdentifier(value) }
func (d notificationSDKDialect) Placeholder(position int) string {
	return d.store.Placeholder(position)
}
func (d notificationSDKDialect) Insert(table string, columns []string) string {
	return d.store.InsertStatement(table, columns)
}

type notificationSDKWorkspaceScope struct{}

func (notificationSDKWorkspaceScope) Context(ctx context.Context, workspaceID string) context.Context {
	return requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
}

type notificationSDKQueueScopes struct{ store *persistence.RuntimeStore }

func (q notificationSDKQueueScopes) Register(ctx context.Context, executor modulehost.Executor, kind, workspaceID, updatedAt string) error {
	return q.store.RegisterWorkerQueueScope(ctx, executor, kind, workspaceID, updatedAt)
}
func (q notificationSDKQueueScopes) Workspaces(ctx context.Context, queryer modulehost.Queryer, kind string, limit int) ([]string, error) {
	return q.store.WorkerQueueScopePage(ctx, queryer, kind, limit)
}

type notificationSDKWorkNotifier struct{ broker *workerplatform.WakeupBroker }

func (n notificationSDKWorkNotifier) Notify(_ context.Context, work modulehost.WorkLocator) {
	if n.broker != nil {
		n.broker.Publish(workerplatform.DurableTaskLocator{QueueKind: work.Kind, WorkspaceID: work.WorkspaceID, TaskID: work.TaskID})
	}
}

type notificationSDKRecipientDirectory struct{ directory identitysdk.Directory }

func (d notificationSDKRecipientDirectory) FindRecipient(ctx context.Context, workspaceID, userID string) (modulehost.Recipient, bool, error) {
	if d.directory == nil {
		return modulehost.Recipient{}, false, nil
	}
	user, found, err := d.directory.FindUser(requestcontext.WithWorkspaceID(ctx, workspaceID), identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)})
	return modulehost.Recipient{ID: string(user.ID), Email: user.Email, Locale: user.Locale, Timezone: user.Timezone}, found, err
}

type notificationSDKWorkflowAudience struct{ lookup workflowTaskLookup }

func (a notificationSDKWorkflowAudience) ResolveAudience(ctx context.Context, key string, event contract.NotificationEvent) ([]string, error) {
	if strings.TrimSpace(key) != "workflow_task_assignee" || a.lookup == nil {
		return nil, &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.notification.inbox_audience_resolver_unavailable"}
	}
	task, found, err := a.lookup(ctx, event.WorkspaceID, event.SubjectID)
	if err != nil {
		return nil, err
	}
	if !found || strings.TrimSpace(task.AssigneeUserID) == "" {
		return nil, &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_audience_resolution_empty"}
	}
	return []string{task.AssigneeUserID}, nil
}

type notificationSDKDeliveryMetrics struct {
	store notificationpersistence.DeliveryMetricsStore
}

func (m notificationSDKDeliveryMetrics) Metrics(ctx context.Context, workspaceID, since string) (contract.NotificationDeliveryMetrics, error) {
	value, err := m.store.DeliveryMetrics(ctx, workspaceID, since)
	if err != nil {
		return contract.NotificationDeliveryMetrics{}, err
	}
	return notificationSDKConvert[contract.NotificationDeliveryMetrics](value)
}

type notificationSDKDeliveryGateway struct {
	repository  integrationrepository.IntegrationDeliveryRepository
	productName string
	wakeup      func(integrationmodel.IntegrationOutboxMessage)
}

func (g *notificationSDKDeliveryGateway) BindWakeup(wakeup func(integrationmodel.IntegrationOutboxMessage)) {
	if g != nil {
		g.wakeup = wakeup
	}
}

func (g *notificationSDKDeliveryGateway) Dispatch(ctx context.Context, request modulehost.DeliveryRequest) (modulehost.DeliveryReceipt, error) {
	if g.repository == nil || strings.TrimSpace(g.productName) == "" {
		return modulehost.DeliveryReceipt{}, fmt.Errorf("notification Integration delivery gateway is unavailable")
	}
	payload, err := notificationSDKOutboxPayload(request.Rendered, g.productName)
	if err != nil {
		return modulehost.DeliveryReceipt{}, err
	}
	if request.DeliverAfter != "" {
		payload["notification_deliver_after"] = request.DeliverAfter
	}
	if len(request.Fallbacks) > 0 {
		fallbacks := make([]any, len(request.Fallbacks))
		for index, fallback := range request.Fallbacks {
			fallbackPayload, fallbackErr := notificationSDKOutboxPayload(fallback.Rendered, g.productName)
			if fallbackErr != nil {
				return modulehost.DeliveryReceipt{}, fallbackErr
			}
			fallbacks[index] = map[string]any{"connector_key": fallback.ConnectorKey, "connection_key": fallback.ConnectionKey, "operation": fallback.Operation, "channel": fallback.Rendered.Channel, "payload": fallbackPayload, "source_index": index}
		}
		payload["notification_fallback_plan"], payload["notification_fallback_hop"] = fallbacks, 0
	}
	createdAt := strings.TrimSpace(request.CreatedAt)
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	message, err := g.repository.InsertOutbox(ctx, request.WorkspaceID, integrationmodel.IntegrationOutboxMessage{
		WorkspaceID: request.WorkspaceID, ConnectorKey: request.ConnectorKey, ConnectionKey: request.ConnectionKey,
		Operation: request.Operation, Status: "queued", Payload: payload, EventID: request.EventID,
		DedupKey: request.DedupeKey, CreatedBy: "notification", CreatedAt: createdAt,
	})
	if err != nil {
		return modulehost.DeliveryReceipt{}, err
	}
	if g.wakeup != nil {
		g.wakeup(message)
	}
	return modulehost.DeliveryReceipt{MessageID: message.ID}, nil
}

func notificationSDKOutboxPayload(content contract.RenderedNotification, productName string) (map[string]any, error) {
	metadata := cloneSDKMap(content.Metadata)
	payload := map[string]any{
		"template_key": content.TemplateKey, "template_version": content.TemplateVersion, "template_locale": content.TemplateLocale,
		"template_content_hash": content.TemplateContentHash, "variables_hash": content.VariablesHash,
		"notification_channel": content.Channel, "notification_provider": content.Provider, "notification_metadata": metadata,
	}
	if content.Channel == "email" {
		payload["to"], payload["subject"], payload["text"], payload["html"] = append([]string(nil), content.Recipients...), content.Subject, content.Text, content.HTML
		return payload, nil
	}
	if (content.Channel != "collaboration" && content.Channel != "whatsapp") || len(content.Recipients) != 1 {
		return nil, fmt.Errorf("notification delivery content has unsupported channel or recipient cardinality")
	}
	payload["recipient"], payload["message"] = content.Recipients[0], content.Message
	facts, actions := make([]any, len(content.Facts)), make([]any, len(content.Actions))
	for index, fact := range content.Facts {
		facts[index] = map[string]any{"key": fact.Key, "value": fact.Value}
	}
	for index, action := range content.Actions {
		actions[index] = map[string]any{"label": action.Label, "url": action.URL, "style": action.Style}
	}
	portable := map[string]any{"schema_version": 1, "product_name": productName, "subject": content.Subject, "title": content.Title, "text": content.Text, "html": content.HTML, "markdown": content.Markdown, "message": content.Message, "facts": facts, "actions": actions, "metadata": metadata, "template": map[string]any{"key": content.TemplateKey, "version": content.TemplateVersion, "locale": content.TemplateLocale, "content_hash": content.TemplateContentHash, "variables_hash": content.VariablesHash}}
	if content.ProviderTemplate != nil {
		portable["provider_template"] = content.ProviderTemplate
	}
	payload["notification_content"] = portable
	return payload, nil
}

func cloneSDKMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

type notificationSDKProviderTemplateValidator struct{}

var notificationSDKTemplateName = regexp.MustCompile(`^[a-z0-9_]+$`)
var notificationSDKTemplateLanguage = regexp.MustCompile(`^[a-z]{2,3}([_-][A-Z]{2})?$`)
var notificationSDKButtonIndex = regexp.MustCompile(`^[0-9]$`)

func (notificationSDKProviderTemplateValidator) ValidateProviderTemplate(channel, provider string, value contract.NotificationProviderTemplate) error {
	if channel != "whatsapp" || provider == "" || !notificationSDKTemplateName.MatchString(strings.TrimSpace(value.Name)) || !notificationSDKTemplateLanguage.MatchString(strings.TrimSpace(value.Language)) || len(value.Components) > 12 {
		return fmt.Errorf("invalid WhatsApp provider template")
	}
	for _, component := range value.Components {
		typeName, subtype, index := strings.TrimSpace(component.Type), strings.TrimSpace(component.SubType), strings.TrimSpace(component.Index)
		if (typeName == "header" || typeName == "body") && (subtype != "" || index != "") {
			return fmt.Errorf("invalid WhatsApp provider template component")
		}
		if typeName == "button" && ((subtype != "url" && subtype != "quick_reply") || !notificationSDKButtonIndex.MatchString(index)) {
			return fmt.Errorf("invalid WhatsApp provider template button")
		}
		if typeName != "header" && typeName != "body" && typeName != "button" {
			return fmt.Errorf("invalid WhatsApp provider template component")
		}
		if len(component.Parameters) == 0 || len(component.Parameters) > 10 {
			return fmt.Errorf("invalid WhatsApp provider template parameters")
		}
		for _, parameter := range component.Parameters {
			if strings.TrimSpace(parameter) == "" {
				return fmt.Errorf("invalid WhatsApp provider template parameters")
			}
		}
	}
	return nil
}

func notificationSDKConvert[To any, From any](value From) (To, error) {
	var result To
	encoded, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(encoded, &result)
	return result, err
}

var _ modulehost.Host = notificationSDKModuleHost{}
var _ modulehost.DeliveryGateway = (*notificationSDKDeliveryGateway)(nil)
var _ modulehost.DeliveryMetrics = notificationSDKDeliveryMetrics{}
var _ modulehost.ProviderTemplateValidator = notificationSDKProviderTemplateValidator{}
