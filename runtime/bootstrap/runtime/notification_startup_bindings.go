package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
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

type workflowTaskLookup func(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error)
type schedulerDefinitionLookup func(context.Context, principalmodel.SystemScope, string, string) (appschemamodel.ApplicationDefinition, bool, error)
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
		if manifest.GeneratedDomainSDK.ApplicationSchemaSnapshotSHA256 != "" {
			metadataRevision = manifest.GeneratedDomainSDK.ApplicationSchemaSnapshotSHA256
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
