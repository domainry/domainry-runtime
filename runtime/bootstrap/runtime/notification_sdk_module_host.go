package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-notification-sdk/contract"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	hostsurfacemodel "github.com/domainry/domainry-runtime/runtime/domain/hostsurface/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
)

type notificationSDKModuleHost struct {
	store      *persistence.RuntimeStore
	identity   identitysdk.Binding
	clock      modulehost.Clock
	workerID   string
	catalog    modulehost.Catalog
	projection identitysdk.Projection
	workflow   workflowTaskLookup
	delivery   modulehost.DeliveryGateway
	metrics    modulehost.DeliveryMetrics
	validator  modulehost.ProviderTemplateValidator
	archives   modulehost.RetentionArchiveStore
}

func notificationSDKCatalog(defaultLocale string, projectTemplates []notificationmodel.NotificationTemplate, eventTypes []notificationmodel.NotificationEventType, projectRules []notificationmodel.NotificationRule) (modulehost.Catalog, error) {
	templates, err := notificationSDKConvert[[]contract.NotificationTemplate](projectTemplates)
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification templates to SDK catalog: %w", err)
	}
	events, err := notificationSDKConvert[[]contract.NotificationEventType](eventTypes)
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification event types to SDK catalog: %w", err)
	}
	rules, err := notificationSDKConvert[[]contract.NotificationRule](projectRules)
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification rules to SDK catalog: %w", err)
	}
	if err := validateNotificationAudienceResolverReferences(events, rules, hostsurfacemodel.NotificationAudienceResolverKeys()); err != nil {
		return modulehost.Catalog{}, fmt.Errorf("validate Notification audience resolver catalog: %w", err)
	}
	capabilities, err := notificationSDKConvert[[]contract.NotificationTemplateCapability](modulehost.DefaultProviderCapabilities())
	if err != nil {
		return modulehost.Catalog{}, fmt.Errorf("convert Notification provider capabilities to SDK catalog: %w", err)
	}
	return modulehost.Catalog{
		DefaultLocale:    defaultLocale,
		ExternalChannels: notificationModuleChannels(projectRules), Templates: templates,
		TemplateCapabilities: capabilities, EventTypes: events, Rules: rules,
	}, nil
}

// validateNotificationAudienceResolverReferences is a host-composition check,
// not Notification domain behavior. It proves every catalog reference has a
// Runtime-mounted resolver before the owner module is started.
func validateNotificationAudienceResolverReferences(eventTypes []contract.NotificationEventType, rules []contract.NotificationRule, resolverKeys []string) error {
	supported := make(map[string]bool, len(resolverKeys))
	for index, raw := range resolverKeys {
		key := strings.TrimSpace(raw)
		if key == "" || supported[key] {
			return fmt.Errorf("notification audience resolver inventory contains blank or duplicate key at index %d", index)
		}
		supported[key] = true
	}
	for eventIndex, eventType := range eventTypes {
		if err := validateNotificationAudienceResolverList(eventType.AudienceResolvers, supported); err != nil {
			return fmt.Errorf("notification_event_types[%d].audience_resolvers: %w", eventIndex, err)
		}
	}
	for ruleIndex, rule := range rules {
		if err := validateNotificationAudienceResolverList(rule.AudienceResolvers, supported); err != nil {
			return fmt.Errorf("notification_rules[%d].audience_resolvers: %w", ruleIndex, err)
		}
	}
	return nil
}

func validateNotificationAudienceResolverList(values []string, supported map[string]bool) error {
	seen := make(map[string]bool, len(values))
	for index, raw := range values {
		key := strings.TrimSpace(raw)
		if key == "" {
			return fmt.Errorf("resolver at index %d is blank", index)
		}
		if seen[key] {
			return fmt.Errorf("resolver %q is duplicated", key)
		}
		if !supported[key] {
			return fmt.Errorf("resolver %q is not implemented by the Runtime host", key)
		}
		seen[key] = true
	}
	return nil
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
func (h notificationSDKModuleHost) ManagedOperationStore() modulehost.ManagedOperationStore {
	return operationpersistence.NewSharedManagedStore(h.store)
}
func (h notificationSDKModuleHost) OperationControlStore() modulehost.OperationControlStore {
	return operationpersistence.NewSharedOperationControlStore(h.store)
}
func (h notificationSDKModuleHost) RetentionArchiveStore() modulehost.RetentionArchiveStore {
	return h.archives
}
func (h notificationSDKModuleHost) Identity() identitysdk.Binding { return h.identity }
func (h notificationSDKModuleHost) Clock() modulehost.Clock       { return h.clock }
func (h notificationSDKModuleHost) WorkerID() string              { return h.workerID }
func (h notificationSDKModuleHost) Catalog() modulehost.Catalog   { return h.catalog }
func (h notificationSDKModuleHost) WorkNotifier() modulehost.WorkNotifier {
	return notificationSDKWorkNotifier{h.store.WorkerWakeups()}
}
func (h notificationSDKModuleHost) RecipientResolver() modulehost.RecipientResolver {
	return notificationSDKRecipientResolver{h.projection}
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

type notificationSDKRecipientResolver struct{ projection identitysdk.Projection }

func (d notificationSDKRecipientResolver) FindRecipient(ctx context.Context, workspaceID, userID string) (modulehost.Recipient, bool, error) {
	if d.projection == nil {
		return modulehost.Recipient{}, false, nil
	}
	user, found, err := d.projection.FindUser(requestcontext.WithWorkspaceID(ctx, workspaceID), identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)})
	return modulehost.Recipient{ID: string(user.ID), Email: user.Email, Locale: user.Locale, Timezone: user.Timezone}, found, err
}

