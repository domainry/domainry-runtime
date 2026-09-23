package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type runtimeNotificationActionAuthorizerBinding struct {
	authorize func(context.Context, string, principalmodel.Principal) error
}

type runtimeNotificationResolvedActionAuthorizerBinding struct {
	authorize func(context.Context, notificationmodel.NotificationInboxResolvedAction, principalmodel.Principal) error
}

func (b *runtimeNotificationResolvedActionAuthorizerBinding) Authorize(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
	if b == nil || b.authorize == nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
	}
	return b.authorize(ctx, action, principal)
}

func (b *runtimeNotificationActionAuthorizerBinding) Authorize(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
	if b == nil || b.authorize == nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
	}
	return b.authorize(ctx, resourceID, principal)
}

func newProjectRecordNotificationActionAuthorizer(getRecord func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)) notificationfacade.InboxResolvedResourceAuthorizer {
	return func(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
		objectKey, recordID := strings.TrimSpace(action.RouteParams["object_key"]), strings.TrimSpace(action.RouteParams["resource_id"])
		if getRecord == nil || objectKey == "" || recordID == "" {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
		}
		_, err := getRecord(ctx, objectKey, recordID, principal)
		return err
	}
}

func newRecordExportNotificationActionAuthorizer(binding dataexchange.Binding) notificationfacade.InboxResolvedResourceAuthorizer {
	return func(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
		jobID := strings.TrimSpace(action.RouteParams["resource_id"])
		if binding == nil || jobID == "" {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
		}
		if !principal.Known || strings.TrimSpace(principal.WorkspaceID) == "" || strings.TrimSpace(principal.UserID) == "" {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		job, err := binding.Job(ctx, dataexchange.JobRequest{Scope: recordDataExchangeNotificationScope(principal), JobID: jobID})
		if err != nil {
			if errors.Is(err, dataexchange.ErrJobNotFound) || apperror.KindOf(err) == apperror.KindNotFound {
				return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found", Err: err}
			}
			return err
		}
		if job.Provider != "records" || job.Operation != "export" || job.WorkspaceID != principal.WorkspaceID || job.ActorID != principal.UserID {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		if job.Status != "completed" || strings.TrimSpace(job.ArtifactID) == "" {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
		}
		return nil
	}
}

func recordDataExchangeNotificationScope(principal principalmodel.Principal) dataexchange.Scope {
	return dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey, RequestID: principal.RequestID}
}

type workflowTaskLookup func(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error)
type reportCatalogLookup func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema
type automationRuleLookup func(context.Context, string, principalmodel.Principal) (automationmodel.AutomationRuleSchema, error)
type notificationIntentPublisher func(context.Context, notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error)
type notificationRecipientLookup func(context.Context, string) (identitysdk.User, bool, error)
type runtimeNotificationCompiler interface {
	CompileInboxIntent(notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, error)
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

func notificationEventPublisherCallback(publisher notificationIntentPublisher) func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error) {
	if publisher == nil {
		return nil
	}
	return func(ctx context.Context, intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error) {
		return publisher(ctx, intent, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "publish scheduled notification"))
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

func newSchedulerNotificationActionAuthorizer(definitions metadatasdk.Definitions) func(context.Context, string, principalmodel.Principal) error {
	return func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		allowed := principal.HasPermission(schedulersdk.ActionSchedulerDefinitionsGet)
		if !allowed {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		if definitions == nil {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
		}
		_, found, err := definitions.Get(ctx, metadatasdk.DefinitionOwnerScheduler, "scheduler", resourceID)
		if err != nil {
			return err
		}
		if !found {
			return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
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
