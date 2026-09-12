package composition

import (
	"context"
	"encoding/json"
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"
	"testing"
	"time"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	connectortest "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit/connectors"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type compositionConnectorAdapterStub struct{}

type actionNotificationIdentityProjection struct {
	found bool
	err   error
}

func (d actionNotificationIdentityProjection) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{ID: "recipient"}, d.found, d.err
}

func (actionNotificationIdentityProjection) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}

func (actionNotificationIdentityProjection) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	return nil, nil
}

func (actionNotificationIdentityProjection) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return nil, nil
}

func (actionNotificationIdentityProjection) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
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

type agentPrincipalResolverStub struct{ principal principalmodel.Principal }

func (s agentPrincipalResolverStub) Resolve(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
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
		t.Fatalf("nil projection err=%v", err)
	}
	wantErr := errors.New("projection failed")
	assembly.identityProjection = actionNotificationIdentityProjection{err: wantErr}
	intent := runtimeext.NotificationIntent{RecipientUserIDs: []string{" recipient "}}
	if _, err := compileActionNotification(assembly)(t.Context(), "event", intent, principalmodel.Principal{}); !errors.Is(err, wantErr) {
		t.Fatalf("projection err=%v", err)
	}
	assembly.identityProjection = actionNotificationIdentityProjection{}
	if _, err := compileActionNotification(assembly)(t.Context(), "event", intent, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.notification.recipient_invalid" {
		t.Fatalf("missing recipient err=%v", err)
	}
	assembly.identityProjection = actionNotificationIdentityProjection{found: true}
	value := "Ada"
	var compiled notificationmodel.NotificationIntent
	assembly.recordNotificationCompiler = func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		compiled = intent
		return notificationmodel.NotificationEvent{ID: intent.ID}, nil
	}
	intent = runtimeext.NotificationIntent{
		EventType: " order.ready ", SourceEventID: " source ", RecipientUserIDs: []string{" recipient "},
		SubjectObjectKey: " order ", SubjectRecordID: " order-1 ", SubjectVersion: " v1 ", DedupeKey: " dedupe ", GroupKey: " group ",
		ActionState: " completed ", AlertState: " resolved ", ExpiresAt: time.Date(2026, 8, 11, 1, 2, 3, 0, time.FixedZone("CST", 8*60*60)),
		OccurredAt: time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC), Variables: []runtimeext.NotificationVariable{{Key: " name ", StringValue: &value}},
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	if event, err := compileActionNotification(assembly)(t.Context(), "event-1", intent, principal); err != nil || event.ID != "event-1" || compiled.ActionState != notificationmodel.NotificationActionCompleted || compiled.AlertState != notificationmodel.NotificationAlertResolved || compiled.ExpiresAt != "2026-08-10T17:02:03Z" || compiled.Variables["name"] != "Ada" {
		t.Fatalf("event=%+v compiled=%+v err=%v", event, compiled, err)
	}
	intent.ActionState, intent.AlertState, intent.ExpiresAt, intent.Alert = "", "", time.Time{}, true
	if _, err := compileActionNotification(assembly)(t.Context(), "event-2", intent, principal); err != nil || compiled.AlertState != notificationmodel.NotificationAlertFiring {
		t.Fatalf("legacy alert compiled=%+v err=%v", compiled, err)
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
	dependencies := workflowDependencies(&runtimeAssembly{})
	if _, err := dependencies.StartAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{}); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("err=%v", err)
	}
	assembly := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	_, _ = (runtimeInteractiveWorkflowStarter{records: assembly}).StartInteractiveAgentWorkflow(t.Context(), "missing", nil, "run", "key", principalmodel.Principal{})
	_, _ = (runtimeAgentRecordVisibility{records: assembly}).CanReadAgentRecord(t.Context(), "missing", "record", principalmodel.Principal{})
	_, _ = workflowDependencies(assembly).StartAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{})
	_, _ = workflowDependencies(assembly).StartAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{Contract: definitionmodel.WorkflowAgentTaskNodeContract{Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 0}}})
	_, _ = workflowDependencies(assembly).StartAgentTask(t.Context(), workflowapplication.WorkflowAgentTaskPreparation{Contract: definitionmodel.WorkflowAgentTaskNodeContract{Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 3}}})
	assemblyWithPrincipal := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Dependencies: RuntimeServicesDependencies{AgentPrincipals: agentPrincipalResolverStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}}}})
	_, _ = resolveIdentityPrincipal(t.Context(), assemblyWithPrincipal.agentPrincipals, "user", "role")
	if _, err := (runtimeInteractiveWorkflowStarter{records: &runtimeAssembly{}}).StartInteractiveAgentWorkflow(t.Context(), "workflow", nil, "run", "key", principalmodel.Principal{}); apperror.CodeOf(err) != "agent.interactive.workflow_handoff_unavailable" {
		t.Fatalf("ownerless err=%v", err)
	}
}

