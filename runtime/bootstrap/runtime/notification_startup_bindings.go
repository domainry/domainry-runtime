package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	sourcenotification "github.com/domainry/domainry-notification"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationapplication "github.com/domainry/domainry-runtime/runtime/application/notification"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type workflowTaskLookup func(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error)
type schedulerDefinitionLookup func(context.Context, principalmodel.SystemScope, string, string) (metadatamodel.MetadataDefinition, bool, error)
type recordBatchJobLookup func(context.Context, string, string) (recordmodel.RecordBatchJob, bool, error)
type reportCatalogLookup func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema
type automationRuleLookup func(context.Context, string, principalmodel.Principal) (automationmodel.AutomationRuleSchema, error)
type notificationIntentPublisher func(context.Context, notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error)
type notificationRecipientLookup func(context.Context, string) (identitysdk.User, bool, error)

func runtimeActionRevisions(manifest manifestmodel.ManifestSchema) (string, string) {
	projectRevision, metadataRevision := manifest.ManifestHash, manifest.ManifestHash
	if manifest.GeneratedDomainSDK != nil {
		if manifest.GeneratedDomainSDK.ArtifactSHA256 != "" {
			projectRevision = manifest.GeneratedDomainSDK.ArtifactSHA256
		}
		if manifest.GeneratedDomainSDK.MetadataSnapshotSHA256 != "" {
			metadataRevision = manifest.GeneratedDomainSDK.MetadataSnapshotSHA256
		}
	}
	return projectRevision, metadataRevision
}

func notificationIntentPublisherCallback(publisher notificationIntentPublisher) func(context.Context, notificationmodel.NotificationIntent) error {
	return func(ctx context.Context, intent notificationmodel.NotificationIntent) error {
		if publisher == nil {
			return nil
		}
		_, _, err := publisher(ctx, intent, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "publish record batch terminal notification"))
		return err
	}
}

func newNotificationRecipientLocaleResolver(lookup notificationRecipientLookup) func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, workspaceID, recipientID string) (string, error) {
		user, found, err := lookup(requestcontext.WithWorkspaceID(ctx, workspaceID), recipientID)
		if err != nil || !found {
			return "", err
		}
		return user.Locale, nil
	}
}

func requireRuntimeSchemaRevision(revision string) error {
	if strings.TrimSpace(revision) == "" {
		return fmt.Errorf("project Runtime schema revision is empty")
	}
	return nil
}

func newWorkflowTaskAssigneeResolver(lookup workflowTaskLookup) notificationapplication.NotificationAudienceResolver {
	return func(ctx context.Context, event sourceinbox.Event) ([]sourcenotification.UserID, error) {
		task, found, err := lookup(ctx, event.WorkspaceID.String(), event.SubjectID)
		if err != nil {
			return nil, err
		}
		if !found || strings.TrimSpace(task.AssigneeUserID) == "" {
			return nil, &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_audience_resolution_empty"}
		}
		return []sourcenotification.UserID{sourcenotification.UserID(task.AssigneeUserID)}, nil
	}
}

type runtimeNotificationRecipientLocale struct{ lookup notificationRecipientLookup }

func (r runtimeNotificationRecipientLocale) RecipientLocale(ctx context.Context, workspaceID sourcenotification.WorkspaceID, recipientID sourcenotification.UserID) (string, error) {
	return newNotificationRecipientLocaleResolver(r.lookup)(ctx, workspaceID.String(), recipientID.String())
}

type runtimeNotificationWorkNotifier struct{ broker *workerplatform.WakeupBroker }

func (n runtimeNotificationWorkNotifier) Notify(_ context.Context, work sourcenotification.Work) {
	if n.broker == nil {
		return
	}
	n.broker.Publish(workerplatform.DurableTaskLocator{QueueKind: string(work.Kind), WorkspaceID: work.WorkspaceID.String(), TaskID: work.TaskID})
}

func newWorkflowTaskNotificationActionAuthorizer(lookup workflowTaskLookup) func(context.Context, string, principalmodel.Principal) error {
	return func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		task, found, err := lookup(ctx, principal.WorkspaceID, resourceID)
		if err != nil {
			return err
		}
		if !found {
			return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
		}
		if task.AssigneeUserID != principal.UserID {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		if task.Status != "open" {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
		}
		return nil
	}
}

func newSchedulerNotificationActionAuthorizer(lookup schedulerDefinitionLookup) func(context.Context, string, principalmodel.Principal) error {
	return func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		allowed := principal.HasPermission("workspace.admin") || principal.HasPermission("scheduler.definition.read")
		if !allowed {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "authorize scheduler notification action")
		_, found, err := lookup(ctx, scope, "scheduler", resourceID)
		if err != nil {
			return err
		}
		if !found {
			return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
		}
		return nil
	}
}

func newRecordBatchNotificationActionAuthorizer(lookup recordBatchJobLookup) func(context.Context, string, principalmodel.Principal) error {
	return func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		job, found, err := lookup(ctx, principal.WorkspaceID, resourceID)
		if err != nil {
			return err
		}
		if !found {
			return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
		}
		if job.ActorID != principal.UserID {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		return nil
	}
}

func newReportNotificationActionAuthorizer(reports reportCatalogLookup) func(context.Context, string, principalmodel.Principal) error {
	return func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		for _, report := range reports(ctx, principal) {
			if report.Key == strings.TrimSpace(resourceID) {
				return nil
			}
		}
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
	}
}

