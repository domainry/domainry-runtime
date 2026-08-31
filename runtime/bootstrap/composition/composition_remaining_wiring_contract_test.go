package composition

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	connectortest "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit/connectors"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	"github.com/domainry/domainry-runtime/runtime/platform/resilience"
	agentsdkfixture "github.com/domainry/domainry-runtime/testsupport/agentsdkfixture"
)

type compositionConnectorAdapterStub struct{}

type actionNotificationIdentityDirectory struct {
	found bool
	err   error
}

func (d actionNotificationIdentityDirectory) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{ID: "recipient"}, d.found, d.err
}

func (actionNotificationIdentityDirectory) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}

func (actionNotificationIdentityDirectory) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return nil, nil
}

func (actionNotificationIdentityDirectory) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}

func (actionNotificationIdentityDirectory) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func (actionNotificationIdentityDirectory) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return nil, nil
}

type workflowRunnerStub struct {
	result  workflowmodel.WorkflowRunResult
	err     error
	payload map[string]any
}

func (s *workflowRunnerStub) RunAgentWorkflow(_ context.Context, _ string, payload map[string]any, _ principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	s.payload = payload
	return s.result, s.err
}

type agentPrincipalDirectoryStub struct{ principal principalmodel.Principal }

func (s agentPrincipalDirectoryStub) Resolve(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	bundle := identitysdk.AccessBundle{}
	if s.principal.AccessBundle != nil {
		bundle = *s.principal.AccessBundle
	}
	principal := s.principal.Principal
	principal.AccessBundle = nil
	return identitysdk.PrincipalResolution{Principal: principal, AccessBundle: bundle}, nil
}

type agentTaskRunnerStub struct{}

func (agentTaskRunnerStub) Start(context.Context, agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{}, nil
}

func (agentTaskRunnerStub) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{}, nil
}

func (agentTaskRunnerStub) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{}, nil
}

type interactiveAgentRunnerStub struct{}

func (interactiveAgentRunnerStub) Run(context.Context, agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	return agentsdk.InteractiveResult{}, nil
}

func TestActionNotificationCompilerValidatesDependenciesRecipientsAndProjection(t *testing.T) {
	if _, err := compileActionNotification(nil)(t.Context(), "event", runtimeext.NotificationIntent{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.notification.action_compiler_required" {
		t.Fatalf("nil assembly err=%v", err)
	}
	if _, err := compileActionNotification(&runtimeAssembly{})(t.Context(), "event", runtimeext.NotificationIntent{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.notification.action_compiler_required" {
		t.Fatalf("nil compiler err=%v", err)
	}
	assembly := &runtimeAssembly{recordNotificationCompiler: func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, nil
	}}
	if _, err := compileActionNotification(assembly)(t.Context(), "event", runtimeext.NotificationIntent{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.notification.action_compiler_required" {
		t.Fatalf("nil directory err=%v", err)
	}
	wantErr := errors.New("directory failed")
	assembly.identityDirectory = actionNotificationIdentityDirectory{err: wantErr}
	intent := runtimeext.NotificationIntent{RecipientUserIDs: []string{" recipient "}}
	if _, err := compileActionNotification(assembly)(t.Context(), "event", intent, principalmodel.Principal{}); !errors.Is(err, wantErr) {
		t.Fatalf("directory err=%v", err)
	}
	assembly.identityDirectory = actionNotificationIdentityDirectory{}
	if _, err := compileActionNotification(assembly)(t.Context(), "event", intent, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.notification.recipient_invalid" {
		t.Fatalf("missing recipient err=%v", err)
	}
	assembly.identityDirectory = actionNotificationIdentityDirectory{found: true}
	value := "Ada"
	var compiled notificationmodel.NotificationIntent
	assembly.recordNotificationCompiler = func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		compiled = intent
		return notificationmodel.NotificationEvent{ID: intent.ID}, nil
	}
	intent = runtimeext.NotificationIntent{
		EventType: " order.ready ", SourceEventID: " source ", RecipientUserIDs: []string{" recipient "}, Surface: " business_workspace ",
		SubjectObjectKey: " order ", SubjectRecordID: " order-1 ", SubjectVersion: " v1 ", DedupeKey: " dedupe ", GroupKey: " group ", Alert: true,
		OccurredAt: time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC), Variables: []runtimeext.NotificationVariable{{Key: " name ", StringValue: &value}},
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	if event, err := compileActionNotification(assembly)(t.Context(), "event-1", intent, principal); err != nil || event.ID != "event-1" || compiled.AlertState != notificationmodel.NotificationAlertFiring || compiled.Variables["name"] != "Ada" {
		t.Fatalf("event=%+v compiled=%+v err=%v", event, compiled, err)
	}
	intent.Alert = false
	if _, err := compileActionNotification(assembly)(t.Context(), "event-2", intent, principal); err != nil || compiled.AlertState != "" {
		t.Fatalf("non-alert compiled=%+v err=%v", compiled, err)
	}
}

func TestInteractiveWorkflowStarterBranches(t *testing.T) {
	if _, err := (runtimeInteractiveWorkflowStarter{}).StartInteractiveAgentWorkflow(t.Context(), "workflow", nil, "run", "key", principalmodel.Principal{}); apperror.CodeOf(err) != "agent.interactive.workflow_handoff_unavailable" {
		t.Fatalf("err=%v", err)
	}
	wantErr := errors.New("workflow failed")
	runner := &workflowRunnerStub{err: wantErr}
	if _, err := startInteractiveAgentWorkflow(t.Context(), runner, "workflow", map[string]any{"name": "Ada"}, " run ", " key ", principalmodel.Principal{}); !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
	if runner.payload["name"] != "Ada" || runner.payload["agent_interactive_run_id"] != "run" || runner.payload["agent_handoff_idempotency_key"] != "key" {
		t.Fatalf("payload=%v", runner.payload)
	}
	runner = &workflowRunnerStub{}
	if _, err := startInteractiveAgentWorkflow(t.Context(), runner, "workflow", nil, "run", "key", principalmodel.Principal{}); apperror.CodeOf(err) != "agent.interactive.workflow_process_required" {
		t.Fatalf("err=%v", err)
	}
	runner.result.Execution.ProcessID = " process-1 "
	if processID, err := startInteractiveAgentWorkflow(t.Context(), runner, "workflow", nil, "run", "key", principalmodel.Principal{}); err != nil || processID != "process-1" {
		t.Fatalf("process=%q err=%v", processID, err)
	}
}

func TestAgentWorkflowDependencyAbsentAndRetryBranches(t *testing.T) {
	if allowed, err := (runtimeAgentRecordVisibility{}).CanReadAgentRecord(t.Context(), "customer", "record", principalmodel.Principal{}); allowed || err != nil {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
	if allowed, err := (runtimeAgentRecordVisibility{records: &runtimeAssembly{}}).CanReadAgentRecord(t.Context(), "customer", "record", principalmodel.Principal{}); allowed || err != nil {
		t.Fatalf("ownerless allowed=%v err=%v", allowed, err)
	}
	committer := runtimeAgentTaskTerminalCommitter{}
	if err := committer.CommitAgentTaskTerminal(t.Context(), agentmodel.AgentTaskRun{}, "owner", 1); apperror.CodeOf(err) != "agent.task.terminal_committer_unavailable" {
		t.Fatalf("err=%v", err)
	}
	if err := committer.CommitAgentTaskApprovalTerminal(t.Context(), agentmodel.AgentTaskRun{}); apperror.CodeOf(err) != "agent.task.workflow_unavailable" {
		t.Fatalf("err=%v", err)
	}
	committer.records = &runtimeAssembly{}
	if err := committer.CommitAgentTaskTerminal(t.Context(), agentmodel.AgentTaskRun{}, "owner", 1); apperror.CodeOf(err) != "agent.task.terminal_committer_unavailable" {
		t.Fatalf("ownerless err=%v", err)
	}
	if err := committer.CommitAgentTaskApprovalTerminal(t.Context(), agentmodel.AgentTaskRun{}); apperror.CodeOf(err) != "agent.task.workflow_unavailable" {
		t.Fatalf("ownerless err=%v", err)
	}
	dependencies := workflowDependencies(&runtimeAssembly{})
	if _, err := dependencies.PrepareAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{}); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("err=%v", err)
	}
	assembly := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	_, _ = (runtimeInteractiveWorkflowStarter{records: assembly}).StartInteractiveAgentWorkflow(t.Context(), "missing", nil, "run", "key", principalmodel.Principal{})
	_, _ = (runtimeAgentRecordVisibility{records: assembly}).CanReadAgentRecord(t.Context(), "missing", "record", principalmodel.Principal{})
	_ = (runtimeAgentTaskTerminalCommitter{records: assembly}).CommitAgentTaskTerminal(t.Context(), agentmodel.AgentTaskRun{}, "owner", 1)
	_ = (runtimeAgentTaskTerminalCommitter{records: assembly}).CommitAgentTaskApprovalTerminal(t.Context(), agentmodel.AgentTaskRun{})
	_, _ = workflowDependencies(assembly).PrepareAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{})
	_, _ = workflowDependencies(assembly).PrepareAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{Contract: definitionmodel.WorkflowAgentTaskNodeContract{Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 0}}})
	_, _ = workflowDependencies(assembly).PrepareAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{Contract: definitionmodel.WorkflowAgentTaskNodeContract{Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 3}}})
	assemblyWithPrincipal := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Dependencies: RuntimeServicesDependencies{AgentPrincipals: agentPrincipalDirectoryStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}}}})
	_, _ = resolveIdentityPrincipal(t.Context(), assemblyWithPrincipal.agentPrincipals, "user", "role")
	if _, err := (runtimeInteractiveWorkflowStarter{records: &runtimeAssembly{}}).StartInteractiveAgentWorkflow(t.Context(), "workflow", nil, "run", "key", principalmodel.Principal{}); apperror.CodeOf(err) != "agent.interactive.workflow_handoff_unavailable" {
		t.Fatalf("ownerless err=%v", err)
	}
}