func (compositionConnectorAdapterStub) Call(context.Context, connectortest.CallRequest) (connectortest.CallResult, error) {
	return connectortest.CallResult{}, nil
}

func TestCompositionSmallAdaptersCoverAllDelegationBranches(t *testing.T) {
	ensureRecordSchemaSnapshotProvider(nil)
	partial := &runtimeAssembly{}
	ensureRecordSchemaSnapshotProvider(partial)
	if snapshot := partial.Schema(); len(snapshot.Objects) != 0 {
		t.Fatalf("partial schema snapshot=%#v", snapshot)
	}

	executions := 0
	adapter := scheduledWorkflowRuntimeAdapter{
		processExecutions: func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			executions++
			return workflowmodel.WorkflowProcessResult{Processed: 1}, nil
		},
	}
	if _, err := adapter.ProcessDueWorkflowExecutions(t.Context(), 1, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Fatalf("scheduler delegation=%d", executions)
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
		Permissions:  []string{"audit.business.read", "customer.audit"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all", Read: true}},
	})
	events, err := runtime.auditApplicationService.Events(t.Context(), auditmodel.AuditEventQuery{}, principal)
	if err != nil || len(events) != 6 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if events[3].Before["name"] != "Before" || events[4].After["name"] != "After" || events[5].Before["name"] != "Old" || events[5].After["name"] != "New" {
		t.Fatalf("projected events=%#v", events)
	}
	if runtime.reportModuleQueryHost == nil {
		t.Fatal("Report module query host was not assembled")
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
		IdentityProjection: compositionIdentityProjection{},
	})
	if nonDefaultState.identityProjection == nil {
		t.Fatal("explicit runtime state dependencies were not retained")
	}
}

func TestCompositionActionWithoutRegisteredHandlerFailsClosed(t *testing.T) {
	actions := []definitionmodel.ActionSchema{{Key: "customer.call", ObjectKey: "customer", Kind: "object_operation"}}
	processes := &runtimeServicesWorkflowProcessRepository{processes: []workflowmodel.WorkflowProcessInstance{{ID: "process-1", Status: "running", ObjectKey: "customer", RecordID: "customer-1"}}}
	connector := connectormodel.ConnectorSchema{Key: "email", Providers: []connectormodel.ConnectorProviderSchema{{Key: "test"}}, Operations: []connectormodel.ConnectorOperationSchema{{Key: "send", SideEffect: "write", IdempotencySupported: true}}}
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}, Actions: actions,
			Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{connector}},
		},
		Dependencies: RuntimeServicesDependencies{WorkflowProcesses: processes, WorkflowWorker: &runtimeServicesWorkflowWorkerRepository{}},
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{
		Permissions:  []string{"customer.call"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all", Read: true, Write: true}},
	})
	if _, err := runtime.actionService.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: "customer.call", ObjectKey: "customer", IdempotencyKey: "customer-call-owner-check", Principal: admin}); apperror.CodeOf(err) != "backend.action.owner_unresolved" {
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
	timeZone string
	err      error
}

func (r actionWiringMetadataRepository) SnapshotRevision(context.Context, principalmodel.SystemScope) (string, error) {
	return r.revision, r.err
}

