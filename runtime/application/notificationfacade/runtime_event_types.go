package notificationfacade

import (
	"fmt"
	"strings"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

type NotificationLocalizationLookup func(locale, key string) (string, bool)

// NotificationRuntimeEventTypes returns framework-owned event types plus any
// project-published overrides. Built-in copy is resolved from Runtime's shared
// locale catalog; Go owns only stable keys, variables, actions and routing.
func NotificationRuntimeEventTypes(project []notificationmodel.NotificationEventType, locales []string, defaultLocale string, lookup NotificationLocalizationLookup) ([]notificationmodel.NotificationEventType, error) {
	builtIns, err := notificationBuiltInEventTypes(locales, defaultLocale, lookup)
	if err != nil {
		return nil, err
	}
	byKey := map[string]notificationmodel.NotificationEventType{}
	order := []string{}
	for _, value := range builtIns {
		key := strings.TrimSpace(value.Key)
		byKey[key], order = value, append(order, key)
	}
	for _, value := range project {
		key := strings.TrimSpace(value.Key)
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
		}
		byKey[key] = value
	}
	result := make([]notificationmodel.NotificationEventType, 0, len(order))
	for _, key := range order {
		result = append(result, byKey[key])
	}
	return result, nil
}

func notificationBuiltInEventTypes(locales []string, defaultLocale string, lookup NotificationLocalizationLookup) ([]notificationmodel.NotificationEventType, error) {
	types := []notificationmodel.NotificationEventType{
		workflowTaskActionEventType("workflow.task.assigned", "workflow.task.assigned.in_app", "info", true),
		workflowTaskActionEventType("workflow.task.reminded", "workflow.task.reminded.in_app", "warning", false),
		workflowTaskTerminalEventType("workflow.task.completed", "workflow.task.completed.in_app"),
		workflowTaskTerminalEventType("workflow.task.cancelled", "workflow.task.cancelled.in_app"),
		schedulerEventType("scheduler.job.failed", "scheduler.job.failed.in_app", "warning", true),
		schedulerEventType("scheduler.job.repeated_failure", "scheduler.job.repeated_failure.in_app", "warning", true),
		schedulerEventType("scheduler.job.missed_deadline", "scheduler.job.missed_deadline.in_app", "warning", true),
		schedulerEventType("scheduler.job.recovered", "scheduler.job.recovered.in_app", "info", false),
		integrationCredentialEventType("integration.credential.expiring", "integration.credential.expiring.in_app", "warning", "integration_secret", "integration.secret.open"),
		integrationCredentialEventType("integration.credential.expired", "integration.credential.expired.in_app", "critical", "integration_secret", "integration.secret.open"),
		integrationCredentialEventType("integration.credential.refresh_failed", "integration.credential.refresh_failed.in_app", "critical", "integration_connection", "integration.connection.open"),
		integrationCredentialEventType("integration.credential.recovered", "integration.credential.recovered.in_app", "info", "", ""),
		integrationResourceHealthEventType("integration.quota.warning", "integration.quota.warning.in_app", "warning", true),
		integrationResourceHealthEventType("integration.quota.exhausted", "integration.quota.exhausted.in_app", "critical", true),
		integrationResourceHealthEventType("integration.quota.recovered", "integration.quota.recovered.in_app", "info", false),
		integrationResourceHealthEventType("integration.billing.payment_required", "integration.billing.payment_required.in_app", "critical", true),
		integrationResourceHealthEventType("integration.billing.recovered", "integration.billing.recovered.in_app", "info", false),
		recordBatchEventType("record.batch.completed", "record.batch.completed.in_app", "info"),
		recordBatchEventType("record.batch.failed", "record.batch.failed.in_app", "critical"),
		recordBatchEventType("record.batch.cancelled", "record.batch.cancelled.in_app", "warning"),
		reportSnapshotEventType("report.snapshot.completed", "report.snapshot.completed.in_app", "info"),
		reportSnapshotEventType("report.snapshot.failed", "report.snapshot.failed.in_app", "critical"),
		automationExecutionEventType("automation.execution.completed", "automation.execution.completed.in_app", "info"),
		automationExecutionEventType("automation.execution.failed", "automation.execution.failed.in_app", "critical"),
	}
	for index := range types {
		value := &types[index]
		value.DefaultLocale, value.Version, value.Status = defaultLocale, 1, "published"
		factKeys, actionKeys := notificationBuiltInPresentationKeys(value.Key)
		contents, err := notificationLocalizedContents(locales, lookup, notificationBuiltInPrefix(value.Key), factKeys, actionKeys)
		if err != nil {
			return nil, err
		}
		value.Locales = contents
	}
	return types, nil
}

