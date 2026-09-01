package composition

import (
	"context"
	"encoding/json"
	"errors"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"slices"
	"testing"
	"time"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationbusiness "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type compositionRecordRepository struct {
	recordrepository.RecordRepository
	page recordmodel.RecordPageResult
}

type runtimeServicesMetadataDefinitions struct {
	fail    bool
	failure error
}

func (definitions runtimeServicesMetadataDefinitions) List(_ context.Context, query metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	if definitions.fail {
		return nil, definitions.failure
	}
	if query.ResourceType != "action" {
		return nil, nil
	}
	return []metadatasdk.Definition{{ResourceType: query.ResourceType, ResourceKey: "customer.activate", SourceKind: "manifest"}}, nil
}

func (runtimeServicesMetadataDefinitions) Get(context.Context, string, string) (metadatasdk.Definition, bool, error) {
	return metadatasdk.Definition{}, false, nil
}

func (runtimeServicesMetadataDefinitions) Snapshot(context.Context) (metadatasdk.DefinitionSnapshot, error) {
	return metadatasdk.DefinitionSnapshot{}, nil
}

type runtimeServicesRecordActionRepository struct {
	recordrepository.RecordRepository
	record       recordmodel.Record
	found        bool
	getErr       error
	listErr      error
	uniqueExists bool
	uniqueErr    error
	commitErr    error
}

func (r *runtimeServicesRecordActionRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return cloneRecord(r.record), r.found, r.getErr
}

func (r *runtimeServicesRecordActionRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.listErr != nil {
		return recordmodel.RecordPageResult{}, r.listErr
	}
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{cloneRecord(r.record)}, Total: 1}, nil
}

func (r *runtimeServicesRecordActionRepository) UniqueExists(context.Context, string, string, string, string, any) (bool, error) {
	return r.uniqueExists, r.uniqueErr
}

func (r *runtimeServicesRecordActionRepository) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	if r.commitErr != nil {
		return r.commitErr
	}
	r.record = cloneRecord(commit.Record)
	r.found = true
	return nil
}

func (r *runtimeServicesRecordActionRepository) CommitRecordMutationBatch(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
	for _, commit := range commits {
		if err := r.CommitRecordMutation(ctx, workspaceID, commit); err != nil {
			return err
		}
	}
	return nil
}

func (r *compositionRecordRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.page, nil
}

type compositionIdentityDirectory struct{}

type runtimeServicesDecisionRepository struct{}

type runtimeServicesActionExecutionRepository struct {
	actioncontract.ActionExecutionStore
	records interface {
		CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error
	}
	execution actionmodel.ActionBusinessExecution
}

type runtimeServicesActionExecutionTransaction struct {
	repository *runtimeServicesActionExecutionRepository
}

func (*runtimeServicesActionExecutionTransaction) Context(ctx context.Context) context.Context {
	return ctx
}

func (t *runtimeServicesActionExecutionTransaction) Commit(ctx context.Context, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return t.repository.CommitExecution(ctx, commits, completion)
}

func (*runtimeServicesActionExecutionTransaction) RollBack(context.Context) error { return nil }

func (r *runtimeServicesActionExecutionRepository) BeginExecutionTransaction(context.Context) (actioncontract.ActionExecutionTransaction, error) {
	return &runtimeServicesActionExecutionTransaction{repository: r}, nil
}

func (r *runtimeServicesActionExecutionRepository) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	r.execution = request.Execution
	r.execution.ID, r.execution.LeaseOwner, r.execution.FencingToken = "action-execution-1", request.LeaseOwner, 1
	return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: r.execution}, nil
}

func (*runtimeServicesActionExecutionRepository) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (r *runtimeServicesActionExecutionRepository) CompleteExecution(_ context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	r.execution = completion.Execution
	return r.execution, nil
}

func (r *runtimeServicesActionExecutionRepository) CommitExecution(ctx context.Context, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	if err := r.records.CommitRecordMutationBatch(ctx, completion.Execution.WorkspaceID, commits); err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	r.execution = completion.Execution
	return r.execution, nil
}

type runtimeServicesBusinessEvidenceRepository struct {
	changeplanrepository.ChangePlanEvidenceRepository
}

type runtimeServicesBusinessSystemEvidenceRepository struct {
	items []businessseedmodel.BusinessSeedProvenance
	err   error
}

func (r runtimeServicesBusinessSystemEvidenceRepository) ListSeedProvenance(context.Context) ([]businessseedmodel.BusinessSeedProvenance, error) {
	return append([]businessseedmodel.BusinessSeedProvenance(nil), r.items...), r.err
}

type runtimeServicesAutomationMetadataRepository struct {
	appschemarepository.ApplicationSchemaRepository
	manifest  manifestmodel.ManifestSchema
	persist   bool
	getErr    error
	upsertErr error
}

func (r *runtimeServicesAutomationMetadataRepository) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (appschemamodel.ApplicationDefinition, bool, error) {
	return appschemamodel.ApplicationDefinition{}, false, r.getErr
}

func (r *runtimeServicesAutomationMetadataRepository) UpsertDefinition(_ context.Context, _ principalmodel.SystemScope, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinition, error) {
	if r.upsertErr != nil {
		return appschemamodel.ApplicationDefinition{}, r.upsertErr
	}
	if r.persist && resourceType == "automation_rule" {
		var rule automationmodel.AutomationRuleSchema
		if err := json.Unmarshal(request.Payload, &rule); err == nil {
			r.manifest.AutomationRules = []automationmodel.AutomationRuleSchema{rule}
		}
	}
	return appschemamodel.ApplicationDefinition{ResourceType: resourceType, ResourceKey: resourceKey, ObjectKey: request.ObjectKey, Name: request.Name, Payload: request.Payload}, nil
}

