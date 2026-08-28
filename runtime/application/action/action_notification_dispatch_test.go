package action

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func testNotificationIntent() runtimeext.NotificationIntent {
	value := "PT COMEX order"
	return runtimeext.NotificationIntent{
		EventType: "business_task.assigned", SourceEventID: "s10:order-42:v7:assigned:user-2", RecipientUserIDs: []string{"user-2"},
		Surface: "business_workspace", SubjectObjectKey: "business_task", SubjectRecordID: "task-42", SubjectVersion: "order-version-7",
		DedupeKey: "s10:order-42:v7:assigned:user-2", OccurredAt: time.Date(2026, 8, 9, 8, 0, 0, 0, time.UTC),
		Variables: []runtimeext.NotificationVariable{{Key: "title", StringValue: &value}},
	}
}

func TestBusinessActionStagesGrantedNotificationAtomicallyWithAudit(t *testing.T) {
	compiled := 0
	execution := &businessActionExecution{
		unitOfWork: newActionTestUnitOfWork(), identity: runtimeext.ExecutionIdentity{ExecutionID: "execution-1"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
		principal: runtimeext.Principal{UserID: "user-1", RoleKey: "operator"}, invocation: actionTestInvocation(), notificationGrants: []string{"business_task.assigned"},
		dependencies: BusinessHandlerExecutionDependencies{CompileNotification: func(_ context.Context, eventID string, intent runtimeext.NotificationIntent, principal principalmodel.Principal) (notificationmodel.NotificationEvent, error) {
			compiled++
			if eventID == "" || principal.WorkspaceID != "workspace-a" || intent.Variables[0].Value() != "PT COMEX order" {
				t.Fatalf("intent=%+v principal=%+v", intent, principal)
			}
			return notificationmodel.NotificationEvent{Source: "project", Category: "task", Snapshot: notificationmodel.NotificationInboxSnapshot{Title: "Assigned", Actions: []notificationmodel.NotificationInboxActionRef{{ResourceType: "project_record", ResourceID: "task-42"}}}}, nil
		}},
		plans: []transactionmodel.MutationPlan{actionMutationEdgePlan(t, "create", "business_task", "task-42")},
	}
	receipt, err := execution.StageNotification(t.Context(), testNotificationIntent())
	if err != nil || receipt.ID == "" || compiled != 1 {
		t.Fatalf("receipt=%+v compiled=%d err=%v", receipt, compiled, err)
	}
	commits, err := execution.canonicalCommits()
	if err != nil || len(commits) != 1 || len(commits[0].NotificationEvents) != 1 || len(commits[0].Audits) != 1 {
		t.Fatalf("commits=%+v err=%v", commits, err)
	}
	event := commits[0].NotificationEvents[0]
	if event.WorkspaceID != "workspace-a" || event.SourceEventID != "s10:order-42:v7:assigned:user-2" || event.SubjectVersion != "order-version-7" || event.DedupeKey == "" {
		t.Fatalf("event=%+v", event)
	}
	if commits[0].Audits[0].Event != runtimeext.NotificationDispatchOperationKey || commits[0].Audits[0].Metadata["recipient_count"] != 1 {
		t.Fatalf("audit=%+v", commits[0].Audits[0])
	}
}

func TestBusinessActionNotificationFailsClosedBeforeCommit(t *testing.T) {
	valid := testNotificationIntent()
	execution := &businessActionExecution{unitOfWork: newActionTestUnitOfWork(), identity: runtimeext.ExecutionIdentity{ExecutionID: "execution"}, invocation: actionTestInvocation(), notificationGrants: []string{"other.event"}}
	if _, err := execution.StageNotification(t.Context(), runtimeext.NotificationIntent{}); apperror.CodeOf(err) != "backend.notification.action_intent_invalid" {
		t.Fatalf("invalid err=%v", err)
	}
	if _, err := execution.StageNotification(t.Context(), valid); apperror.CodeOf(err) != runtimeext.NotificationActionGrantDeniedErrorCode {
		t.Fatalf("denied err=%v", err)
	}
	execution.notificationGrants = []string{valid.EventType}
	if _, err := execution.StageNotification(t.Context(), valid); apperror.CodeOf(err) != "backend.notification.action_compiler_required" {
		t.Fatalf("compiler err=%v", err)
	}
	execution.dependencies.CompileNotification = func(context.Context, string, runtimeext.NotificationIntent, principalmodel.Principal) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, nil
	}
	if _, err := execution.StageNotification(t.Context(), valid); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.canonicalCommits(); apperror.CodeOf(err) != "backend.notification.action_requires_business_mutation" {
		t.Fatalf("atomicity err=%v", err)
	}
}