func automationExecutionEventType(key, templateKey, severity string) notificationmodel.NotificationEventType {
	return notificationmodel.NotificationEventType{
		Key: key, Source: "automation", Category: "automation", DefaultSeverity: severity, Surfaces: []string{"business_workspace"}, MandatoryInApp: false,
		TemplateKey: templateKey, Variables: []notificationmodel.NotificationTemplateVariable{
			{Key: "rule_key", Type: "string", Required: true}, {Key: "object_key", Type: "string", Required: true}, {Key: "execution_id", Type: "string", Required: true},
			{Key: "status", Type: "string", Required: true}, {Key: "error_code", Type: "string"},
		},
		Actions: []notificationmodel.NotificationInboxActionDescriptor{{Key: "automation.rule.open", Kind: "route", ResourceType: "automation_rule", SurfaceRoutes: map[string]string{"business_workspace": "automation.rule.detail"}}},
	}
}

func reportSnapshotEventType(key, templateKey, severity string) notificationmodel.NotificationEventType {
	return notificationmodel.NotificationEventType{
		Key: key, Source: "report", Category: "long_task", DefaultSeverity: severity, Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: templateKey, Variables: []notificationmodel.NotificationTemplateVariable{
			{Key: "report_key", Type: "string", Required: true}, {Key: "status", Type: "string", Required: true}, {Key: "error_code", Type: "string"},
		},
		Actions: []notificationmodel.NotificationInboxActionDescriptor{{Key: "report.open", Kind: "route", ResourceType: "report", SurfaceRoutes: map[string]string{"business_workspace": "report.detail"}}},
	}
}

func recordBatchEventType(key, templateKey, severity string) notificationmodel.NotificationEventType {
	return notificationmodel.NotificationEventType{
		Key: key, Source: "record", Category: "long_task", DefaultSeverity: severity, Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: templateKey, Variables: []notificationmodel.NotificationTemplateVariable{
			{Key: "job_kind", Type: "string", Required: true}, {Key: "object_key", Type: "string", Required: true},
			{Key: "status", Type: "string", Required: true}, {Key: "total", Type: "number"}, {Key: "error_code", Type: "string"},
			{Key: "job_id", Type: "string"}, {Key: "audit_id", Type: "string"}, {Key: "artifact_id", Type: "string"},
		},
		Actions: []notificationmodel.NotificationInboxActionDescriptor{{Key: "record.batch.open", Kind: "route", ResourceType: "record_batch_job", SurfaceRoutes: map[string]string{"business_workspace": "record.batch.job.detail"}}},
	}
}

func workflowTaskActionEventType(key, templateKey, severity string, dueFact bool) notificationmodel.NotificationEventType {
	variables := workflowTaskLifecycleVariables()
	if dueFact {
		variables = append(variables, notificationmodel.NotificationTemplateVariable{Key: "due_at", Type: "string"})
	}
	return notificationmodel.NotificationEventType{
		Key: key, Source: "workflow", Category: "approval", DefaultSeverity: severity, Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: templateKey, Variables: variables, AudienceResolvers: []string{"workflow_task_assignee"},
		Actions: []notificationmodel.NotificationInboxActionDescriptor{{Key: "workflow.task.open", Kind: "route", ResourceType: "workflow_task", SurfaceRoutes: map[string]string{"business_workspace": "workflow.task.detail"}}},
	}
}

func workflowTaskTerminalEventType(key, templateKey string) notificationmodel.NotificationEventType {
	return notificationmodel.NotificationEventType{Key: key, Source: "workflow", Category: "approval", DefaultSeverity: "info", Surfaces: []string{"business_workspace"}, MandatoryInApp: true, TemplateKey: templateKey, Variables: workflowTaskLifecycleVariables(), AudienceResolvers: []string{"workflow_task_assignee"}}
}

func workflowTaskLifecycleVariables() []notificationmodel.NotificationTemplateVariable {
	return []notificationmodel.NotificationTemplateVariable{{Key: "task_title", Type: "string", Required: true}, {Key: "workflow_name", Type: "string"}, {Key: "decision", Type: "string"}}
}

func schedulerEventType(key, templateKey, severity string, actionable bool) notificationmodel.NotificationEventType {
	value := notificationmodel.NotificationEventType{
		Key: key, Source: "scheduler", Category: "scheduler", DefaultSeverity: severity, Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: templateKey, Variables: []notificationmodel.NotificationTemplateVariable{{Key: "job_name", Type: "string", Required: true}, {Key: "scheduled_for", Type: "string"}, {Key: "status", Type: "string"}, {Key: "error_code", Type: "string"}, {Key: "occurrence_count", Type: "number"}},
	}
	if actionable {
		value.Actions = []notificationmodel.NotificationInboxActionDescriptor{{Key: "scheduler.job.open", Kind: "route", ResourceType: "scheduler_job", SurfaceRoutes: map[string]string{"business_workspace": "scheduler.job.detail"}}}
	}
	return value
}

func integrationCredentialEventType(key, templateKey, severity, resourceType, actionKey string) notificationmodel.NotificationEventType {
	value := notificationmodel.NotificationEventType{
		Key: key, Source: "integration", Category: "integration", DefaultSeverity: severity, Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: templateKey, Variables: []notificationmodel.NotificationTemplateVariable{
			{Key: "credential_name", Type: "string", Required: true}, {Key: "connection_name", Type: "string"},
			{Key: "expires_at", Type: "string"}, {Key: "days_remaining", Type: "number"}, {Key: "error_code", Type: "string"},
		},
	}
	if resourceType != "" && actionKey != "" {
		routeKey := "integration.secret.detail"
		if resourceType == "integration_connection" {
			routeKey = "integration.connection.detail"
		}
		value.Actions = []notificationmodel.NotificationInboxActionDescriptor{{Key: actionKey, Kind: "route", ResourceType: resourceType, SurfaceRoutes: map[string]string{"business_workspace": routeKey}}}
	}
	return value
}

