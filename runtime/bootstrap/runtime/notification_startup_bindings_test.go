package runtime

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestNotificationStartupRevisionAndPublisherBoundaries(t *testing.T) {
	if err := requireRuntimeSchemaRevision(" "); err == nil {
		t.Fatal("blank schema revision was accepted")
	}
	if err := requireRuntimeSchemaRevision("revision"); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{ManifestHash: "manifest"}
	if project, metadata := runtimeActionRevisions(manifest); project != "manifest" || metadata != "manifest" {
		t.Fatalf("fallback revisions=%q,%q", project, metadata)
	}
	manifest.GeneratedDomainSDK = &manifestmodel.GeneratedDomainSDKIdentity{}
	if project, metadata := runtimeActionRevisions(manifest); project != "manifest" || metadata != "manifest" {
		t.Fatalf("empty SDK revisions=%q,%q", project, metadata)
	}
	manifest.GeneratedDomainSDK.ArtifactSHA256 = "artifact"
	manifest.GeneratedDomainSDK.ApplicationSchemaSnapshotSHA256 = "metadata"
	if project, metadata := runtimeActionRevisions(manifest); project != "artifact" || metadata != "metadata" {
		t.Fatalf("SDK revisions=%q,%q", project, metadata)
	}
	if err := notificationIntentPublisherCallback(nil)(t.Context(), notificationmodel.NotificationIntent{}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("publish failed")
	callback := notificationIntentPublisherCallback(func(_ context.Context, _ notificationmodel.NotificationIntent, scope principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
		if scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
			t.Fatalf("scope=%+v", scope)
		}
		return notificationmodel.NotificationEvent{}, false, failure
	})
	if err := callback(t.Context(), notificationmodel.NotificationIntent{}); !errors.Is(err, failure) {
		t.Fatalf("publisher error=%v", err)
	}
}

func TestNotificationRecipientLocaleResolverBoundaries(t *testing.T) {
	failure := errors.New("recipient lookup failed")
	for _, test := range []struct {
		name   string
		user   identitysdk.User
		found  bool
		err    error
		locale string
	}{
		{name: "lookup error", err: failure},
		{name: "missing"},
		{name: "found", found: true, user: identitysdk.User{Locale: "zh-CN"}, locale: "zh-CN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := newNotificationRecipientLocaleResolver(func(ctx context.Context, id string) (identitysdk.User, bool, error) {
				if requestcontext.WorkspaceID(ctx) != "workspace" || id != "user" {
					t.Fatalf("lookup context workspace=%q id=%q", requestcontext.WorkspaceID(ctx), id)
				}
				return test.user, test.found, test.err
			})
			locale, err := resolver(t.Context(), "workspace", "user")
			if !errors.Is(err, test.err) || locale != test.locale {
				t.Fatalf("locale=%q error=%v", locale, err)
			}
		})
	}
}