func TestRuntimeCompositionWiresPersistentAgentWorkersAndInteractiveFactory(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "agent-wiring.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	binding, err := agentsdkfixture.Open(t.Context(), store, "composition-agent-wiring-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	repositories, ok := binding.(agentpersistence.Binding)
	if !ok || repositories.AgentTaskRunRepository() == nil {
		t.Fatal("Agent SDK Binding returned no task-run repository")
	}
	repository := repositories.AgentTaskRunRepository()
	withoutRunner := NewRuntimeServices(t.Context(), RuntimeServicesConfig{Dependencies: RuntimeServicesDependencies{AgentTaskRuns: repository, AgentPrincipals: agentPrincipalDirectoryStub{}}})
	if withoutRunner.Applications().AgentInteractiveRuns == nil || withoutRunner.Applications().NewAgentInteractive != nil {
		t.Fatalf("runnerless applications=%+v", withoutRunner.Applications())
	}
	auditRepository := &runtimeServicesAuditRepository{}
	services := NewRuntimeServices(t.Context(), RuntimeServicesConfig{Dependencies: RuntimeServicesDependencies{AgentTaskRuns: repository, AgentTaskRunner: agentTaskRunnerStub{}, AgentInteractiveRunner: interactiveAgentRunnerStub{}, Audit: auditRepository}})
	applications := services.Applications()
	if applications.AgentTasks == nil || applications.AgentInteractiveRuns == nil || applications.AgentTaskWorker == nil || applications.NewAgentInteractive == nil {
		t.Fatalf("applications=%+v", applications)
	}
	if interactive := applications.NewAgentInteractive(nil); interactive == nil {
		t.Fatal("interactive execution service is nil")
	}
	now := time.Now().UTC()
	deadLetter := agentmodel.AgentTaskRun{ID: "agent-task-dead-letter", WorkspaceID: "workspace-a", TaskKey: "agent.retry", TaskVersion: "v1", Status: agentmodel.AgentTaskRunDeadLetter, Outcome: "error", IdempotencyKey: "agent-task-dead-letter", Revision: 3, CreatedAt: now, UpdatedAt: now, CompletedAt: &now}
	if _, _, err := repository.Create(t.Context(), deadLetter); err != nil {
		t.Fatal(err)
	}
	actor := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: deadLetter.WorkspaceID, UserID: "operator"}}, accessfixture.Bundle{Key: "ops"})
	retried, replayed, err := applications.AgentTasks.Operate(t.Context(), deadLetter.WorkspaceID, deadLetter.ID, "retry", "retry-after-repair", "provider timeout repaired", actor)
	if err != nil || replayed || retried.Status != agentmodel.AgentTaskRunPending || len(auditRepository.events) != 1 || auditRepository.events[0].Event != "agent_task_retry" {
		t.Fatalf("retried=%+v replayed=%v audits=%+v err=%v", retried, replayed, auditRepository.events, err)
	}
}

func (compositionConnectorAdapterStub) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, nil
}

type compositionIntegrationEventWorker struct {
	integrationrepository.IntegrationWorkerRepository
	event integrationmodel.IntegrationEvent
}

type compositionIntegrationIdentityRepository struct {
	integrationrepository.IntegrationConfigRepository
	identities []integrationmodel.IntegrationExternalIdentity
}

func (r *compositionIntegrationIdentityRepository) ListExternalIdentities(context.Context, string) ([]integrationmodel.IntegrationExternalIdentity, error) {
	return append([]integrationmodel.IntegrationExternalIdentity(nil), r.identities...), nil
}

func (r *compositionIntegrationIdentityRepository) UpsertExternalIdentity(_ context.Context, _ string, identity integrationmodel.IntegrationExternalIdentity) (integrationmodel.IntegrationExternalIdentity, error) {
	for index := range r.identities {
		if r.identities[index].Key == identity.Key {
			r.identities[index] = identity
			return identity, nil
		}
	}
	r.identities = append(r.identities, identity)
	return identity, nil
}