func newAutomationNotificationActionAuthorizer(lookup automationRuleLookup) func(context.Context, string, principalmodel.Principal) error {
	return func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		_, err := lookup(ctx, strings.TrimSpace(resourceID), principal)
		if apperror.KindOf(err) == apperror.KindNotFound {
			return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found", Err: err}
		}
		return err
	}
}

// notificationModuleCatalog converts Runtime authoring values at the host
// composition boundary. Source modules still own event definitions; the
// extracted notification module owns validation and immutable lookup behavior.
func notificationModuleCatalog(validator *sourceinbox.Validator, eventTypes []notificationmodel.NotificationEventType, rules []notificationmodel.NotificationRule) (*sourceinbox.Catalog, error) {
	sourceEventTypes := make([]sourceinbox.EventType, len(eventTypes))
	for index, eventType := range eventTypes {
		surfaces := make([]sourcenotification.Surface, len(eventType.Surfaces))
		for surfaceIndex, surface := range eventType.Surfaces {
			surfaces[surfaceIndex] = sourcenotification.Surface(surface)
		}
		locales := make(map[string]sourceinbox.Content, len(eventType.Locales))
		for locale, content := range eventType.Locales {
			facts := make([]sourcetemplate.Fact, len(content.Facts))
			for factIndex, fact := range content.Facts {
				facts[factIndex] = sourcetemplate.Fact{Key: fact.Key, Value: fact.Value}
			}
			labels := make(map[string]string, len(content.ActionLabels))
			for key, label := range content.ActionLabels {
				labels[key] = label
			}
			locales[locale] = sourceinbox.Content{Title: content.Title, Body: content.Body, Facts: facts, ActionLabels: labels}
		}
		variables := make([]sourcetemplate.Variable, len(eventType.Variables))
		for variableIndex, variable := range eventType.Variables {
			variables[variableIndex] = sourcetemplate.Variable{Key: variable.Key, Type: variable.Type, Required: variable.Required}
		}
		actions := make([]sourceinbox.ActionDescriptor, len(eventType.Actions))
		for actionIndex, action := range eventType.Actions {
			routes := make(map[string]string, len(action.SurfaceRoutes))
			for surface, route := range action.SurfaceRoutes {
				routes[surface] = route
			}
			actions[actionIndex] = sourceinbox.ActionDescriptor{Key: action.Key, Kind: action.Kind, ResourceType: action.ResourceType, SurfaceRoutes: routes}
		}
		sourceEventTypes[index] = sourceinbox.EventType{
			Key: eventType.Key, Source: eventType.Source, Category: eventType.Category, DefaultSeverity: eventType.DefaultSeverity,
			Surfaces: surfaces, MandatoryInApp: eventType.MandatoryInApp, TemplateKey: eventType.TemplateKey, DefaultLocale: eventType.DefaultLocale,
			Locales: locales, Variables: variables, Actions: actions, AudienceResolvers: append([]string(nil), eventType.AudienceResolvers...),
			Version: eventType.Version, Status: eventType.Status,
		}
	}
	sourceRules := make([]sourceinbox.Rule, len(rules))
	for index, rule := range rules {
		channels := make([]sourceinbox.RuleChannel, len(rule.Channels))
		for channelIndex, channel := range rule.Channels {
			channels[channelIndex] = sourceinbox.RuleChannel{
				Channel: channel.Channel, TemplateKey: channel.TemplateKey, ConnectorKey: channel.ConnectorKey, ConnectionKey: channel.ConnectionKey,
				Operation: channel.Operation, Mandatory: channel.Mandatory, DelaySeconds: channel.DelaySeconds, EscalationStep: channel.EscalationStep,
				CancelWhenActionTerminal: channel.CancelWhenActionTerminal, DeliveryMode: channel.DeliveryMode,
				DigestWindowSeconds: channel.DigestWindowSeconds, DigestMaximumItems: channel.DigestMaximumItems,
			}
		}
		sourceRules[index] = sourceinbox.Rule{
			EventTypeKey: rule.EventTypeKey, Enabled: rule.Enabled, AudienceResolvers: append([]string(nil), rule.AudienceResolvers...), MandatoryInApp: rule.MandatoryInApp,
			MinimumSeverity: rule.MinimumSeverity, DedupeWindowSeconds: rule.DedupeWindowSeconds, AggregationWindowSeconds: rule.AggregationWindowSeconds,
			ReminderIntervalSeconds: rule.ReminderIntervalSeconds, MaximumReminders: rule.MaximumReminders, RecoveryEventTypeKey: rule.RecoveryEventTypeKey,
			AutoResolveOnRecovery: rule.AutoResolveOnRecovery, UserMutable: rule.UserMutable, Channels: channels,
		}
	}
	return sourceinbox.NewCatalog(validator, sourceEventTypes, sourceRules)
}

func notificationModuleChannels(rules []notificationmodel.NotificationRule) []string {
	seen := map[string]bool{}
	channels := []string{}
	for _, rule := range rules {
		for _, channel := range rule.Channels {
			key := strings.TrimSpace(channel.Channel)
			if key == "" || key == "in_app" || seen[key] {
				continue
			}
			seen[key] = true
			channels = append(channels, key)
		}
	}
	sort.Strings(channels)
	return channels
}