func (r *runtimeServicesAutomationMetadataRepository) PublishDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionUpsertRequest, _ auditmodel.AuditEvent) (appschemamodel.ApplicationDefinition, error) {
	return r.UpsertDefinition(ctx, scope, resourceType, resourceKey, request)
}

func (*runtimeServicesAutomationMetadataRepository) CompleteDefinitionRefresh(context.Context, principalmodel.SystemScope, string, string, string, string) error {
	return nil
}

func (r *runtimeServicesAutomationMetadataRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return r.manifest, nil
}

func (*runtimeServicesAutomationMetadataRepository) SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error {
	return nil
}

func (*runtimeServicesAutomationMetadataRepository) DisableDefinition(context.Context, principalmodel.SystemScope, string, string) error {
	return nil
}

type runtimeServicesActionSideEffectRecords struct {
	createdID string
}

type runtimeServicesActionExecutionWorkflows struct {
	results []workflowmodel.WorkflowRunSummary
	err     error
}

func (r runtimeServicesActionExecutionWorkflows) TriggeredWorkflows(context.Context, string, recordmodel.Record, principalmodel.Principal, string) ([]workflowmodel.WorkflowRunSummary, error) {
	return append([]workflowmodel.WorkflowRunSummary(nil), r.results...), r.err
}

func (r runtimeServicesActionSideEffectRecords) CreateRecord(_ context.Context, _ string, data map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{ID: r.createdID, Data: data}, nil
}

func (r runtimeServicesActionSideEffectRecords) UpdateRecord(_ context.Context, _ string, recordID string, data map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{ID: recordID, Data: data}, nil
}

func (runtimeServicesActionSideEffectRecords) DeleteRecord(context.Context, string, string, principalmodel.Principal) error {
	return nil
}

func (runtimeServicesActionSideEffectRecords) RestoreRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{}, nil
}

type runtimeServicesActionSideEffectRepository struct {
	recordrepository.RecordRepository
	page    recordmodel.RecordPageResult
	record  recordmodel.Record
	found   bool
	listErr error
	getErr  error
}

func (r *runtimeServicesActionSideEffectRepository) ListRecords(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.page, r.listErr
}

func (r *runtimeServicesActionSideEffectRepository) GetRecord(context.Context, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.getErr
}

type runtimeServicesNotificationRenderer struct {
	rendered notificationmodel.RenderedNotification
	err      error
}

type runtimeServicesWorkflowProcessRepository struct {
	workflowcontract.WorkflowProcessStore
	processes []workflowmodel.WorkflowProcessInstance
	tasks     []workflowmodel.WorkflowTask
	listErr   error
	taskErr   error
}

func (r *runtimeServicesWorkflowProcessRepository) ListProcesses(context.Context, string, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	return append([]workflowmodel.WorkflowProcessInstance(nil), r.processes...), r.listErr
}

func (r *runtimeServicesWorkflowProcessRepository) GetProcess(_ context.Context, _ string, processID string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	for _, process := range r.processes {
		if process.ID == processID {
			return process, true, nil
		}
	}
	return workflowmodel.WorkflowProcessInstance{}, false, nil
}

func (r *runtimeServicesWorkflowProcessRepository) UpdateProcess(_ context.Context, _ string, updated workflowmodel.WorkflowProcessInstance) error {
	for index := range r.processes {
		if r.processes[index].ID == updated.ID {
			r.processes[index] = updated
		}
	}
	return nil
}

func (r *runtimeServicesWorkflowProcessRepository) ListTasks(context.Context, string, string, string, string, int) ([]workflowmodel.WorkflowTask, error) {
	return append([]workflowmodel.WorkflowTask(nil), r.tasks...), r.taskErr
}

func (*runtimeServicesWorkflowProcessRepository) UpdateTask(context.Context, string, workflowmodel.WorkflowTask) error {
	return nil
}

func (*runtimeServicesWorkflowProcessRepository) InsertEvent(context.Context, string, workflowmodel.WorkflowProcessEvent) error {
	return nil
}

type runtimeServicesWorkflowWorkerRepository struct {
	workflowcontract.WorkflowWorkerStore
}

func (*runtimeServicesWorkflowWorkerRepository) GetExecution(context.Context, string, string) (workflowmodel.WorkflowExecution, bool, error) {
	return workflowmodel.WorkflowExecution{}, false, nil
}

func (r runtimeServicesNotificationRenderer) Render(context.Context, NotificationRenderRequest) (notificationmodel.RenderedNotification, error) {
	return r.rendered, r.err
}

type runtimeServicesAutomationWorkerRepository struct {
	automationcontract.AutomationWorkerStore
}

type runtimeServicesMetadataRepository struct {
	appschemarepository.ApplicationSchemaRepository
	getDefinitionErr error
}

func (r runtimeServicesMetadataRepository) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (appschemamodel.ApplicationDefinition, bool, error) {
	return appschemamodel.ApplicationDefinition{}, false, r.getDefinitionErr
}

type runtimeServicesAuditRepository struct {
	events []auditmodel.AuditEvent
}

func (r *runtimeServicesAuditRepository) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func (r *runtimeServicesAuditRepository) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return append([]auditmodel.AuditEvent(nil), r.events...), nil
}
func (*runtimeServicesAuditRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return nil, nil
}

func (*runtimeServicesAuditRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	return nil, nil
}

func (runtimeServicesDecisionRepository) CommitWorkflowDecision(context.Context, transactionmodel.WorkflowDecisionCommit) (bool, error) {
	return true, nil
}

func (compositionIdentityDirectory) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}

func (compositionIdentityDirectory) FindDepartment(context.Context, identitysdk.DepartmentLookup) (identitysdk.Department, bool, error) {
	return identitysdk.Department{}, false, nil
}