func (w *compositionIntegrationEventWorker) ListDueEvents(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationEvent, error) {
	return []integrationmodel.IntegrationEvent{w.event}, nil
}

func (w *compositionIntegrationEventWorker) ClaimEvent(_ context.Context, _, _, owner, _ string) (integrationmodel.IntegrationEvent, bool, error) {
	claimed := w.event
	claimed.Status = "executing"
	claimed.LeaseOwner = owner
	claimed.FencingToken = 1
	w.event = claimed
	return claimed, true, nil
}

func (w *compositionIntegrationEventWorker) HeartbeatEvent(context.Context, string, string, string, int64, string) (integrationmodel.IntegrationEvent, error) {
	return w.event, nil
}

func (w *compositionIntegrationEventWorker) UpdateEventStatus(_ context.Context, _, _, leaseOwner string, fencingToken int64, status, errorText, _ string) (integrationmodel.IntegrationEvent, error) {
	w.event.LeaseOwner = leaseOwner
	w.event.FencingToken = fencingToken
	w.event.Status = status
	w.event.Error = errorText
	return w.event, nil
}

func TestCompositionSmallAdaptersCoverAllDelegationBranches(t *testing.T) {
	ensureRecordSchemaSnapshotProvider(nil)
	partial := &runtimeAssembly{}
	ensureRecordSchemaSnapshotProvider(partial)
	if snapshot := partial.Schema(); len(snapshot.Objects) != 0 {
		t.Fatalf("partial schema snapshot=%#v", snapshot)
	}

	executions, inserts, updates := 0, 0, 0
	adapter := schedulerOperationRuntimeAdapter{
		processExecutions: func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			executions++
			return workflowmodel.WorkflowProcessResult{Processed: 1}, nil
		},
		insertRecord: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			inserts++
			return nil
		},
		updateRecord: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			updates++
			return nil
		},
	}
	if _, err := adapter.ProcessDueWorkflowExecutions(t.Context(), 1, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.InsertSchedulerRecord(t.Context(), "workspace-primary", definitionmodel.ObjectSchema{}, recordmodel.Record{}, "test"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.UpdateSchedulerRecord(t.Context(), "workspace-primary", definitionmodel.ObjectSchema{}, recordmodel.Record{}, "test"); err != nil {
		t.Fatal(err)
	}
	if executions != 1 || inserts != 1 || updates != 1 {
		t.Fatalf("scheduler delegation=%d/%d/%d", executions, inserts, updates)
	}
}

func TestRecordInitializationClosuresServeSurfaceContext(t *testing.T) {
	company := definitionmodel.ObjectSchema{Key: "company", Name: "Company", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	customer := definitionmodel.ObjectSchema{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"}, {Key: "company", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "company"}},
		{Key: "owner", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "identity_user"}},
	}}
	repository := &pipelineFailureRepository{records: map[string]map[string]recordmodel.Record{
		"customer": {"customer-1": {ID: "customer-1", Data: map[string]any{"name": "Ada", "company": "company-1", "owner": "admin"}}},
		"company":  {"company-1": {ID: "company-1", Data: map[string]any{"name": "Example"}}},
	}}
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			Objects: []definitionmodel.ObjectSchema{customer, company},
		},
		Dependencies: RuntimeServicesDependencies{Records: repository, IdentityDirectory: compositionIdentityDirectory{}},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	result := runtime.surfaceContextService.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{
		Objects: []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: "customer", Page: 1, PageSize: 5}},
	}, principal)
	if result.Objects["customer"].ObjectKey != "customer" || len(result.Objects["customer"].Page.Items) != 1 {
		t.Fatalf("surface context result=%#v", result)
	}
	limited := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{
		Permissions: []string{"customer.read"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}},
		ReferencePolicies: []accessfixture.ReferencePolicyFixture{{SourceObjectKey: "customer", RelationFieldKey: "company", TargetObjectKey: "company", DisplayFields: []string{"name"}, Mode: "label_only"}},
	})
	limitedResult := runtime.surfaceContextService.Context(t.Context(), surfacecontextmodel.SurfaceContextRequest{
		Objects: []surfacecontextmodel.SurfaceContextObjectRequest{{ObjectKey: "customer"}},
	}, limited)
	if len(limitedResult.RelationProjections) != 1 || limitedResult.RelationProjections[0].Mode != "label_only" {
		t.Fatalf("label-only surface projection=%#v", limitedResult.RelationProjections)
	}
}

func TestRecordInitializationAuditProjectorCoversPresentationAndFailures(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	auditRepository := &runtimeServicesAuditRepository{events: []auditmodel.AuditEvent{
		{ObjectKey: "unknown", RecordID: "unknown-1", Before: map[string]any{"name": "Unknown"}},
		{ObjectKey: "customer", Before: map[string]any{"name": "No record"}},
		{ObjectKey: "customer", RecordID: "customer-empty"},
		{ObjectKey: "customer", RecordID: "customer-before", Before: map[string]any{"name": "Before"}},
		{ObjectKey: "customer", RecordID: "customer-after", After: map[string]any{"name": "After"}},
		{ObjectKey: "customer", RecordID: "customer-both", Before: map[string]any{"name": "Old"}, After: map[string]any{"name": "New"}},
	}}
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{customer}},
		Dependencies: RuntimeServicesDependencies{
			Records: &pipelineFailureRepository{records: map[string]map[string]recordmodel.Record{}},
			Audit:   auditRepository,
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "auditor", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{
		Permissions:  []string{"identity.audit.view", "customer.audit"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}},
	})
	events, err := runtime.auditApplicationService.Events(t.Context(), auditmodel.AuditEventQuery{}, principal)
	if err != nil || len(events) != 6 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if events[3].Before["name"] != "Before" || events[4].After["name"] != "After" || events[5].Before["name"] != "Old" || events[5].After["name"] != "New" {
		t.Fatalf("projected events=%#v", events)
	}
	if _, err := runtime.reportQueriesService.Summary(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("missing report error=%v", err)
	}

	invalidPolicyPrincipal := principal
	invalidPolicyRole := accessfixture.FromPrincipal(invalidPolicyPrincipal)
	invalidPolicyRole.FieldPolicies = []accessfixture.FieldPolicyFixture{{
		ObjectKey: "customer", FieldKey: "name",
		Policies: []accessfixture.FieldRuleFixture{{
			Key: "invalid-audit-policy", Actions: []string{"audit"}, Effect: "invalid",
		}},
	}}
	invalidPolicyPrincipal = accessfixture.Attach(invalidPolicyPrincipal, invalidPolicyRole)
	auditRepository.events = []auditmodel.AuditEvent{{
		ObjectKey: "customer", RecordID: "customer-before", Before: map[string]any{"name": "Before"},
	}}
	if _, err := runtime.auditApplicationService.Events(t.Context(), auditmodel.AuditEventQuery{}, invalidPolicyPrincipal); apperror.CodeOf(err) != "backend.field_policy.invalid_effect" {
		t.Fatalf("before projection error=%v", err)
	}
	auditRepository.events = []auditmodel.AuditEvent{{
		ObjectKey: "customer", RecordID: "customer-after", After: map[string]any{"name": "After"},
	}}
	if _, err := runtime.auditApplicationService.Events(t.Context(), auditmodel.AuditEventQuery{}, invalidPolicyPrincipal); apperror.CodeOf(err) != "backend.field_policy.invalid_effect" {
		t.Fatalf("after projection error=%v", err)
	}
}