func TestWorkflowTaskNotificationBindingsBoundaryMatrix(t *testing.T) {
	failure := errors.New("task lookup failed")
	event := notificationcontract.NotificationEvent{WorkspaceID: "workspace", SubjectID: "task"}
	for _, test := range []struct {
		name     string
		task     workflowmodel.WorkflowTask
		found    bool
		err      error
		wantUser string
		wantCode string
	}{
		{name: "lookup error", err: failure},
		{name: "missing", wantCode: "backend.notification.inbox_audience_resolution_empty"},
		{name: "blank assignee", found: true, task: workflowmodel.WorkflowTask{AssigneeUserID: " "}, wantCode: "backend.notification.inbox_audience_resolution_empty"},
		{name: "assignee", found: true, task: workflowmodel.WorkflowTask{AssigneeUserID: "user"}, wantUser: "user"},
	} {
		t.Run("audience "+test.name, func(t *testing.T) {
			resolver := notificationSDKWorkflowAudience{lookup: func(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error) {
				return test.task, test.found, test.err
			}}
			users, err := resolver.ResolveAudience(t.Context(), "workflow_task_assignee", event)
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("error=%v", err)
			}
			if test.wantCode != "" && apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("code=%q error=%v", apperror.CodeOf(err), err)
			}
			if test.wantUser != "" && (err != nil || len(users) != 1 || users[0] != test.wantUser) {
				t.Fatalf("users=%v error=%v", users, err)
			}
		})
	}

	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", UserID: "user"}}
	for _, test := range []struct {
		name     string
		task     workflowmodel.WorkflowTask
		found    bool
		err      error
		wantCode string
	}{
		{name: "lookup error", err: failure},
		{name: "missing", wantCode: "backend.notification.inbox_action_resource_not_found"},
		{name: "other assignee", found: true, task: workflowmodel.WorkflowTask{AssigneeUserID: "other", Status: "open"}, wantCode: "backend.notification.inbox_action_forbidden"},
		{name: "closed", found: true, task: workflowmodel.WorkflowTask{AssigneeUserID: "user", Status: "completed"}, wantCode: "backend.notification.inbox_action_unavailable"},
		{name: "open", found: true, task: workflowmodel.WorkflowTask{AssigneeUserID: "user", Status: "open"}},
	} {
		t.Run("action "+test.name, func(t *testing.T) {
			authorize := newWorkflowTaskNotificationActionAuthorizer(func(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error) {
				return test.task, test.found, test.err
			})
			err := authorize(t.Context(), "task", principal)
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("error=%v", err)
			}
			if test.wantCode != "" && apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("code=%q error=%v", apperror.CodeOf(err), err)
			}
			if test.err == nil && test.wantCode == "" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSchedulerNotificationAuthorizerBoundaries(t *testing.T) {
	for _, test := range []struct {
		name        string
		permissions []string
		found       bool
		err         error
		wantCode    string
	}{
		{name: "unrelated permission", permissions: []string{"other"}, wantCode: "backend.notification.inbox_action_forbidden"},
		{name: "workspace admin is not scheduler read", permissions: []string{"workspace.admin"}, wantCode: "backend.notification.inbox_action_forbidden"},
		{name: "reader missing", permissions: []string{"scheduler.definition.read"}, wantCode: "backend.notification.inbox_action_resource_not_found"},
		{name: "reader found after unrelated", permissions: []string{"other", "scheduler.definition.read"}, found: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorize := newSchedulerNotificationActionAuthorizer(schedulerNotificationDefinitions{found: test.found, err: test.err})
			err := authorize(t.Context(), "job", accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: test.permissions}))
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("error=%v", err)
			}
			if test.wantCode != "" && apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("code=%q error=%v", apperror.CodeOf(err), err)
			}
			if test.err == nil && test.wantCode == "" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

type schedulerNotificationDefinitions struct {
	found bool
	err   error
}

func (s schedulerNotificationDefinitions) List(context.Context, metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	return nil, s.err
}

func (s schedulerNotificationDefinitions) Get(context.Context, string, string) (metadatasdk.Definition, bool, error) {
	return metadatasdk.Definition{}, s.found, s.err
}

func (s schedulerNotificationDefinitions) Snapshot(context.Context) (metadatasdk.DefinitionSnapshot, error) {
	return metadatasdk.DefinitionSnapshot{}, s.err
}

func TestReportAndAutomationNotificationAuthorizerBoundaries(t *testing.T) {
	principal := principalmodel.Principal{}
	reportAuthorize := newReportNotificationActionAuthorizer(func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{{Key: "first"}, {Key: "target"}}
	})
	if err := reportAuthorize(t.Context(), " target ", principal); err != nil {
		t.Fatal(err)
	}
	if code := apperror.CodeOf(reportAuthorize(t.Context(), "missing", principal)); code != "backend.notification.inbox_action_resource_not_found" {
		t.Fatalf("missing report code=%q", code)
	}

	notFound := &apperror.AppError{Kind: apperror.KindNotFound, Code: "automation.missing"}
	for _, test := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "found"},
		{name: "not found", err: notFound, wantCode: "backend.notification.inbox_action_resource_not_found"},
		{name: "other error", err: errors.New("automation failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorize := newAutomationNotificationActionAuthorizer(func(_ context.Context, key string, _ principalmodel.Principal) (automationmodel.AutomationRuleSchema, error) {
				if key != "rule" {
					t.Fatalf("trimmed key=%q", key)
				}
				return automationmodel.AutomationRuleSchema{}, test.err
			})
			err := authorize(t.Context(), " rule ", principal)
			if test.wantCode != "" && apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("code=%q error=%v", apperror.CodeOf(err), err)
			}
			if test.wantCode == "" && !errors.Is(err, test.err) {
				t.Fatalf("error=%v want=%v", err, test.err)
			}
		})
	}
}

func TestNotificationModuleChannelsAreDerivedFromRules(t *testing.T) {
	channels := notificationModuleChannels([]notificationmodel.NotificationRule{
		{Channels: []notificationmodel.NotificationRuleChannel{{Channel: "in_app"}, {Channel: " slack "}, {Channel: "email"}}},
		{Channels: []notificationmodel.NotificationRuleChannel{{Channel: "slack"}, {Channel: ""}}},
	})
	if len(channels) != 2 || channels[0] != "email" || channels[1] != "slack" {
		t.Fatalf("channels=%v", channels)
	}
}