type notificationSDKWorkflowAudience struct{ lookup workflowTaskLookup }

func (a notificationSDKWorkflowAudience) ResolveAudience(ctx context.Context, key string, event contract.NotificationEvent) ([]string, error) {
	if strings.TrimSpace(key) != hostsurfacemodel.NotificationAudienceResolverWorkflowTaskAssignee || a.lookup == nil {
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
	operations integrationsdk.Operations
	clock      modulehost.Clock
}

func (m notificationSDKDeliveryMetrics) Metrics(ctx context.Context, workspaceID, since string) (contract.NotificationDeliveryMetrics, error) {
	if m.operations == nil {
		return contract.NotificationDeliveryMetrics{}, fmt.Errorf("Integration owner operations are unavailable for Notification delivery metrics")
	}
	invocations, err := m.operations.ListInvocations(ctx, integrationsdk.InvocationQuery{
		WorkspaceID: strings.TrimSpace(workspaceID),
		CreatedFrom: strings.TrimSpace(since),
		Limit:       500,
	})
	if err != nil {
		return contract.NotificationDeliveryMetrics{}, err
	}
	now := time.Now().UTC()
	if m.clock != nil {
		now = m.clock.Now().UTC()
	}
	result := contract.NotificationDeliveryMetrics{
		Since:       strings.TrimSpace(since),
		GeneratedAt: now.Format(time.RFC3339),
		Summary:     contract.NotificationDeliveryMetricBucket{Key: "all"},
	}
	channels := map[string]*contract.NotificationDeliveryMetricBucket{}
	templates := map[string]*contract.NotificationDeliveryMetricBucket{}
	failures := map[string]int{}
	for _, invocation := range invocations {
		payload := notificationInvocationPayload(invocation.Metadata)
		templateKey, _ := payload["template_key"].(string)
		templateKey = strings.TrimSpace(templateKey)
		if templateKey == "" {
			continue
		}
		channel, _ := payload["notification_channel"].(string)
		channel = strings.TrimSpace(channel)
		if channel == "" {
			channel = "unknown"
		}
		if channels[channel] == nil {
			channels[channel] = &contract.NotificationDeliveryMetricBucket{Key: channel}
		}
		if templates[templateKey] == nil {
			templates[templateKey] = &contract.NotificationDeliveryMetricBucket{Key: templateKey}
		}
		fallback := payload["notification_fallback_root_id"] != nil
		for _, bucket := range []*contract.NotificationDeliveryMetricBucket{&result.Summary, channels[channel], templates[templateKey]} {
			addIntegrationInvocationMetric(bucket, invocation.Status, fallback)
		}
		if strings.TrimSpace(invocation.Error) != "" {
			failures[strings.TrimSpace(invocation.Error)]++
		}
	}
	for _, bucket := range channels {
		result.ByChannel = append(result.ByChannel, *bucket)
	}
	for _, bucket := range templates {
		result.ByTemplate = append(result.ByTemplate, *bucket)
	}
	sort.Slice(result.ByChannel, func(i, j int) bool { return result.ByChannel[i].Key < result.ByChannel[j].Key })
	sort.Slice(result.ByTemplate, func(i, j int) bool {
		if result.ByTemplate[i].Total == result.ByTemplate[j].Total {
			return result.ByTemplate[i].Key < result.ByTemplate[j].Key
		}
		return result.ByTemplate[i].Total > result.ByTemplate[j].Total
	})
	for failure, count := range failures {
		result.Failures = append(result.Failures, contract.NotificationDeliveryFailureMetric{Error: failure, Count: count})
	}
	sort.Slice(result.Failures, func(i, j int) bool {
		if result.Failures[i].Count == result.Failures[j].Count {
			return result.Failures[i].Error < result.Failures[j].Error
		}
		return result.Failures[i].Count > result.Failures[j].Count
	})
	if len(result.Failures) > 10 {
		result.Failures = result.Failures[:10]
	}
	return result, nil
}

func notificationInvocationPayload(metadata map[string]any) map[string]any {
	if payload, ok := metadata["payload"].(map[string]any); ok {
		return payload
	}
	var raw []byte
	switch value := metadata["payload"].(type) {
	case json.RawMessage:
		raw = value
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	}
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	return result
}

func addIntegrationInvocationMetric(bucket *contract.NotificationDeliveryMetricBucket, status string, fallback bool) {
	bucket.Total++
	if fallback {
		bucket.Fallbacks++
	}
	switch strings.TrimSpace(status) {
	case "prepared", "running", "reconciling":
		bucket.Queued++
	case "succeeded":
		bucket.Sent++
	case "failed":
		bucket.Failed++
	case "reconciliation_required":
		bucket.DeadLetter++
	}
}

type notificationSDKDeliveryGateway struct {
	repository  publicationrepository.Repository
	productName string
	wakeup      func(publicationmodel.Message)
}

func (g *notificationSDKDeliveryGateway) BindWakeup(wakeup func(publicationmodel.Message)) {
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
	message, err := g.repository.InsertOutbox(ctx, request.WorkspaceID, publicationmodel.Message{
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