/*
	Legacy in-Runtime provider/connection/event composition was removed when

Integration ownership moved behind the SDK Binding.

	func TestWorkflowConnectorSchemaProviderFindsRegisteredAdapter(t *testing.T) {
		providers := connectortest.Registry(connectortest.Provider("mock", "test", compositionConnectorAdapterStub{}, nil))
		runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
			Manifest:     manifestmodel.ManifestSchema{Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "mock", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "test"}}}}}},
			Dependencies: RuntimeServicesDependencies{ConnectorProviders: providers},
		})
		provider := runtimeWorkflowSchemaProvider{records: runtime}
		if _, ok := runtime.connectorRegistry.ProviderAdapter("mock", "test"); !ok {
			t.Fatal("exact mock provider adapter was not registered")
		}
		if !provider.ConnectorAdapterExists(t.Context(), "mock") {
			t.Fatal("registered mock connector was not reported ready")
		}
	}

	func TestCompositionConsumesFrozenPublicConnectorRegistryDirectly(t *testing.T) {
		operation := connector.CallOperation[map[string]any, map[string]any]{
			ConnectorKey: "mock", ProviderKey: "project", Key: "ping",
			ContractSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Reliability: connector.ReliabilityContract{
				Effect: connector.EffectRead, Idempotency: connector.IdempotencyContract{Strategy: connector.IdempotencyNatural},
				Reconciliation: connector.ReconciliationNone, Compensation: connector.CompensationContract{Mode: connector.CompensationNone},
			},
		}
		bound, err := connector.BindCall(operation, func(context.Context, connector.TypedRequest[map[string]any]) (connector.TypedResult[map[string]any], error) {
			return connector.TypedResult[map[string]any]{Output: map[string]any{"ok": true}}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		provider, err := connector.NewProvider(connector.ProviderSchema{ConnectorKey: "mock", ProviderKey: "project", ProviderRevision: "test-v1"}, bound)
		if err != nil {
			t.Fatal(err)
		}
		providers := connector.NewRegistry()
		if err := providers.Register(provider); err != nil {
			t.Fatal(err)
		}
		providers.Freeze()

		runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
			Manifest:     manifestmodel.ManifestSchema{Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "mock", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "project"}}}}}},
			Dependencies: RuntimeServicesDependencies{ConnectorProviders: providers},
		})
		if _, ok := runtime.connectorRegistry.ProviderAdapter("mock", "project"); !ok {
			t.Fatal("composition did not resolve the public provider from the supplied frozen Registry")
		}
		if _, ok := runtime.connectorRegistry.ProviderAdapter("mock", ""); ok {
			t.Fatal("composition introduced a connector-only public provider fallback")
		}
	}

	func TestIntegrationCompositionInvokesProviderReferenceAndSchemaClosures(t *testing.T) {
		delivery := &runtimeServicesDeliveryRepository{}
		runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
			Manifest: manifestmodel.ManifestSchema{
				Objects: []definitionmodel.ObjectSchema{{Key: "activity", Name: "Activity", Fields: []definitionmodel.FieldSchema{
					{Key: "subject", Type: "text"}, {Key: "owner_id", Type: "text"}, {Key: "status", Type: "text"},
				}}},
				Integrations: integrationmodel.IntegrationSchema{
					Connectors: []integrationmodel.ConnectorSchema{{Key: "email", Type: "email", Provider: "test", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "test"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send"}}}},
					EventMappings: []integrationmodel.IntegrationEventMappingSchema{{
						Key: "owner", Provider: "provider", EventType: "created", TargetType: "owner_task",
						ExternalIdentity: integrationmodel.IntegrationExternalIdentityMappingSchema{SubjectPath: "subject", OnUnmapped: "read_only"},
					}},
				},
			},
			Dependencies: RuntimeServicesDependencies{IntegrationDelivery: delivery},
		})
		principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*", businessintegration.PermissionInvoke, businessintegration.PermissionRetry}})

		invocation, err := runtime.integrationService.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{
			ConnectorKey: "email", ProviderKey: "test", Operation: "send",
		}, principal)
		if err != nil || invocation.ProviderKey != "test" {
			t.Fatalf("integration invocation=%#v error=%v", invocation, err)
		}

		resolver := serviceReferenceResolver(&runtime.integrationService)
		if references, err := resolver(t.Context(), "missing", principal); err != nil || len(references) != 0 {
			t.Fatalf("connection references=%#v error=%v", references, err)
		}

		event := integrationmodel.IntegrationEvent{ID: "event-1", WorkspaceID: "workspace-primary", Provider: "provider", EventType: "created", Payload: map[string]any{"title": "Follow up", "subject": "external-1"}}
		if _, handled, err := runtime.integrationService.ExecuteIntegrationEventMapping(t.Context(), event, principal); !handled || err == nil {
			t.Fatalf("owner task without Record repository handled=%t error=%v", handled, err)
		}

		records := &pipelineFailureRepository{records: map[string]map[string]recordmodel.Record{}}
		config := &compositionIntegrationIdentityRepository{identities: []integrationmodel.IntegrationExternalIdentity{{
			Key: "provider_external-2", WorkspaceID: "workspace-primary", Provider: "provider", ExternalSubject: "external-2",
			ExternalSubjectType: "user", ActorID: "admin", RoleKey: "admin", Status: "active",
		}}}
		worker := &compositionIntegrationEventWorker{event: integrationmodel.IntegrationEvent{
			ID: "event-2", WorkspaceID: "workspace-primary", Provider: "provider", EventType: "created",
			Payload: map[string]any{"title": "Follow up", "subject": "external-2"},
		}}
		snapshot := runtime.Schema()
		workerRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
			Manifest: manifestmodel.ManifestSchema{Objects: snapshot.Objects, Integrations: snapshot.Integrations},
			Dependencies: RuntimeServicesDependencies{
				Records: records, IntegrationConfig: config, IntegrationDelivery: &runtimeServicesDeliveryRepository{}, IntegrationWorker: worker,
				AgentPrincipals: agentPrincipalDirectoryStub{principal: principal},
			},
		})
		result, err := workerRuntime.integrationService.ProcessDueIntegrationEvents(t.Context(), 1, principal)
		if err != nil || result.Processed != 1 || len(records.records["activity"]) != 1 {
			t.Fatalf("mapped event result=%#v activities=%#v error=%v", result, records.records["activity"], err)
		}

		canonical := workerRuntime.integrationService
		workerRuntime.integrationService = nil
		if rebuilt := integrationApplication(workerRuntime); rebuilt == nil {
			t.Fatal("integration composition fallback was not assembled")
		}
		if _, err := integrationRuntimeActionInvoker(workerRuntime.Applications())(t.Context(), actionmodel.ActionInvocation{ActionKey: "missing", Principal: principal}); err == nil {
			t.Fatal("integration action callback did not delegate")
		}
		workerRuntime.integrationService = canonical
	}
*/
func TestCompositionFinalBranchContracts(t *testing.T) {
	recordResult := workflowActionInvocationResult(actionmodel.ActionInvocationResult{Record: &actionmodel.ActionResult{RecordID: "record-1"}})
	if recordResult.Record == nil || recordResult.Record.RecordID != "record-1" {
		t.Fatalf("workflow action record result=%#v", recordResult)
	}
	if result := workflowActionInvocationResult(actionmodel.ActionInvocationResult{}); result.Record != nil {
		t.Fatalf("empty workflow action result=%#v", result)
	}

	services := NewRuntimeServices(t.Context(), RuntimeServicesConfig{})
	_ = services.Schema()
	_ = services.SchemaForPrincipal(t.Context(), principalmodel.Principal{})

	nonDefaultState := newRuntimeServicesState(t.Context(), manifestmodel.ManifestSchema{}, RuntimeServicesDependencies{
		IdentityDirectory:      compositionIdentityDirectory{},
		IntegrationPolicyStore: resilience.NewMemoryStore(resilience.DefaultMemoryCapacity),
		IntegrationAPILimiter:  ratelimit.NewMemoryLimiter(ratelimit.DefaultMemoryCapacity),
	})
	if nonDefaultState.identityDirectory == nil || nonDefaultState.integrationPolicyStore == nil || nonDefaultState.apiKeyRateLimiter == nil {
		t.Fatal("explicit runtime state dependencies were not retained")
	}
}