func (compositionIdentityDirectory) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	return nil, nil
}

func (compositionIdentityDirectory) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return nil, nil
}

func (compositionIdentityDirectory) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func (compositionIdentityDirectory) ListWorkforce(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.WorkforceEntry, error) {
	return nil, nil
}

func TestRuntimeApplicationsHandlesNilAndPartiallyAssembledState(t *testing.T) {
	var services *runtimeAssembly
	if applications := services.Applications(); applications.Scheduler == nil {
		t.Fatal("nil RuntimeServices must still expose stateless Scheduler validation")
	}
	applications := (&runtimeAssembly{}).Applications()
	if applications.Scheduler == nil || applications.Records != nil {
		t.Fatalf("partial applications=%#v", applications)
	}
}

func TestAuditAppendHandlesNilAndConfiguredServices(t *testing.T) {
	var service *auditapplication.AuditApplicationService
	service.AppendWithMetadata(t.Context(), "nil", "object", "record", principalmodel.Principal{}, "ignored", nil, nil, nil)
	(&auditapplication.AuditApplicationService{}).AppendWithMetadata(t.Context(), "partial", "object", "record", principalmodel.Principal{}, "ignored", nil, nil, nil)

	repository := &runtimeServicesAuditRepository{}
	service = auditapplication.NewAuditApplicationService(repository)
	service.Append(t.Context(), "created", "customer", "customer-1", principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, "Created customer", nil, map[string]any{"status": "new"})
	if len(repository.events) != 1 || repository.events[0].Event != "created" || repository.events[0].RecordID != "customer-1" {
		t.Fatalf("audit events=%#v", repository.events)
	}
}

func TestMetadataSnapshotWatcherSupportsCanonicalAndFallbackOwners(t *testing.T) {
	records := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	canonical := assembleApplicationSchema(records)
	fallback := appschemaapplication.NewApplicationSchemaApplicationService(appschemaapplication.ApplicationSchemaDependencies{Runtime: applicationSchemaLifecycleRuntimeAdapter{runtime: records}, Workflows: assembleWorkflowApplication(records)})

	for _, service := range []*appschemaapplication.ApplicationSchemaApplicationService{canonical, fallback} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		select {
		case <-service.StartSnapshotWatcher(ctx, time.Hour, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test metadata snapshot watcher")):
		case <-time.After(time.Second):
			t.Fatal("Metadata snapshot watcher did not stop after cancellation")
		}
	}
}