func TestBusinessActionStagesBoundedNotificationBatchInOrderWithoutPartialFailure(t *testing.T) {
	compiled := 0
	execution := &businessActionExecution{
		unitOfWork: newActionTestUnitOfWork(), identity: runtimeext.ExecutionIdentity{ExecutionID: "execution-batch"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
		principal: runtimeext.Principal{UserID: "user-1", RoleKey: "operator"}, invocation: actionTestInvocation(), notificationGrants: []string{"business_task.assigned"},
		dependencies: BusinessHandlerExecutionDependencies{CompileNotification: func(_ context.Context, eventID string, intent runtimeext.NotificationIntent, principal principalmodel.Principal) (notificationmodel.NotificationEvent, error) {
			compiled++
			if principal.WorkspaceID != "workspace-a" || eventID == "" {
				t.Fatalf("principal=%+v event=%q", principal, eventID)
			}
			return notificationmodel.NotificationEvent{Snapshot: notificationmodel.NotificationInboxSnapshot{Title: intent.SubjectRecordID}}, nil
		}},
	}
	first, second := testNotificationIntent(), testNotificationIntent()
	first.SourceEventID, first.SubjectRecordID, first.DedupeKey = "task-1:assigned", "task-1", "task-1:assigned:user-2"
	second.SourceEventID, second.SubjectRecordID, second.DedupeKey = "task-2:assigned", "task-2", "task-2:assigned:user-2"
	receipts, err := execution.StageNotificationBatch(t.Context(), []runtimeext.NotificationIntent{first, second})
	if err != nil || len(receipts) != 2 || receipts[0].ID == receipts[1].ID || compiled != 2 || len(execution.notifications) != 2 || execution.notifications[0].SubjectID != "task-1" || execution.notifications[1].SubjectID != "task-2" {
		t.Fatalf("receipts=%+v notifications=%+v compiled=%d err=%v", receipts, execution.notifications, compiled, err)
	}

	before := len(execution.notifications)
	duplicate := second
	if _, err := execution.StageNotificationBatch(t.Context(), []runtimeext.NotificationIntent{second, duplicate}); apperror.CodeOf(err) != "backend.notification.action_batch_source_duplicate" {
		t.Fatalf("duplicate error=%v", err)
	}
	if len(execution.notifications) != before || compiled != 2 {
		t.Fatalf("duplicate batch partially staged: notifications=%d compiled=%d", len(execution.notifications), compiled)
	}

	failing := *execution
	failing.notifications = nil
	failing.dependencies.CompileNotification = func(_ context.Context, _ string, intent runtimeext.NotificationIntent, _ principalmodel.Principal) (notificationmodel.NotificationEvent, error) {
		if intent.SubjectRecordID == "task-2" {
			return notificationmodel.NotificationEvent{}, apperror.New(apperror.KindBadRequest, "test.compile_failed", nil, nil)
		}
		return notificationmodel.NotificationEvent{}, nil
	}
	if _, err := failing.StageNotificationBatch(t.Context(), []runtimeext.NotificationIntent{first, second}); apperror.CodeOf(err) != "test.compile_failed" {
		t.Fatalf("compile failure=%v", err)
	}
	if len(failing.notifications) != 0 {
		t.Fatalf("compile failure partially staged %+v", failing.notifications)
	}
}

func TestBusinessActionNotificationBatchRejectsSizeBeforeRuntimeWork(t *testing.T) {
	execution := &businessActionExecution{}
	for _, intents := range [][]runtimeext.NotificationIntent{nil, make([]runtimeext.NotificationIntent, runtimeext.NotificationBatchMaximum+1)} {
		if _, err := execution.StageNotificationBatch(t.Context(), intents); apperror.CodeOf(err) != runtimeext.NotificationBatchSizeInvalidErrorCode {
			t.Fatalf("size=%d error=%v", len(intents), err)
		}
	}
}

func TestConditionalMutationResultUsesCanonicalCommitVersionForNotification(t *testing.T) {
	const revision = "2026-08-22T10:11:12.123456789Z"
	mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
		WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction,
		ActionKey: "business_task.assign", CorrelationID: "request-1", MetadataRevision: "metadata-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical := recordmodel.Record{ID: "task-42", UpdatedAt: revision, Data: map[string]any{"status": "assigned"}}
	plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
		Operation: "update", Object: definitionmodel.ObjectSchema{Key: "business_task"}, Record: canonical,
	}, map[string]any{"status": "open"})
	if err != nil {
		t.Fatal(err)
	}
	execution := &businessActionExecution{
		unitOfWork: newActionTestUnitOfWork(), identity: runtimeext.ExecutionIdentity{ExecutionID: "request-1"},
		workspace: runtimeext.Workspace{ID: "workspace-a"}, principal: runtimeext.Principal{UserID: "user-1"},
		invocation: actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "user-1"}}},
		action: definitionmodel.ActionSchema{Key: "business_task.assign", EffectSet: &definitionmodel.ActionEffectSet{
			Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "business_task"}},
		}},
		notificationGrants: []string{"business_task.assigned"},
		dependencies: BusinessHandlerExecutionDependencies{
			PlanConditionalUpdate: func(context.Context, string, string, transactionmodel.ConditionalUpdateInput, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				// Reproduce the live boundary defect: the convenience result omits
				// UpdatedAt even though the canonical commit owns the final revision.
				return plan, recordmodel.Record{ID: canonical.ID, Data: canonical.Data}, nil
			},
			CompileNotification: func(_ context.Context, _ string, intent runtimeext.NotificationIntent, _ principalmodel.Principal) (notificationmodel.NotificationEvent, error) {
				if intent.SubjectVersion != revision {
					t.Fatalf("notification subject version=%q", intent.SubjectVersion)
				}
				return notificationmodel.NotificationEvent{}, nil
			},
		},
	}
	result, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{
		Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "business_task", RecordID: canonical.ID,
		Fields: map[string]any{"status": "assigned"}, Predicates: []runtimeext.Predicate{{Field: "status", Operator: "eq", Value: "open"}},
	})
	if err != nil || result.Record.UpdatedAt != revision {
		t.Fatalf("mutation result=%+v err=%v", result, err)
	}
	intent := testNotificationIntent()
	intent.SubjectVersion = result.Record.UpdatedAt
	if _, err := execution.StageNotification(t.Context(), intent); err != nil {
		t.Fatalf("stage notification from mutation result: %v", err)
	}
	commits, err := execution.canonicalCommits()
	if err != nil || len(commits) != 1 || commits[0].Record.UpdatedAt != revision || len(commits[0].NotificationEvents) != 1 || commits[0].NotificationEvents[0].SubjectVersion != revision {
		t.Fatalf("canonical commits=%+v err=%v", commits, err)
	}
}