func TestCompositionActionWithoutRegisteredHandlerFailsClosed(t *testing.T) {
	actions := []definitionmodel.ActionSchema{{Key: "customer.call", ObjectKey: "customer", Kind: "object_operation"}}
	delivery := &runtimeServicesDeliveryRepository{}
	config := &runtimeServicesIntegrationConfigRepository{connections: []integrationmodel.IntegrationConnection{{
		Key: "main", WorkspaceID: "workspace-primary", ConnectorKey: "email", ProviderKey: "test", Status: "active",
	}}}
	processes := &runtimeServicesWorkflowProcessRepository{processes: []workflowmodel.WorkflowProcessInstance{{ID: "process-1", Status: "running", ObjectKey: "customer", RecordID: "customer-1"}}}
	connector := integrationmodel.ConnectorSchema{Key: "email", Type: "email", Provider: "test", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send", SideEffect: "write", IdempotencySupported: true}}}
	providers := connectortest.Registry(connectortest.Provider("email", "test", compositionConnectorAdapterStub{}, connector.Operations))
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}, Actions: actions,
			Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}},
		},
		Dependencies: RuntimeServicesDependencies{
			IntegrationConfig: config, IntegrationDelivery: delivery, WorkflowProcesses: processes, WorkflowWorker: &runtimeServicesWorkflowWorkerRepository{}, ConnectorProviders: providers,
		},
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	if _, err := runtime.actionService.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: "customer.call", ObjectKey: "customer", Principal: admin}); apperror.CodeOf(err) != "backend.action.owner_unresolved" {
		t.Fatalf("unregistered source-owned handler error=%v code=%q", err, apperror.CodeOf(err))
	}
}

type actionWiringAssuranceStore struct {
	grants     map[string]actionmodel.ActionAssuranceGrant
	consumeErr error
}

type actionWiringMetadataRepository struct {
	appschemarepository.ApplicationSchemaRepository
	revision string
	err      error
}

func (r actionWiringMetadataRepository) SnapshotRevision(context.Context, principalmodel.SystemScope) (string, error) {
	return r.revision, r.err
}

type actionWiringBusinessHandler struct {
	descriptor runtimeext.HandlerDescriptor
	intent     bool
}

func (h actionWiringBusinessHandler) Descriptor() runtimeext.HandlerDescriptor { return h.descriptor }
func (h actionWiringBusinessHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	if h.intent {
		_, err := execution.StageDurableIntent(ctx, runtimeext.DurableIntent{
			ConsumerKey:    "connector",
			ConnectionKey:  "connection",
			OperationKey:   "notify",
			ContractSHA256: strings.Repeat("c", 64),
			Payload:        map[string]any{"message": "ready"},
		})
		if err != nil {
			return nil, err
		}
	}
	return json.RawMessage(`{}`), nil
}

func (s *actionWiringAssuranceStore) SaveActionAssuranceGrant(_ context.Context, grant actionmodel.ActionAssuranceGrant) error {
	if s.grants == nil {
		s.grants = map[string]actionmodel.ActionAssuranceGrant{}
	}
	s.grants[grant.ID] = grant
	return nil
}

func (s *actionWiringAssuranceStore) GetActionAssuranceGrant(_ context.Context, id string) (actionmodel.ActionAssuranceGrant, bool, error) {
	grant, ok := s.grants[id]
	return grant, ok, nil
}

func (s *actionWiringAssuranceStore) ConsumeActionAssuranceGrant(_ context.Context, _ string, id string, consumedAt time.Time) (bool, error) {
	if s.consumeErr != nil {
		return false, s.consumeErr
	}
	grant, ok := s.grants[id]
	if !ok || grant.ConsumedAt != "" {
		return false, nil
	}
	grant.ConsumedAt = consumedAt.Format(time.RFC3339Nano)
	s.grants[id] = grant
	return true, nil
}