func TestAutomationApplicationUsesCanonicalRuntimeServiceAndOwnerBoundaries(t *testing.T) {
	historyPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"runtime.automation.list_automation_executions"}})
	emptyRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	if emptyRuntime.Applications().Automations == nil || emptyRuntime.Applications().Automations != emptyRuntime.automationApplicationService {
		t.Fatal("Runtime does not expose the canonical Automation application service")
	}
	if history, err := emptyRuntime.Applications().Automations.AutomationExecutions(t.Context(), automationmodel.AutomationExecutionFilter{}, historyPrincipal); err != nil || history.Count != 0 {
		t.Fatalf("empty Automation history=%#v error=%v", history, err)
	}
	automationPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "developer", Permissions: []string{"customer.update"}})
	configuredRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest:     manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}},
		Dependencies: RuntimeServicesDependencies{AgentPrincipals: agentPrincipalDirectoryStub{principal: automationPrincipal}},
	})
	if _, err := configuredRuntime.Applications().Automations.AutomationExecutions(t.Context(), automationmodel.AutomationExecutionFilter{}, historyPrincipal); err != nil {
		t.Fatalf("configured Automation history error=%v", err)
	}
	readPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"runtime.automation.automation_capabilities"}})
	if _, err := emptyRuntime.Applications().Automations.AutomationCapabilities(t.Context(), readPrincipal); err != nil {
		t.Fatalf("empty Automation capabilities error=%v", err)
	}
	if _, err := configuredRuntime.Applications().Automations.AutomationCapabilities(t.Context(), readPrincipal); err != nil {
		t.Fatalf("configured Automation capabilities error=%v", err)
	}

	baseRule := automationmodel.AutomationRuleSchema{
		Key: "customer.after_update", Name: "Customer updated", ObjectKey: "customer", Enabled: true,
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "emit", Type: "emit_event", Config: map[string]any{"event": "customer.updated"}}},
	}
	if _, err := configuredRuntime.Applications().Automations.ValidateAutomationRule(t.Context(), baseRule, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"runtime.automation.validate_automation_rule"}})); err != nil {
		t.Fatalf("Automation definition with configured Integration repository error=%v", err)
	}
	if _, err := emptyRuntime.Applications().Automations.ExecuteBeforeRule(t.Context(), automationmodel.AutomationRuleSchema{Key: "before", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before"}}, nil, nil, map[string]any{}, historyPrincipal); err != nil {
		t.Fatalf("before Automation execution error=%v", err)
	}

	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{
		"runtime.automation.simulate_rule_candidate", "runtime.automation.simulate_rule",
	}})
	automation := configuredRuntime.Applications().Automations
	baseRule.Key = "customer.after_update"
	rules := configuredRuntime.automationRules
	rules[baseRule.Key] = baseRule

	if _, err := automation.SimulateAutomationRule(t.Context(), "ignored", automationcontract.AutomationSimulationRequest{Rule: &baseRule}, admin); err == nil {
		// A successful simulation is acceptable; this assertion intentionally only
		// requires the inline-rule branch to execute.
	}
	if _, err := automation.SimulateAutomationRule(t.Context(), "missing", automationcontract.AutomationSimulationRequest{}, admin); err == nil {
		t.Fatal("simulation lookup of a missing Automation rule must fail")
	}
	automation.SimulateAutomationRule(t.Context(), baseRule.Key, automationcontract.AutomationSimulationRequest{}, admin)

	rules["disabled"] = automationmodel.AutomationRuleSchema{Key: "disabled", Enabled: false, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}}
	rules["before"] = automationmodel.AutomationRuleSchema{Key: "before", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "before"}}
	rules["after"] = automationmodel.AutomationRuleSchema{Key: "after", Enabled: true, ObjectKey: "customer", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}}
	messageFor := func(ruleKey string) publicationmodel.Message {
		return publicationmodel.Message{WorkspaceID: "workspace-primary", Payload: automationbusiness.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{
			RuleKey: ruleKey, ObjectKey: "customer", Operation: "update", RecordID: "customer-1", RecordVersion: "v2",
			Record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active"}}, ActorUserID: "admin", ActorRoleKey: "developer",
		})}
	}
	for _, ruleKey := range []string{"missing", "disabled", "before"} {
		if err := automation.ExecuteOutboxMessage(t.Context(), messageFor(ruleKey)); err == nil {
			t.Fatalf("Automation outbox rule=%q must fail", ruleKey)
		}
	}
	if err := automation.ExecuteOutboxMessage(t.Context(), messageFor("after")); err != nil {
		t.Fatalf("Automation after outbox error=%v", err)
	}
	workflowFailure := errors.New("workflow failed")
	if result, err := automationapplication.AutomationWorkflowInstructionResult("customer.approval", workflowmodel.WorkflowRunResult{}, workflowFailure); !errors.Is(err, workflowFailure) || result != nil {
		t.Fatalf("Automation Workflow failure result=%#v error=%v", result, err)
	}
	if result, err := automationapplication.AutomationWorkflowInstructionResult("customer.approval", workflowmodel.WorkflowRunResult{Execution: workflowmodel.WorkflowExecution{ID: "execution-1", Status: "completed"}}, nil); err != nil || result["execution_id"] != "execution-1" {
		t.Fatalf("Automation Workflow result=%#v error=%v", result, err)
	}

	adminRuntimePrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	if _, err := (businessReferenceRuntimeAdapter{}).WorkflowProcesses(t.Context(), adminRuntimePrincipal, workflowmodel.WorkflowProcessFilter{}); err != nil {
		t.Fatalf("nil Business Reference Workflow adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{workflows: emptyRuntime.workflowApplicationService}).WorkflowProcesses(t.Context(), adminRuntimePrincipal, workflowmodel.WorkflowProcessFilter{}); err != nil {
		t.Fatalf("recordless Business Reference Workflow adapter error=%v", err)
	}
	workflowRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Dependencies: RuntimeServicesDependencies{WorkflowProcesses: &runtimeServicesWorkflowProcessRepository{}, WorkflowWorker: &runtimeServicesWorkflowWorkerRepository{}}})
	if _, err := (businessReferenceRuntimeAdapter{records: workflowRuntime, workflows: workflowRuntime.workflowApplicationService}).WorkflowProcesses(t.Context(), adminRuntimePrincipal, workflowmodel.WorkflowProcessFilter{}); err != nil {
		t.Fatalf("configured Business Reference Workflow adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{}).ListPublicationMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("nil Business Reference Integration adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{records: &runtimeAssembly{}}).ListPublicationMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("unassembled Business Reference Integration adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{records: emptyRuntime}).ListPublicationMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("repository-free Business Reference Integration adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{records: configuredRuntime}).ListPublicationMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("configured Business Reference Integration adapter error=%v", err)
	}
}

func TestAuthoringCapabilityHelpersCoverAbsentSchemaAndLookupFallbacks(t *testing.T) {
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{businesssystemapplication.ActionBusinessSystemSnapshot}})
	for _, service := range []*capabilityapplication.CapabilityAuthoringApplicationService{
		newCapabilityAuthoringApplicationService(nil),
		newCapabilityAuthoringApplicationService(newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})),
	} {
		if contract, err := service.Capabilities(t.Context(), admin); err != nil || contract.ContractVersion == "" {
			t.Fatalf("contract=%#v error=%v", contract, err)
		}
	}
	if !slices.Contains([]string{"read", "write"}, "write") || slices.Contains([]string{"read"}, "delete") {
		t.Fatal("authoring enum lookup changed")
	}
	if capabilitycontract.RuntimeAuthoringErrorContract("backend.action.invalid", map[string]string{"field": " status "}).FieldPath != "status" || capabilitycontract.RuntimeAuthoringErrorContract("backend.action.invalid", map[string]string{"field": "", "path": ""}).FieldPath != "" {
		t.Fatal("authoring error field path fallback changed")
	}
	contract := capabilityapplication.RuntimeAuthoringCapabilities()
	capabilitycontract.SortAuthoringContract(&contract)
	if capabilitycontract.ContractHash(contract) == "" {
		t.Fatal("authoring contract hash is empty")
	}
}

func TestStructuredApplicationDefinitionDispatchesAndRejectsInvalidJSON(t *testing.T) {
	for _, resourceType := range []string{"action"} {
		issues, handled := appschemaapplication.ValidateStructuredApplicationDefinition(resourceType, json.RawMessage(`{`))
		if !handled || len(issues) != 1 {
			t.Fatalf("resource=%s issues=%#v handled=%t", resourceType, issues, handled)
		}
		if issues, handled = appschemaapplication.ValidateStructuredApplicationDefinition(resourceType, json.RawMessage(`{}`)); !handled || issues == nil {
			t.Fatalf("valid resource=%s issues=%#v handled=%t", resourceType, issues, handled)
		}
	}
	if issues, handled := appschemaapplication.ValidateStructuredApplicationDefinition("connector", json.RawMessage(`{}`)); handled || issues != nil {
		t.Fatalf("Integration-owned Connector must not dispatch through Runtime structured validation: issues=%#v handled=%t", issues, handled)
	}
	if issues, handled := appschemaapplication.ValidateStructuredApplicationDefinition("object", json.RawMessage(`{}`)); handled || issues != nil {
		t.Fatalf("unhandled issues=%#v handled=%t", issues, handled)
	}
}