func (r actionWiringMetadataRepository) ExecutionConfiguration(context.Context, principalmodel.SystemScope) (connectormodel.ApplicationExecutionConfiguration, error) {
	return connectormodel.ApplicationExecutionConfiguration{SchemaRevision: r.revision, TimeZone: r.timeZone}, r.err
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
	zone, err := runtimeext.ResolveApplicationTimeZone(execution)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"revision": execution.Identity().ApplicationSchemaRevision, "time_zone": zone})
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
	if _, err := validator(t.Context(), invocation); apperror.CodeOf(err) != "backend.action.assurance_required" || auditEvents[len(auditEvents)-1] != "action_assurance_denied" {
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
			Input: input, IdempotencyKey: "pipeline-advance-" + input["to_stage"].(string), Principal: principal,
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
		Key: "operator", Permissions: []string{actionKey},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "notification_job", Scope: "all", Read: true, Write: true}},
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
			ActionKey: actionKey, ObjectKey: "notification_job", IdempotencyKey: "notification-job-notify", Principal: principal,
		})
	}

	withoutMetadata := newService(t, nil, false)
	if result, err := invoke(withoutMetadata); err != nil || result.Object == nil || result.Object.Output["revision"] != "metadata-fallback" || result.Object.Output["time_zone"] != "UTC" {
		t.Fatalf("fallback metadata revision result=%#v err=%v", result, err)
	}
	withMetadata := newService(t, actionWiringMetadataRepository{revision: "metadata-live", timeZone: "Asia/Tokyo"}, false)
	if result, err := invoke(withMetadata); err != nil || result.Object == nil || result.Object.Output["revision"] != "metadata-live" || result.Object.Output["time_zone"] != "Asia/Tokyo" {
		t.Fatalf("live metadata revision result=%#v err=%v", result, err)
	}
	metadataFailure := errors.New("metadata snapshot failed")
	withMetadataFailure := newService(t, actionWiringMetadataRepository{err: metadataFailure}, false)
	if _, err := invoke(withMetadataFailure); !errors.Is(err, metadataFailure) {
		t.Fatalf("metadata revision error=%v", err)
	}

	withoutIntegration := newService(t, nil, true)
	withoutIntegration.publicationHandoffService = nil
	if _, err := invoke(withoutIntegration); err == nil || !strings.Contains(err.Error(), "durable_intent_validator_required") {
		t.Fatalf("missing durable intent validator error=%v", err)
	}
	withIntegration := newService(t, nil, true)
	withIntegration.publicationHandoffService = publicationhandoff.NewPublicationHandoffApplicationService(publicationhandoff.Dependencies{})
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
	principal := accessfixture.Attach(principalmodel.Principal{
		Principal:      identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"},
		BusinessClaims: map[string]profilebindingmodel.ClaimValue{"warehouse_ids": {Type: "relation_list", Value: []string{"warehouse-north"}}},
	}, accessfixture.Bundle{Permissions: []string{"reservation.update"}, DataPolicies: []accessfixture.DataPolicyFixture{{
		ObjectKey: "reservation", Read: true, Write: true, Predicate: predicate,
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

type recordTimerWorkflowRuntimeFake struct {
	resumedProcessID string
	resumedNodeID    string
	deadlineTaskID   string
	deadlinePhase    string
	err              error
}

func (f *recordTimerWorkflowRuntimeFake) ResumeTimerNode(
	_ context.Context,
	_ string,
	processID string,
	nodeID string,
	_ principalmodel.Principal,
) (workflowmodel.WorkflowProcessInstance, error) {
	f.resumedProcessID, f.resumedNodeID = processID, nodeID
	return workflowmodel.WorkflowProcessInstance{}, f.err
}

func (f *recordTimerWorkflowRuntimeFake) ProcessApprovalDeadlineTimer(
	_ context.Context,
	_ string,
	taskID string,
	phase string,
	_ principalmodel.Principal,
) error {
	f.deadlineTaskID, f.deadlinePhase = taskID, phase
	return f.err
}

type recordTimerActionRuntimeFake struct {
	source     actionmodel.ActionSource
	invocation actionmodel.ActionInvocation
	err        error
}

func (f *recordTimerActionRuntimeFake) Invoke(
	_ context.Context,
	source actionmodel.ActionSource,
	invocation actionmodel.ActionInvocation,
) (actionmodel.ActionInvocationResult, error) {
	f.source, f.invocation = source, invocation
	return actionmodel.ActionInvocationResult{}, f.err
}

func TestRecordTimerRuntimeAdapterTargets(t *testing.T) {
	bare := &runtimeAssembly{}
	adapter := newRecordTimerTargetRuntimeAdapter(bare)
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "resume_node",
	}, principalmodel.Principal{}); err == nil || !strings.Contains(err.Error(), "unsupported workflow timer target") {
		t.Fatalf("nil workflow runtime error=%v", err)
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "action", TargetKey: "order.close",
	}, principalmodel.Principal{}); err == nil || !strings.Contains(err.Error(), "action timer runtime is not configured") {
		t.Fatalf("nil action runtime error=%v", err)
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "unknown", TargetKey: "target",
	}, principalmodel.Principal{}); err == nil || !strings.Contains(err.Error(), "unsupported record timer target type") {
		t.Fatalf("unknown target type error=%v", err)
	}

	assembled := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			TemplateID: "timer-wiring", Version: "1", Name: "Timer Wiring",
		},
	})
	schedulerAdapter := newScheduledWorkflowRuntimeAdapter(assembled)
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}
	if result, err := schedulerAdapter.ProcessDueWorkflowExecutions(t.Context(), 1, principal); err != nil || result.Processed != 0 {
		t.Fatalf("empty workflow queue result=%#v err=%v", result, err)
	}
	adapter = newRecordTimerTargetRuntimeAdapter(assembled)
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "unsupported",
	}, principal); err == nil || !strings.Contains(err.Error(), "unsupported workflow timer target") {
		t.Fatalf("assembled unsupported workflow target error=%v", err)
	}
	actionExecution := recordtimerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "action", TargetKey: "missing.action",
		ObjectKey: "order", RecordID: "order-1",
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), actionExecution, principal); err == nil {
		t.Fatal("missing assembled action unexpectedly succeeded")
	}

	workflowRuntime := &recordTimerWorkflowRuntimeFake{}
	actionRuntime := &recordTimerActionRuntimeFake{}
	resume := recordtimerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "resume_node",
		Payload: map[string]any{"process_id": "process-1", "node_id": "timer-1"},
	}
	if err := executeRecordTimer(t.Context(), resume, principal, workflowRuntime, actionRuntime); err != nil ||
		workflowRuntime.resumedProcessID != "process-1" || workflowRuntime.resumedNodeID != "timer-1" {
		t.Fatalf("resume runtime=%#v err=%v", workflowRuntime, err)
	}
	deadline := recordtimerapplication.RecordTimerExecution{
		WorkspaceID: "workspace-a", TargetType: "workflow", TargetKey: "approval_deadline",
		Payload: map[string]any{"task_id": "task-1", "phase": "reminder"},
	}
	if err := executeRecordTimer(t.Context(), deadline, principal, workflowRuntime, actionRuntime); err != nil ||
		workflowRuntime.deadlineTaskID != "task-1" || workflowRuntime.deadlinePhase != "reminder" {
		t.Fatalf("deadline runtime=%#v err=%v", workflowRuntime, err)
	}
	if err := executeRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{
		TargetType: "workflow", TargetKey: "unsupported",
	}, principal, workflowRuntime, actionRuntime); err == nil || !strings.Contains(err.Error(), "unsupported workflow timer target") {
		t.Fatalf("unsupported workflow target error=%v", err)
	}
	actionRuntime.err = errors.New("action failed")
	actionExecution.IdempotencyKey = "timer-action-1"
	if err := executeRecordTimer(t.Context(), actionExecution, principal, workflowRuntime, actionRuntime); !errors.Is(err, actionRuntime.err) ||
		actionRuntime.source != actionmodel.ActionSourceRecordTimer || actionRuntime.invocation.ActionKey != "missing.action" ||
		actionRuntime.invocation.Principal.UserID != principal.UserID || actionRuntime.invocation.Actor.UserID != principal.UserID {
		t.Fatalf("action source=%q invocation=%#v err=%v", actionRuntime.source, actionRuntime.invocation, err)
	}
	if err := executeRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{
		TargetType: "unsupported",
	}, principal, workflowRuntime, actionRuntime); err == nil || !strings.Contains(err.Error(), "unsupported record timer target type") {
		t.Fatalf("unsupported timer target error=%v", err)
	}
}