func TestActionAssuranceValidatorRemainingBranches(t *testing.T) {
	const (
		actionKey = "order.approve"
		objectKey = "order"
		recordID  = "order-1"
	)
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	repository := &runtimeServicesRecordActionRepository{
		found:  true,
		record: recordmodel.Record{ID: recordID, Data: map[string]any{"approval_version": "7", "approval_hash": "hash-7"}},
	}
	records := &runtimeAssembly{
		actions:    map[string]definitionmodel.ActionSchema{},
		schema:     map[string]definitionmodel.ObjectSchema{objectKey: {Key: objectKey}},
		recordRepo: repository,
	}
	var auditEvents []string
	audit := func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _ map[string]any) {
		auditEvents = append(auditEvents, event)
	}
	invocation := actionmodel.ActionInvocation{
		ActionKey: actionKey,
		ObjectKey: objectKey,
		RecordID:  recordID,
		Principal: principal,
		Input:     map[string]any{"amount": "10"},
	}
	validator := actionAssuranceValidator(records, actionservice.NewActionAssuranceDomainService(nil, nil), audit)
	if evidence, err := validator(t.Context(), invocation); err != nil || evidence != nil {
		t.Fatalf("missing action evidence=%v err=%v", evidence, err)
	}

	records.actions[actionKey] = definitionmodel.ActionSchema{Key: actionKey, ObjectKey: objectKey}
	if evidence, err := validator(t.Context(), invocation); err != nil || evidence != nil {
		t.Fatalf("nil assurance policy evidence=%v err=%v", evidence, err)
	}
	records.actions[actionKey] = definitionmodel.ActionSchema{
		Key: actionKey, ObjectKey: objectKey,
		AssurancePolicy: &definitionmodel.ActionAssurancePolicy{},
	}
	if evidence, err := validator(t.Context(), invocation); err != nil || evidence != nil {
		t.Fatalf("empty assurance methods evidence=%v err=%v", evidence, err)
	}
	records.actions[actionKey] = definitionmodel.ActionSchema{
		Key: actionKey, ObjectKey: objectKey,
		AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{" normal_login "}},
	}
	evidence, err := validator(t.Context(), invocation)
	if err != nil || evidence["methods"] != definitionmodel.ActionAssuranceNormalLogin || auditEvents[len(auditEvents)-1] != "action_assurance_succeeded" {
		t.Fatalf("normal login evidence=%v events=%v err=%v", evidence, auditEvents, err)
	}

	records.actions[actionKey] = definitionmodel.ActionSchema{
		Key: actionKey, ObjectKey: objectKey,
		AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}},
	}
	if _, err := validator(t.Context(), invocation); err == nil || auditEvents[len(auditEvents)-1] != "action_assurance_denied" {
		t.Fatalf("missing assurance store err=%v events=%v", err, auditEvents)
	}

	store := &actionWiringAssuranceStore{}
	assurance := actionservice.NewActionAssuranceDomainService(store, nil)
	validator = actionAssuranceValidator(records, assurance, audit)
	issue := func(t *testing.T, methods []string, approvalVersion, approvalHash string) string {
		t.Helper()
		token, _, err := assurance.IssueVerifiedGrant(t.Context(), actionmodel.ActionAssuranceIssueRequest{
			WorkspaceID: principal.WorkspaceID, UserID: principal.UserID,
			ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID,
			Payload: invocation.Input, VerifiedMethods: methods,
			ApprovalVersion: approvalVersion, ApprovalHash: approvalHash,
		}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	invocation.AssuranceToken = issue(t, []string{definitionmodel.ActionAssuranceOTP}, "", "")
	evidence, err = validator(t.Context(), invocation)
	if err != nil || evidence["grant_id"] == "" || evidence["payload_digest"] == "" {
		t.Fatalf("otp evidence=%v err=%v", evidence, err)
	}

	workflowAction := definitionmodel.ActionSchema{
		Key: actionKey, ObjectKey: objectKey,
		AssurancePolicy: &definitionmodel.ActionAssurancePolicy{
			RequiredMethods:      []string{definitionmodel.ActionAssuranceWorkflowApproval},
			ApprovalVersionField: "approval_version",
			ApprovalHashField:    "approval_hash",
		},
	}
	records.actions[actionKey] = workflowAction
	runWorkflow := func(t *testing.T) (map[string]string, error) {
		t.Helper()
		invocation.AssuranceToken = issue(t, []string{definitionmodel.ActionAssuranceWorkflowApproval}, "7", "hash-7")
		return validator(t.Context(), invocation)
	}
	repository.getErr = errors.New("record lookup failed")
	if _, err := runWorkflow(t); !errors.Is(err, repository.getErr) {
		t.Fatalf("record lookup error=%v", err)
	}
	repository.getErr = nil
	repository.found = false
	if _, err := runWorkflow(t); err == nil {
		t.Fatal("missing approval record must fail stale")
	}
	repository.found = true
	repository.record.Data["approval_version"] = "8"
	if _, err := runWorkflow(t); err == nil {
		t.Fatal("approval version mismatch must fail stale")
	}
	repository.record.Data["approval_version"] = "7"
	repository.record.Data["approval_hash"] = "hash-8"
	if _, err := runWorkflow(t); err == nil {
		t.Fatal("approval hash mismatch must fail stale")
	}
	repository.record.Data["approval_hash"] = "hash-7"
	evidence, err = runWorkflow(t)
	if err != nil || evidence["approval_version"] != "7" || evidence["approval_hash"] != "hash-7" {
		t.Fatalf("workflow approval evidence=%v err=%v", evidence, err)
	}
	if !actionAssuranceContains([]string{"otp", " workflow_approval "}, definitionmodel.ActionAssuranceWorkflowApproval) ||
		actionAssuranceContains([]string{"otp"}, definitionmodel.ActionAssuranceWorkflowApproval) {
		t.Fatal("assurance method containment mismatch")
	}
}

func TestAssembledActionPipelineBindingCoversPlanFailureAndCommits(t *testing.T) {
	service, repository, _, _, principal := pipelineFailureFixture(t, 0, false, false)
	invoke := func(input map[string]any) (actionmodel.ActionInvocationResult, error) {
		return service.Applications().Actions.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
			ActionKey: "pipeline_item.advance", ObjectKey: "pipeline_item", RecordID: "item_1",
			Input: input, Principal: principal,
		})
	}
	if _, err := invoke(map[string]any{"to_stage": "missing"}); err == nil {
		t.Fatal("missing destination stage must fail through assembled binding")
	}
	result, err := invoke(map[string]any{"to_stage": "stage_new"})
	if err != nil || result.Record == nil || result.Record.Record.Data["current_stage"] != "stage_new" {
		t.Fatalf("pipeline result=%#v err=%v", result, err)
	}
	if repository.records["pipeline_item"]["item_1"].Data["current_stage"] != "stage_new" {
		t.Fatalf("pipeline commit missing: %#v", repository.records["pipeline_item"]["item_1"])
	}

	bulkService, _, _, _, bulkPrincipal := pipelineFailureFixture(t, 0, false, false)
	bulk, err := bulkService.Applications().Actions.ExecuteBulkAction(t.Context(), "pipeline_item", "pipeline_item.advance", actionmodel.ActionBulkRequest{
		RecordIDs: []string{"item_1"}, Data: map[string]any{"to_stage": "stage_new"}, IdempotencyKey: "bulk-pipeline-1",
	}, bulkPrincipal)
	if err != nil || bulk.Total != 1 {
		t.Fatalf("pipeline bulk result=%#v err=%v", bulk, err)
	}
}