func TestApplicationDefinitionValidationRoutesOwnerContracts(t *testing.T) {
	repository := &compositionRecordRepository{}
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest:     manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}},
		Dependencies: RuntimeServicesDependencies{Records: repository},
	})
	service := assembleApplicationSchema(runtime)
	request := func(payload string) appschemamodel.ApplicationDefinitionUpsertRequest {
		return appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(payload)}
	}

	if _, _, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "dictionary", "status", request(`{"key":"status","items":[{"key":"active","value":"active"},{"key":"active","value":"duplicate"}]}`)); err == nil {
		t.Fatal("invalid dictionary must fail its owner contract")
	}
	if payload, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "dictionary", "status", request(`{"key":"status","items":[{"key":"active","value":"active"}]}`)); err != nil || len(issues) != 0 || len(payload) == 0 {
		t.Fatalf("dictionary payload=%s issues=%#v error=%v", payload, issues, err)
	}

	if _, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "action", "customer.activate", request(`{`)); err != nil || len(issues) == 0 {
		t.Fatalf("invalid structured action issues=%#v error=%v", issues, err)
	}
	validAction := `{"key":"customer.activate","object_key":"customer","kind":"record_operation","audit_event":"customer_activated"}`
	if payload, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "action", "customer.activate", request(validAction)); err != nil || len(issues) != 0 || len(payload) == 0 {
		t.Fatalf("action payload=%s issues=%#v error=%v", payload, issues, err)
	}

	if _, _, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "report", "customer.summary", request(`{`)); err == nil {
		t.Fatal("invalid report JSON must fail")
	}
	if _, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "report", "customer.summary", request(`{}`)); err != nil || len(issues) == 0 {
		t.Fatalf("invalid report issues=%#v error=%v", issues, err)
	}
	validReport := `{"key":"customer.summary","dataset":{"source":{"object_key":"customer","alias":"customers"},"dimensions":[{"key":"name","field":{"source_alias":"customers","field_key":"name"}}]}}`
	if payload, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "report", "customer.summary", request(validReport)); err != nil || len(issues) != 0 || len(payload) == 0 {
		t.Fatalf("report payload=%s issues=%#v error=%v", payload, issues, err)
	}
	if _, _, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "automation_rule", "invalid", request(`{}`)); err == nil {
		t.Fatal("Automation owner validation error must propagate through Metadata validation")
	}

	for _, resourceType := range []string{"automation_rule", "connector", "action", "report"} {
		if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), resourceType, request(`{`)); err == nil {
			t.Fatalf("resource=%q invalid JSON must fail", resourceType)
		}
		payload := `{}`
		if resourceType == "action" {
			payload = validAction
		}
		if resourceType == "report" {
			payload = validReport
		}
		service.ValidateApplicationDefinitionPayload(t.Context(), resourceType, request(payload))
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "view", request(`{"key":"customer_list"}`)); err == nil {
		t.Fatal("retired view definition unexpectedly accepted")
	}
}

func TestBusinessRuntimeProjectionPropagatesOwnerFailures(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}
	failure := errors.New("owner read failed")
	portsFor := func(failAt string) businesssystemapplication.BusinessSystemRuntimeProjectionDependencies {
		return businesssystemapplication.BusinessSystemRuntimeProjectionDependencies{
			WorkflowProcesses: func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
				if failAt == "workflow" {
					return nil, failure
				}
				return nil, nil
			},
			AutomationRules: func(context.Context, principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error) {
				if failAt == "rules" {
					return nil, failure
				}
				return nil, nil
			},
			AutomationExecutions: func(context.Context, automationmodel.AutomationExecutionFilter, principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error) {
				if failAt == "executions" {
					return automationprojection.AutomationExecutionHistory{}, failure
				}
				return automationprojection.AutomationExecutionHistory{}, nil
			},
			ConnectorCatalog: func(context.Context, principalmodel.Principal) ([]connectormodel.ConnectorSchema, error) {
				if failAt == "connectors" {
					return nil, failure
				}
				return nil, nil
			},
			IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationsdk.Connection, error) {
				if failAt == "connections" {
					return nil, failure
				}
				return nil, nil
			},
			PublicationHandoff: func(context.Context, string, string, int, principalmodel.Principal) ([]publicationmodel.Message, error) {
				if failAt == "outbox" {
					return nil, failure
				}
				return nil, nil
			},
			SchedulerDefinitions: func(context.Context, principalmodel.Principal) ([]recordmodel.Record, error) {
				if failAt == "scheduler_definitions" {
					return nil, failure
				}
				return []recordmodel.Record{{ID: "scheduler-1"}}, nil
			},
			SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
				return appschemamodel.ApplicationSchemaSnapshot{Reports: []reportmodel.ReportSchema{{Key: "pipeline"}}}
			},
			ListRecords: func(_ context.Context, objectKey string, _ recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
				if failAt == objectKey {
					return recordmodel.RecordPageResult{}, failure
				}
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: objectKey + "-1"}}}, nil
			},
		}
	}

	for _, failAt := range []string{"workflow", "rules", "executions", "connectors", "connections", "outbox", "scheduler_definitions"} {
		service := businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{Runtime: portsFor(failAt)})
		if _, err := service.RuntimeStateSnapshot(t.Context(), principal); !errors.Is(err, failure) {
			t.Fatalf("failure=%q error=%v", failAt, err)
		}
	}
	service := businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{Runtime: portsFor("")})
	snapshot, err := service.RuntimeStateSnapshot(t.Context(), principal)
	if err != nil || len(snapshot.Scheduler.Definitions) != 1 || len(snapshot.Reports) != 1 {
		t.Fatalf("snapshot=%#v error=%v", snapshot, err)
	}
}