func integrationResourceHealthEventType(key, templateKey, severity string, actionable bool) notificationmodel.NotificationEventType {
	value := notificationmodel.NotificationEventType{
		Key: key, Source: "integration", Category: "integration", DefaultSeverity: severity, Surfaces: []string{"business_workspace"}, MandatoryInApp: true,
		TemplateKey: templateKey, Variables: []notificationmodel.NotificationTemplateVariable{
			{Key: "connection_name", Type: "string", Required: true}, {Key: "provider_key", Type: "string"},
			{Key: "quota_used_percent", Type: "number"}, {Key: "balance_band", Type: "string"},
			{Key: "capability_blocked", Type: "boolean"}, {Key: "observed_at", Type: "string"}, {Key: "error_code", Type: "string"},
		},
	}
	if actionable {
		value.Actions = []notificationmodel.NotificationInboxActionDescriptor{{Key: "integration.connection.open", Kind: "route", ResourceType: "integration_connection", SurfaceRoutes: map[string]string{"business_workspace": "integration.connection.detail"}}}
	}
	return value
}

func notificationBuiltInPrefix(eventType string) string {
	value := strings.ReplaceAll(eventType, "repeated_failure", "repeatedFailure")
	value = strings.ReplaceAll(value, "missed_deadline", "missedDeadline")
	value = strings.ReplaceAll(value, "refresh_failed", "refreshFailed")
	value = strings.ReplaceAll(value, "payment_required", "paymentRequired")
	return "notification." + value
}

func notificationBuiltInPresentationKeys(eventType string) ([]string, []string) {
	switch eventType {
	case "workflow.task.assigned":
		return []string{"due"}, []string{"workflow.task.open"}
	case "workflow.task.reminded":
		return nil, []string{"workflow.task.open"}
	case "scheduler.job.failed", "scheduler.job.repeated_failure", "scheduler.job.missed_deadline":
		return []string{"scheduled", "errorCode"}, []string{"scheduler.job.open"}
	case "integration.credential.expiring", "integration.credential.expired":
		return []string{"expiresAt", "daysRemaining"}, []string{"integration.secret.open"}
	case "integration.credential.refresh_failed":
		return []string{"errorCode"}, []string{"integration.connection.open"}
	case "integration.quota.warning", "integration.quota.exhausted":
		return []string{"quotaUsed", "observedAt"}, []string{"integration.connection.open"}
	case "integration.billing.payment_required":
		return []string{"balanceBand", "observedAt"}, []string{"integration.connection.open"}
	case "record.batch.completed", "record.batch.failed", "record.batch.cancelled":
		return nil, []string{"record.batch.open"}
	case "report.snapshot.completed", "report.snapshot.failed":
		return nil, []string{"report.open"}
	case "automation.execution.completed", "automation.execution.failed":
		return nil, []string{"automation.rule.open"}
	default:
		return nil, nil
	}
}

func notificationLocalizedContents(locales []string, lookup NotificationLocalizationLookup, prefix string, factKeys, actionKeys []string) (map[string]notificationmodel.NotificationInboxEventTypeContent, error) {
	if lookup == nil || len(locales) == 0 {
		return nil, fmt.Errorf("notification localization catalog is required")
	}
	result := make(map[string]notificationmodel.NotificationInboxEventTypeContent, len(locales))
	for _, locale := range locales {
		title, titleOK := lookup(locale, prefix+".title")
		body, bodyOK := lookup(locale, prefix+".body")
		if !titleOK || !bodyOK {
			return nil, fmt.Errorf("notification localization is incomplete for %s in %s", prefix, locale)
		}
		content := notificationmodel.NotificationInboxEventTypeContent{Title: title, Body: body, ActionLabels: map[string]string{}}
		for _, factKey := range factKeys {
			label, labelOK := lookup(locale, "notification.fact."+factKey+".label")
			value, valueOK := lookup(locale, "notification.fact."+factKey+".value")
			if !labelOK || !valueOK {
				return nil, fmt.Errorf("notification fact localization is incomplete for %s.%s in %s", prefix, factKey, locale)
			}
			content.Facts = append(content.Facts, notificationmodel.NotificationTemplateFact{Key: label, Value: value})
		}
		for _, actionKey := range actionKeys {
			label, ok := lookup(locale, "notification.action."+actionKey)
			if !ok {
				return nil, fmt.Errorf("notification action localization is incomplete for %s.%s in %s", prefix, actionKey, locale)
			}
			content.ActionLabels[actionKey] = label
		}
		if len(content.ActionLabels) == 0 {
			content.ActionLabels = nil
		}
		result[locale] = content
	}
	return result, nil
}