func TestAssembledBusinessHandlerCoversRevisionAndDurableIntentFallbacks(t *testing.T) {
	const actionKey = "notification_job.notify"
	hash := strings.Repeat("a", 64)
	descriptor := runtimeext.HandlerDescriptor{
		ActionKey: actionKey, InputType: "project.NotifyInput", OutputType: "project.NotifyOutput",
		InputContractSHA256: hash, OutputContractSHA256: hash, HandlerRevision: "handler-1",
		ConnectorCapabilities: []runtimeext.ActionConnectorCapability{{
			ConnectorKey: "connector", ConnectionKey: "connection", OperationKey: "notify",
			ContractSHA256: strings.Repeat("c", 64),
			Mode:           runtimeext.ConnectorModeEnqueue,
			Effect:         runtimeext.ConnectorEffectWrite,
		}},
	}
	action := definitionmodel.ActionSchema{
		Key: actionKey, ObjectKey: "notification_job", Kind: definitionmodel.ActionKindObjectOperation,
		InputType: descriptor.InputType, OutputType: descriptor.OutputType,
		InputContractSHA256: descriptor.InputContractSHA256, OutputContractSHA256: descriptor.OutputContractSHA256,
	}
	role := accessfixture.Bundle{
		Key: "operator", Permissions: []string{"notification_job.*"}, RecordScope: "all_records",
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "notification_job", Scope: "all_records", Read: true, Write: true}},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, role)
	newService := func(t *testing.T, repository appschemarepository.ApplicationSchemaRepository, withIntent bool) *runtimeAssembly {
		t.Helper()
		registry := runtimeext.NewBusinessHandlerRegistry()
		if err := registry.Register(actionWiringBusinessHandler{descriptor: descriptor, intent: withIntent}); err != nil {
			t.Fatal(err)
		}
		service := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
			Manifest: manifestmodel.ManifestSchema{
				TemplateID: "action-wiring", Version: "1", Name: "Action Wiring",
				Objects: []definitionmodel.ObjectSchema{{Key: "notification_job"}},
				Actions: []definitionmodel.ActionSchema{action},
			},
			Dependencies: RuntimeServicesDependencies{
				ActionRuntimeRevision: "runtime-1", ActionProjectRevision: "project-1", ActionMetadataRevision: "metadata-fallback",
				ApplicationSchema: repository, BusinessHandlers: registry,
				ActionExecutions: &runtimeServicesActionExecutionRepository{records: &runtimeServicesRecordActionRepository{}},
			},
		})
		return service
	}
	invoke := func(service *runtimeAssembly) (actionmodel.ActionInvocationResult, error) {
		return service.Applications().Actions.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
			ActionKey: actionKey, ObjectKey: "notification_job", Principal: principal,
		})
	}

	withoutMetadata := newService(t, nil, false)
	if result, err := invoke(withoutMetadata); err != nil || result.Object == nil {
		t.Fatalf("fallback metadata revision result=%#v err=%v", result, err)
	}
	withMetadata := newService(t, actionWiringMetadataRepository{revision: "metadata-live"}, false)
	if result, err := invoke(withMetadata); err != nil || result.Object == nil {
		t.Fatalf("live metadata revision result=%#v err=%v", result, err)
	}
	metadataFailure := errors.New("metadata snapshot failed")
	withMetadataFailure := newService(t, actionWiringMetadataRepository{err: metadataFailure}, false)
	if _, err := invoke(withMetadataFailure); !errors.Is(err, metadataFailure) {
		t.Fatalf("metadata revision error=%v", err)
	}

	withoutIntegration := newService(t, nil, true)
	withoutIntegration.integrationService = nil
	if _, err := invoke(withoutIntegration); err == nil || !strings.Contains(err.Error(), "durable_intent_validator_required") {
		t.Fatalf("missing durable intent validator error=%v", err)
	}
	withIntegration := newService(t, nil, true)
	withIntegration.integrationService = publicationhandoff.NewPublicationHandoffApplicationService(publicationhandoff.Dependencies{})
	if _, err := invoke(withIntegration); err == nil || strings.Contains(err.Error(), "durable_intent_validator_required") {
		t.Fatalf("configured durable intent validator error=%v", err)
	}
}

func TestActionAssuranceHelpersTrimMethods(t *testing.T) {
	if !actionAssuranceOnlyNormalLogin([]string{" normal_login "}) {
		t.Fatal("normal login method should be trimmed")
	}
	if actionAssuranceOnlyNormalLogin(nil) || actionAssuranceOnlyNormalLogin([]string{"normal_login", "otp"}) {
		t.Fatal("only-normal-login helper accepted invalid shape")
	}
	if got := strings.TrimSpace(definitionmodel.ActionAssuranceNormalLogin); got == "" {
		t.Fatal("normal login constant unexpectedly blank")
	}
}

type recordPolicyCandidateRepository struct {
	recordrepository.RecordRepository
	matched bool
	err     error
	called  bool
}

func (r *recordPolicyCandidateRepository) CandidateScopeMatches(
	_ context.Context,
	workspaceID string,
	candidate recordmodel.Record,
	expression recordmodel.RecordScopeExpression,
) (bool, error) {
	r.called = true
	if workspaceID != "workspace-a" || candidate.Data["order_id"] != "order-north" || len(expression.Path) != 1 {
		return false, errors.New("unexpected candidate scope input")
	}
	return r.matched, r.err
}

func TestRecordQueryPolicyCompositionCandidateEvaluatorAvailability(t *testing.T) {
	order := definitionmodel.ObjectSchema{
		Key: "order",
		Fields: []definitionmodel.FieldSchema{{
			Key: "warehouse_id", Type: "relation",
		}},
	}
	reservation := definitionmodel.ObjectSchema{
		Key: "reservation",
		Fields: []definitionmodel.FieldSchema{{
			Key: "order_id", Type: "relation", Config: map[string]any{"object_key": "order"},
		}},
	}
	predicate := &accessfixture.PredicateFixture{
		Operator: "in",
		Path: []accessfixture.RelationSegmentFixture{{
			Direction: "forward", RelationFieldKey: "order_id", TargetObjectKey: "order",
		}},
		FieldKey: "warehouse_id", ValueSource: "actor_claim", ClaimKey: "warehouse_ids",
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", OrganizationScopes: identitysdk.OrganizationScopes{WarehouseIDs: []string{"warehouse-north"}}}}, accessfixture.Bundle{Permissions: []string{"reservation.update"}, DataPolicies: []accessfixture.DataPolicyFixture{{
		ObjectKey: "reservation", Scope: "custom", Read: true, Write: true, Predicate: predicate,
	}}},
	)
	candidate := recordmodel.Record{ID: "reservation-new", Data: map[string]any{"order_id": "order-north"}}
	repository := &recordPolicyCandidateRepository{matched: true}
	services := &runtimeAssembly{
		schema:     map[string]definitionmodel.ObjectSchema{"order": order, "reservation": reservation},
		recordRepo: repository,
		actions:    map[string]definitionmodel.ActionSchema{},
		workflows:  map[string]definitionmodel.WorkflowSchema{},
	}
	ensureRecordSchemaSnapshotProvider(services)
	policy := newRecordQueryPolicyService(services)
	allowed, err := policy.CanWriteCandidateScope(t.Context(), principal, reservation, candidate)
	if err != nil || !allowed || !repository.called {
		t.Fatalf("candidate allowed=%v called=%v err=%v", allowed, repository.called, err)
	}

	repository.called = false
	repository.err = errors.New("candidate evaluation failed")
	if _, err := policy.CanWriteCandidateScope(t.Context(), principal, reservation, candidate); !errors.Is(err, repository.err) || !repository.called {
		t.Fatalf("candidate evaluator error=%v called=%v", err, repository.called)
	}

	services.recordRepo = &runtimeServicesRecordActionRepository{}
	policy = newRecordQueryPolicyService(services)
	if allowed, err := policy.CanWriteCandidateScope(t.Context(), principal, reservation, candidate); allowed || err == nil || !strings.Contains(err.Error(), "evaluator is unavailable") {
		t.Fatalf("missing evaluator allowed=%v err=%v", allowed, err)
	}
}