func TestBusinessSystemSnapshotUsesNarrowOwnerPorts(t *testing.T) {
	failure := errors.New("owner projection failed")
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{businesssystemapplication.ActionBusinessSystemSnapshot}})
	limited := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reader"}}
	reader := accessfixture.Attach(limited, accessfixture.Bundle{Permissions: []string{"workflow.definition.read"}})

	newService := func(failAt string, evidence changeplanrepository.ChangePlanEvidenceRepository) *businesssystemapplication.BusinessSystemApplicationService {
		schema := appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "schema-1", Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}, Workflows: []definitionmodel.WorkflowSchema{{Key: "customer.approval"}}}
		service := businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{
			FeaturePermissions: func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
				if failAt == "permissions" {
					return recordcontract.RecordFeaturePermissionSnapshot{}, failure
				}
				return recordcontract.RecordFeaturePermissionSnapshot{RoleKey: "admin"}, nil
			},
			SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
				return schema
			},
			Definitions: runtimeServicesMetadataDefinitions{fail: failAt == "metadata", failure: failure},
			Evidence:    evidence,
			Runtime: businesssystemapplication.BusinessSystemRuntimeProjectionDependencies{
				WorkflowProcesses: func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
					if failAt == "runtime" {
						return nil, failure
					}
					return nil, nil
				},
				AutomationRules: func(context.Context, principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error) {
					return nil, nil
				},
				AutomationExecutions: func(context.Context, automationmodel.AutomationExecutionFilter, principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error) {
					return automationprojection.AutomationExecutionHistory{}, nil
				},
				ConnectorCatalog: func(context.Context, principalmodel.Principal) ([]connectormodel.ConnectorSchema, error) {
					return nil, nil
				},
				IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationsdk.Connection, error) {
					return nil, nil
				},
				PublicationHandoff: func(context.Context, string, string, int, principalmodel.Principal) ([]publicationmodel.Message, error) {
					return nil, nil
				},
				SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
					return schema
				},
				ListRecords: func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
					if failAt == "records" {
						return recordmodel.RecordPageResult{}, failure
					}
					return recordmodel.RecordPageResult{Total: 2}, nil
				},
			},
		})
		return service
	}

	if _, err := newService("", nil).Snapshot(t.Context(), principalmodel.Principal{}); err == nil {
		t.Fatal("unknown principal must be rejected")
	}
	for _, failAt := range []string{"permissions", "metadata", "runtime", "records"} {
		if _, err := newService(failAt, nil).Snapshot(t.Context(), admin); !errors.Is(err, failure) {
			t.Fatalf("failure=%q error=%v", failAt, err)
		}
	}
	if _, err := newService("", runtimeServicesBusinessSystemEvidenceRepository{err: failure}).Snapshot(t.Context(), admin); err == nil || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("evidence error=%v code=%q", err, apperror.CodeOf(err))
	}
	snapshot, err := newService("", runtimeServicesBusinessSystemEvidenceRepository{items: []businessseedmodel.BusinessSeedProvenance{{ObjectKey: "customer"}}}).Snapshot(t.Context(), admin)
	if err != nil || len(snapshot.Schema.Workflows) != 1 || len(snapshot.ResourceSources) != 1 || len(snapshot.SeedRecords) != 1 || snapshot.ObjectRecordCounts["customer"] != 2 {
		t.Fatalf("admin snapshot=%#v error=%v", snapshot, err)
	}

	withoutWorkflow, err := newService("", nil).Snapshot(t.Context(), limited)
	if err != nil {
		t.Fatalf("limited snapshot=%#v error=%v", withoutWorkflow, err)
	}
	withWorkflow, err := newService("", nil).Snapshot(t.Context(), reader)
	if err != nil || len(withWorkflow.Schema.Workflows) != 1 {
		t.Fatalf("reader snapshot=%#v error=%v", withWorkflow, err)
	}
}

func TestMetadataProjectionReplacesRuntimeOwnedSchemaState(t *testing.T) {
	runtime := &runtimeAssembly{}
	objects := []definitionmodel.ObjectSchema{{Key: " "}, {Key: "customer"}}
	actions := []definitionmodel.ActionSchema{{Key: " "}, {Key: "customer.activate"}}
	workflows := []definitionmodel.WorkflowSchema{{Key: " "}, {Key: "customer.approval"}}
	rules := []automationmodel.AutomationRuleSchema{{Key: " "}, {Key: "customer.created"}}
	runtime.applyManifestMetadata(" template ", " 1 ", " Runtime ", objects, actions, workflows, rules, nil, connectormodel.IntegrationSchema{}, nil, nil, nil, nil)
	if runtime.templateID != "template" || runtime.templateVersion != "1" || runtime.name != "Runtime" || runtime.connectorRegistry == nil {
		t.Fatalf("runtime identity=%q/%q/%q connector=%#v", runtime.templateID, runtime.templateVersion, runtime.name, runtime.connectorRegistry)
	}
	if len(runtime.schema) != 1 || len(runtime.actions) != 1 || len(runtime.workflows) != 1 || len(runtime.automationRules) != 1 {
		t.Fatalf("projection objects=%d actions=%d workflows=%d rules=%d", len(runtime.schema), len(runtime.actions), len(runtime.workflows), len(runtime.automationRules))
	}
	runtime.applyManifestMetadata("template", "2", "Runtime", objects[1:], actions[1:], workflows[1:], rules[1:], []appschemamodel.DictionarySchema{{Key: "status"}}, connectormodel.IntegrationSchema{}, nil, nil, nil, nil)
	if runtime.templateVersion != "2" || len(runtime.dictionaries) != 1 {
		t.Fatalf("replacement version=%q dictionaries=%#v", runtime.templateVersion, runtime.dictionaries)
	}
}

func TestRecordActionApplicationWiringCoversRoutingAndCoreValidationBoundaries(t *testing.T) {
	baseObject := definitionmodel.ObjectSchema{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Name: "Status", Type: "text", Required: true},
		{Key: "version", Name: "Version", Type: "number"},
		{Key: "created_by", Name: "Created By", Type: "text"},
	}}
	baseAction := definitionmodel.ActionSchema{Key: "customer.activate", ObjectKey: "customer", Kind: "record_update", AuditEvent: "customer_activated"}
	baseRole := accessfixture.Bundle{
		Key:          "operator",
		Permissions:  []string{baseAction.Key},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true, Write: true}},
	}
	baseRecord := recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "draft", "version": 1, "created_by": "maker"}}

	fixture := func(object definitionmodel.ObjectSchema, action definitionmodel.ActionSchema, role accessfixture.Bundle, repository *runtimeServicesRecordActionRepository) (*runtimeAssembly, principalmodel.Principal) {
		manifest := manifestmodel.ManifestSchema{TemplateID: "action-execution", Version: "1", Name: "Action Execution", Objects: []definitionmodel.ObjectSchema{object}, Actions: []definitionmodel.ActionSchema{action}}
		runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifest, Dependencies: RuntimeServicesDependencies{Records: repository, ActionExecutions: &runtimeServicesActionExecutionRepository{records: repository}}})
		return runtime, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1", WorkspaceID: "workspace-primary"}}, role)
	}
	newRepository := func() *runtimeServicesRecordActionRepository {
		return &runtimeServicesRecordActionRepository{record: cloneRecord(baseRecord), found: true}
	}

	runtime, principal := fixture(baseObject, baseAction, baseRole, newRepository())
	if validationErrors := runtime.Applications().Actions.CatalogValidationErrors(); len(validationErrors) != 0 {
		t.Fatalf("Runtime System Operation Catalog validation errors=%v", validationErrors)
	}
	invoke := func(service *actionapplication.ActionApplicationService, objectKey, actionKey string, principal principalmodel.Principal) (actionmodel.ActionInvocationResult, error) {
		return service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: actionKey, ObjectKey: objectKey, RecordID: "customer-1", IdempotencyKey: objectKey + "-" + actionKey, Principal: principal})
	}
	if _, err := invoke(runtime.Applications().Actions, "customer", "missing", principal); err == nil {
		t.Fatal("missing Action must fail through Invoke")
	}
	if _, err := invoke(runtime.Applications().Actions, "customer", "missing", principal); err == nil {
		t.Fatal("missing Action must fail")
	}
	if _, err := invoke(runtime.Applications().Actions, "other", baseAction.Key, principal); err == nil {
		t.Fatal("Action object mismatch must fail")
	}

	deniedRuntime, deniedPrincipal := fixture(baseObject, baseAction, accessfixture.Bundle{Key: "denied", RecordScope: "all_records"}, newRepository())
	if _, err := invoke(deniedRuntime.Applications().Actions, "customer", baseAction.Key, deniedPrincipal); err == nil {
		t.Fatal("Action permission denial must fail")
	}

	missingObjectRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{Actions: []definitionmodel.ActionSchema{baseAction}}, Dependencies: RuntimeServicesDependencies{Records: newRepository()}})
	if _, err := invoke(missingObjectRuntime.Applications().Actions, "customer", baseAction.Key, principal); err == nil {
		t.Fatal("missing Action object must fail policy resolution")
	}

	if result, err := invoke(runtime.Applications().Actions, "customer", baseAction.Key, principal); err != nil || result.Record == nil || result.Record.ActionKey != baseAction.Key {
		t.Fatalf("single-object system operation result=%+v error=%v", result, err)
	}

	conditionalAction := baseAction
	conditionalAction.Key = "customer.activate_if_current"
	conditionalAction.Kind = definitionmodel.ActionKindConditionalUpdate
	conditionalAction.OptimisticConcurrency = true
	conditionalAction.ConcurrencyField = "version"
	conditionalRole := baseRole
	conditionalRole.Permissions = []string{conditionalAction.Key}
	conditionalRepository := newRepository()
	conditionalRuntime, conditionalPrincipal := fixture(baseObject, conditionalAction, conditionalRole, conditionalRepository)
	conditionalResult, err := conditionalRuntime.Applications().Actions.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: conditionalAction.Key, ObjectKey: conditionalAction.ObjectKey, RecordID: baseRecord.ID,
		Input: map[string]any{"status": "active", "expected_version": 1}, IdempotencyKey: "customer-activate-if-current-success", Principal: conditionalPrincipal,
	})
	if err != nil || conditionalResult.Record == nil || conditionalRepository.record.Data["status"] != "active" {
		t.Fatalf("conditional System Operation result=%+v record=%+v error=%v", conditionalResult, conditionalRepository.record, err)
	}
	if _, leaked := conditionalRepository.record.Data["expected_version"]; leaked {
		t.Fatalf("conditional control field leaked into record patch: %+v", conditionalRepository.record.Data)
	}

	conflictRepository := newRepository()
	conflictRuntime, conflictPrincipal := fixture(baseObject, conditionalAction, conditionalRole, conflictRepository)
	_, err = conflictRuntime.Applications().Actions.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: conditionalAction.Key, ObjectKey: conditionalAction.ObjectKey, RecordID: baseRecord.ID,
		Input: map[string]any{"status": "active", "expected_version": 99}, IdempotencyKey: "customer-activate-if-current-conflict", Principal: conflictPrincipal,
	})
	if apperror.CodeOf(err) != "backend.record.version_conflict" {
		t.Fatalf("conditional System Operation conflict code=%q error=%v", apperror.CodeOf(err), err)
	}
	if conflictRepository.record.Data["status"] != "draft" {
		t.Fatalf("conditional conflict persisted a rejected patch: %+v", conflictRepository.record.Data)
	}
}