type schedulerWorkflowTimerRuntimeFake struct {
	resumedProcessID string
	resumedNodeID    string
	deadlineTaskID   string
	deadlinePhase    string
	err              error
}

func (f *schedulerWorkflowTimerRuntimeFake) ResumeTimerNode(
	_ context.Context,
	_ string,
	processID string,
	nodeID string,
	_ principalmodel.Principal,
) (workflowmodel.WorkflowProcessInstance, error) {
	f.resumedProcessID, f.resumedNodeID = processID, nodeID
	return workflowmodel.WorkflowProcessInstance{}, f.err
}

func (f *schedulerWorkflowTimerRuntimeFake) ProcessApprovalDeadlineTimer(
	_ context.Context,
	_ string,
	taskID string,
	phase string,
	_ principalmodel.Principal,
) error {
	f.deadlineTaskID, f.deadlinePhase = taskID, phase
	return f.err
}

type schedulerActionTimerRuntimeFake struct {
	source     actionmodel.ActionSource
	invocation actionmodel.ActionInvocation
	err        error
}

func (f *schedulerActionTimerRuntimeFake) Invoke(
	_ context.Context,
	source actionmodel.ActionSource,
	invocation actionmodel.ActionInvocation,
) (actionmodel.ActionInvocationResult, error) {
	f.source, f.invocation = source, invocation
	return actionmodel.ActionInvocationResult{}, f.err
}

func TestSchedulerOperationRuntimeAdapterTimerTargets(t *testing.T) {
	bare := &runtimeAssembly{}
	adapter := newSchedulerOperationRuntimeAdapter(bare)
	if err := adapter.ExecuteRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "resume_node",
	}, principalmodel.Principal{}); err == nil || !strings.Contains(err.Error(), "unsupported workflow timer target") {
		t.Fatalf("nil workflow runtime error=%v", err)
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "action", TargetKey: "order.close",
	}, principalmodel.Principal{}); err == nil || !strings.Contains(err.Error(), "action timer runtime is not configured") {
		t.Fatalf("nil action runtime error=%v", err)
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "unknown", TargetKey: "target",
	}, principalmodel.Principal{}); err == nil || !strings.Contains(err.Error(), "unsupported record timer target type") {
		t.Fatalf("unknown target type error=%v", err)
	}

	assembled := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			TemplateID: "timer-wiring", Version: "1", Name: "Timer Wiring",
		},
	})
	adapter = newSchedulerOperationRuntimeAdapter(assembled)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
	if result, err := adapter.ProcessDueWorkflowExecutions(t.Context(), 1, principal); err != nil || result.Processed != 0 {
		t.Fatalf("empty workflow queue result=%#v err=%v", result, err)
	}
	if err := adapter.InsertSchedulerRecord(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "missing"}, recordmodel.Record{ID: "record-1"}, "test insert"); err == nil {
		t.Fatal("scheduler insert without repository unexpectedly succeeded")
	}
	if err := adapter.UpdateSchedulerRecord(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "missing"}, recordmodel.Record{ID: "record-1"}, "test update"); err == nil {
		t.Fatal("scheduler update without repository unexpectedly succeeded")
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "unsupported",
	}, principal); err == nil || !strings.Contains(err.Error(), "unsupported workflow timer target") {
		t.Fatalf("assembled unsupported workflow target error=%v", err)
	}
	actionExecution := schedulerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "action", TargetKey: "missing.action",
		ObjectKey: "order", RecordID: "order-1",
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), actionExecution, principal); err == nil {
		t.Fatal("missing assembled action unexpectedly succeeded")
	}

	workflowRuntime := &schedulerWorkflowTimerRuntimeFake{}
	actionRuntime := &schedulerActionTimerRuntimeFake{}
	resume := schedulerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "resume_node",
		Payload: map[string]any{"process_id": "process-1", "node_id": "timer-1"},
	}
	if err := executeSchedulerRecordTimer(t.Context(), resume, principal, workflowRuntime, actionRuntime); err != nil ||
		workflowRuntime.resumedProcessID != "process-1" || workflowRuntime.resumedNodeID != "timer-1" {
		t.Fatalf("resume runtime=%#v err=%v", workflowRuntime, err)
	}
	deadline := schedulerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "approval_deadline",
		Payload: map[string]any{"task_id": "task-1", "phase": "reminder"},
	}
	if err := executeSchedulerRecordTimer(t.Context(), deadline, principal, workflowRuntime, actionRuntime); err != nil ||
		workflowRuntime.deadlineTaskID != "task-1" || workflowRuntime.deadlinePhase != "reminder" {
		t.Fatalf("deadline runtime=%#v err=%v", workflowRuntime, err)
	}
	if err := executeSchedulerRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{
		TargetType: "workflow", TargetKey: "unsupported",
	}, principal, workflowRuntime, actionRuntime); err == nil || !strings.Contains(err.Error(), "unsupported workflow timer target") {
		t.Fatalf("unsupported workflow target error=%v", err)
	}
	actionRuntime.err = errors.New("action failed")
	actionExecution.IdempotencyKey = "timer-action-1"
	if err := executeSchedulerRecordTimer(t.Context(), actionExecution, principal, workflowRuntime, actionRuntime); !errors.Is(err, actionRuntime.err) ||
		actionRuntime.source != actionmodel.ActionSourceScheduler || actionRuntime.invocation.ActionKey != "missing.action" ||
		actionRuntime.invocation.Principal.UserID != principal.UserID || actionRuntime.invocation.Actor.UserID != principal.UserID {
		t.Fatalf("action source=%q invocation=%#v err=%v", actionRuntime.source, actionRuntime.invocation, err)
	}
	if err := executeSchedulerRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{
		TargetType: "unsupported",
	}, principal, workflowRuntime, actionRuntime); err == nil || !strings.Contains(err.Error(), "unsupported record timer target type") {
		t.Fatalf("unsupported timer target error=%v", err)
	}
}