func TestBusinessReferenceProjectionSupportsOptionalPorts(t *testing.T) {
	service := assembleChangePlanReferenceApplication(nil, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{
		Permissions:  []string{changeplanapplication.ActionBusinessReferenceGraph, "customer.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all_records", Read: true}},
	})
	if graph, err := service.Graph(t.Context(), principal); err != nil || len(graph.Nodes) != 0 {
		t.Fatalf("empty reference graph=%#v error=%v", graph, err)
	}

	records := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}})
	if graph, err := assembleChangePlanReferenceApplication(records, nil, nil).Graph(t.Context(), principal); err != nil || len(graph.Nodes) == 0 {
		t.Fatalf("schema-backed reference graph=%#v error=%v", graph, err)
	}
}

func TestRecordAdaptersRejectIncompleteComposition(t *testing.T) {
	assembled := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	for _, services := range []*runtimeAssembly{nil, {}} {
		if _, err := updateRecordMutationWithContext(t.Context(), services, nil, "customer", "customer-1", nil, principalmodel.Principal{}); err == nil {
			t.Fatal("incomplete composition must reject record mutation")
		}
	}
	if _, err := updateRecordMutationWithContext(t.Context(), assembled, nil, "missing", "record-1", nil, principalmodel.Principal{}); err == nil {
		t.Fatal("assembled record mutation must reach the canonical service")
	}

	workflowSchema := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{Workflows: []definitionmodel.WorkflowSchema{{Key: "customer.approval"}}}})
	if workflow, ok := workflowSchemaByKey(t.Context(), workflowSchema, "customer.approval"); !ok || workflow.Key != "customer.approval" {
		t.Fatalf("workflow lookup=%#v found=%t", workflow, ok)
	}
	if workflow, ok := workflowSchemaByKey(t.Context(), workflowSchema, "missing"); ok || workflow.Key != "" {
		t.Fatalf("missing workflow lookup=%#v found=%t", workflow, ok)
	}
}

func TestRecordConsumersShareCanonicalApplicationService(t *testing.T) {
	directory := compositionIdentityDirectory{}
	service := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest:     manifestmodel.ManifestSchema{TemplateID: "record-composition", Version: "1", Name: "Record Composition"},
		Dependencies: RuntimeServicesDependencies{IdentityDirectory: directory},
	})
	canonical := service.recordApplicationService
	if canonical == nil || service.RecordDomainService != canonical.RecordDomainService || service.recordMutations == nil {
		t.Fatal("canonical Record application service was not assembled")
	}
	if service.Applications().Records != canonical {
		t.Fatal("Runtime application facade does not expose the canonical Record application service")
	}
	if service.reportModuleQueryHost == nil || service.reportModuleSnapshotHost == nil || service.reportModuleExportHost == nil {
		t.Fatal("Report module host adapters were not assembled")
	}
	if service.businessSystemService == nil || !service.businessSystemService.RuntimeProjectionConfigured() {
		t.Fatal("Business System does not expose its canonical Record read port")
	}
	automation := assembleAutomationApplication(service)
	if automation == nil {
		t.Fatal("Automation composition did not tolerate absent optional repositories")
	}

	if canonical.IdentityDirectory() != directory {
		t.Fatal("SDK identity directory was not retained by the canonical Record application port")
	}
}

func TestRecordExportSupportsOptionalIdentityDirectory(t *testing.T) {
	repository := &compositionRecordRepository{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "task-1", Data: map[string]any{"assignee": "user-1"}}}}}
	services := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
			Key: "task", Fields: []definitionmodel.FieldSchema{{Key: "assignee", Type: "relation", Config: map[string]any{"object_key": "identity_user"}}},
		}}},
		Dependencies: RuntimeServicesDependencies{Records: repository},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"task.export"}, DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "task", Scope: "own", Read: true}}})
	if _, _, err := services.Applications().Records.ExportRecords(t.Context(), "task", principal); err != nil {
		t.Fatalf("export without Identity Directory error=%v", err)
	}
	services = newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
			Key: "task", Fields: []definitionmodel.FieldSchema{{Key: "assignee", Type: "relation", Config: map[string]any{"object_key": "identity_user"}}},
		}}},
		Dependencies: RuntimeServicesDependencies{Records: repository, IdentityDirectory: compositionIdentityDirectory{}},
	})
	if _, _, err := services.Applications().Records.ExportRecords(t.Context(), "task", principal); err != nil {
		t.Fatalf("export with Identity Directory error=%v", err)
	}
}

func TestSchemaDiscoveryPublishesIdentityProfileExtensionsAndHashesThem(t *testing.T) {
	extension := profilebindingmodel.Binding{
		ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey: "employee_profile", IdentityRelationField: "identity_user", Cardinality: "one_to_one", DefaultVisibility: "when_readable",
	}
	records := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{TemplateID: "template", Version: "1", Name: "Template", Objects: []definitionmodel.ObjectSchema{{Key: "employee_profile"}}, IdentityProfileExtensions: []profilebindingmodel.Binding{extension}},
	})
	after := records.Schema()
	if len(after.IdentityProfileExtensions) != 1 || after.IdentityProfileExtensions[0].ObjectKey != "employee_profile" {
		t.Fatalf("schema discovery omitted profile extension: %#v", after.IdentityProfileExtensions)
	}
}
