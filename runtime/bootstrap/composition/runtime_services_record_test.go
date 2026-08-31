package composition

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationbusiness "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type compositionRecordRepository struct {
	recordrepository.RecordRepository
	page recordmodel.RecordPageResult
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

type runtimeServicesDeliveryRepository struct {
	integrationrepository.IntegrationDeliveryRepository
	insertErr     error
	invocationErr error
	inserted      []integrationmodel.IntegrationOutboxMessage
	invocations   []integrationmodel.IntegrationInvocation
}

func (r *runtimeServicesDeliveryRepository) ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error) {
	return []integrationmodel.IntegrationInvocation{}, nil
}

func (r *runtimeServicesDeliveryRepository) ListOutbox(context.Context, string, string, string, int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return append([]integrationmodel.IntegrationOutboxMessage(nil), r.inserted...), nil
}

func (r *runtimeServicesDeliveryRepository) InsertInvocation(_ context.Context, _ string, invocation integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error) {
	if r.invocationErr != nil {
		return integrationmodel.IntegrationInvocation{}, r.invocationErr
	}
	invocation.ID = "invocation-1"
	r.invocations = append(r.invocations, invocation)
	return invocation, nil
}

func (r *runtimeServicesDeliveryRepository) UpdateInvocationStatus(_ context.Context, _, id, status string, duration int64, responseRef, errorText string) (integrationmodel.IntegrationInvocation, error) {
	for index := range r.invocations {
		if r.invocations[index].ID != id {
			continue
		}
		r.invocations[index].Status = status
		r.invocations[index].DurationMS = duration
		r.invocations[index].ResponseRef = responseRef
		r.invocations[index].Error = errorText
		return r.invocations[index], nil
	}
	return integrationmodel.IntegrationInvocation{}, nil
}

type runtimeServicesAutomationConfigRepository struct {
	integrationrepository.IntegrationConfigRepository
}

func (*runtimeServicesAutomationConfigRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return []integrationmodel.IntegrationConnection{}, nil
}

type runtimeServicesIntegrationConfigRepository struct {
	integrationrepository.IntegrationConfigRepository
	connections []integrationmodel.IntegrationConnection
}

func (r runtimeServicesIntegrationConfigRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return append([]integrationmodel.IntegrationConnection(nil), r.connections...), nil
}

func (*runtimeServicesIntegrationConfigRepository) ListExternalIdentities(context.Context, string) ([]integrationmodel.IntegrationExternalIdentity, error) {
	return []integrationmodel.IntegrationExternalIdentity{}, nil
}

func (r *runtimeServicesIntegrationConfigRepository) UpsertConnection(_ context.Context, _ string, connection integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	for index := range r.connections {
		if r.connections[index].Key == connection.Key {
			r.connections[index] = connection
			break
		}
	}
	return connection, nil
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

type runtimeServicesIntegrationEventRecords struct {
	page      recordmodel.RecordPageResult
	listErr   error
	createErr error
	created   []recordmodel.Record
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

type runtimeServicesIntegrationAgentRecords struct {
	createErr error
	updateErr error
	deleteErr error
}

func (r *runtimeServicesIntegrationAgentRecords) CreateRecord(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{ID: "created-1"}, r.createErr
}

func (r *runtimeServicesIntegrationAgentRecords) UpdateRecord(context.Context, string, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{ID: "updated-1"}, r.updateErr
}

func (r *runtimeServicesIntegrationAgentRecords) DeleteRecord(context.Context, string, string, principalmodel.Principal) error {
	return r.deleteErr
}

type runtimeServicesIntegrationWorkflowApplication struct {
	result workflowmodel.WorkflowRunResult
	err    error
}

func (r runtimeServicesIntegrationWorkflowApplication) RunIntegrationWorkflow(context.Context, string, map[string]any, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return r.result, r.err
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

func (r *runtimeServicesIntegrationEventRecords) ListRecords(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return r.page, r.listErr
}

func (r *runtimeServicesIntegrationEventRecords) CreateRecord(_ context.Context, _ string, data map[string]any, _ principalmodel.Principal) (recordmodel.Record, error) {
	if r.createErr != nil {
		return recordmodel.Record{}, r.createErr
	}
	record := recordmodel.Record{ID: "task-1", Data: data}
	r.created = append(r.created, record)
	return record, nil
}

func (r *runtimeServicesDeliveryRepository) InsertOutbox(_ context.Context, _ string, message integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.insertErr != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.insertErr
	}
	message.ID = "outbox-" + string(rune('1'+len(r.inserted)))
	r.inserted = append(r.inserted, message)
	return message, nil
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

func TestRuntimeInitializationSupportsOptionalSurfacePorts(t *testing.T) {
	repository := &compositionRecordRepository{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "record-1"}}, Total: 1}}
	directory := compositionIdentityDirectory{}
	services := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Dependencies: RuntimeServicesDependencies{
		Records: repository, IdentityDirectory: directory,
	}})

	page, err := listRuntimeSurfaceContextStoredRecords(t.Context(), services, "workspace-primary", definitionmodel.ObjectSchema{Key: "customer"}, recordmodel.RecordListQuery{Page: 1, PageSize: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "record-1" {
		t.Fatalf("stored records=%#v error=%v", page, err)
	}
	users, err := listRuntimeSurfaceContextDirectoryUsers(t.Context(), services)
	if err != nil || users != nil {
		t.Fatalf("users=%#v error=%v", users, err)
	}
	partial := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	if page, err = listRuntimeSurfaceContextStoredRecords(t.Context(), partial, "workspace-primary", definitionmodel.ObjectSchema{Key: "customer"}, recordmodel.RecordListQuery{}); err != nil || len(page.Items) != 0 {
		t.Fatalf("nil repository page=%#v error=%v", page, err)
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
	historyPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"automation.rule.history.read"}})
	emptyRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	if emptyRuntime.Applications().Automations == nil || emptyRuntime.Applications().Automations != emptyRuntime.automationApplicationService {
		t.Fatal("Runtime does not expose the canonical Automation application service")
	}
	if history, err := emptyRuntime.Applications().Automations.AutomationExecutions(t.Context(), automationmodel.AutomationExecutionFilter{}, historyPrincipal); err != nil || history.Count != 0 {
		t.Fatalf("empty Automation history=%#v error=%v", history, err)
	}
	delivery := &runtimeServicesDeliveryRepository{}
	config := &runtimeServicesAutomationConfigRepository{}
	automationPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "developer", Permissions: []string{"*"}})
	configuredRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest:     manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}},
		Dependencies: RuntimeServicesDependencies{IntegrationDelivery: delivery, IntegrationConfig: config, AgentPrincipals: agentPrincipalDirectoryStub{principal: automationPrincipal}},
	})
	if _, err := configuredRuntime.Applications().Automations.AutomationExecutions(t.Context(), automationmodel.AutomationExecutionFilter{}, historyPrincipal); err != nil {
		t.Fatalf("configured Automation history error=%v", err)
	}
	readPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"automation.rule.read"}})
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
	if _, err := configuredRuntime.Applications().Automations.ValidateAutomationRule(t.Context(), baseRule, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})); err != nil {
		t.Fatalf("Automation definition with configured Integration repository error=%v", err)
	}
	if _, err := emptyRuntime.Applications().Automations.ExecuteBeforeRule(t.Context(), automationmodel.AutomationRuleSchema{Key: "before", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before"}}, nil, nil, map[string]any{}, historyPrincipal); err != nil {
		t.Fatalf("before Automation execution error=%v", err)
	}

	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
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
	messageFor := func(ruleKey string) integrationmodel.IntegrationOutboxMessage {
		return integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-primary", Payload: automationbusiness.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{
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
	if _, err := (businessReferenceRuntimeAdapter{}).ListIntegrationOutboxMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("nil Business Reference Integration adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{records: &runtimeAssembly{}}).ListIntegrationOutboxMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("unassembled Business Reference Integration adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{records: emptyRuntime}).ListIntegrationOutboxMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("repository-free Business Reference Integration adapter error=%v", err)
	}
	if _, err := (businessReferenceRuntimeAdapter{records: configuredRuntime}).ListIntegrationOutboxMessages(t.Context(), "", "", 10, adminRuntimePrincipal); err != nil {
		t.Fatalf("configured Business Reference Integration adapter error=%v", err)
	}
}

func TestAuthoringCapabilityHelpersCoverAbsentSchemaAndLookupFallbacks(t *testing.T) {
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
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
	for _, resourceType := range []string{"action", "connector"} {
		issues, handled := appschemaapplication.ValidateStructuredApplicationDefinition(resourceType, json.RawMessage(`{`))
		if !handled || len(issues) != 1 {
			t.Fatalf("resource=%s issues=%#v handled=%t", resourceType, issues, handled)
		}
		if issues, handled = appschemaapplication.ValidateStructuredApplicationDefinition(resourceType, json.RawMessage(`{}`)); !handled || issues == nil {
			t.Fatalf("valid resource=%s issues=%#v handled=%t", resourceType, issues, handled)
		}
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
	validAction := `{"key":"customer.activate","object_key":"customer","kind":"record_operation","requires_permission":"customer.update","audit_event":"customer_activated"}`
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

/*
Integration owner orchestration moved to domainry-integration Module/SaaS.

	func TestIntegrationEventOrchestrationUsesNarrowApplicationPorts(t *testing.T) {
		registry := businessintegration.NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{
			{Key: "workflow", Provider: "workflow", TargetType: "workflow", WorkflowKey: "lead.follow_up"},
			{Key: "action", Provider: "action", TargetType: "action", ObjectKey: "lead", RecordID: "lead-1", ActionKey: "lead.convert"},
			{Key: "owner", Provider: "owner", TargetType: "owner_task"},
			{Key: "invalid", Provider: "invalid", TargetType: "unsupported"},
		}})
		records := &runtimeServicesIntegrationEventRecords{}
		resolvedPrincipal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "owner", WorkspaceID: "workspace-primary"}}
		resolveEventIdentity := func(context.Context, integrationmodel.IntegrationExternalIdentityResolveRequest, principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
			return integrationmodel.IntegrationExternalIdentityResolveResult{Mapped: true}, resolvedPrincipal, nil
		}
		var executeEventWorkflow businessintegration.IntegrationEventWorkflowExecutor
		var executeEventAction businessintegration.IntegrationEventActionExecutor
		var schemaObjectMap map[string]definitionmodel.ObjectSchema
		service := newIntegrationApplicationServiceWithDependencies(IntegrationRuntimeWiringDependencies{
			ConnectorRegistry: registry, EventRecords: records,
			SchemaObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema { return schemaObjectMap },
			EventIdentityResolver: func(ctx context.Context, request integrationmodel.IntegrationExternalIdentityResolveRequest, principal principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
				return resolveEventIdentity(ctx, request, principal)
			},
			EventWorkflowExecutor: func(ctx context.Context, key string, request integrationmodel.IntegrationEntrypointWorkflowRequest, principal principalmodel.Principal) (businessintegration.IntegrationWorkflowRunResult, error) {
				return executeEventWorkflow(ctx, key, request, principal)
			},
			EventActionExecutor: func(ctx context.Context, objectKey, recordID, actionKey string, request integrationmodel.IntegrationEntrypointActionRequest, principal principalmodel.Principal) (businessintegration.IntegrationActionExecutionResult, error) {
				return executeEventAction(ctx, objectKey, recordID, actionKey, request, principal)
			},
		})

		event := func(provider string) integrationmodel.IntegrationEvent {
			return integrationmodel.IntegrationEvent{ID: provider + "-1", Provider: provider, EventType: "updated", Payload: map[string]any{"subject": "Follow up"}}
		}
		if _, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("missing"), resolvedPrincipal); err != nil || handled {
			t.Fatalf("unmapped handled=%t error=%v", handled, err)
		}
		if _, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("invalid"), resolvedPrincipal); err == nil || !handled {
			t.Fatalf("invalid mapping handled=%t error=%v", handled, err)
		}

		executeEventWorkflow = func(context.Context, string, integrationmodel.IntegrationEntrypointWorkflowRequest, principalmodel.Principal) (businessintegration.IntegrationWorkflowRunResult, error) {
			return businessintegration.IntegrationWorkflowRunResult{}, errors.New("workflow failed")
		}
		if _, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("workflow"), resolvedPrincipal); err == nil || !handled {
			t.Fatalf("workflow failure handled=%t error=%v", handled, err)
		}
		executeEventWorkflow = func(context.Context, string, integrationmodel.IntegrationEntrypointWorkflowRequest, principalmodel.Principal) (businessintegration.IntegrationWorkflowRunResult, error) {
			return businessintegration.IntegrationWorkflowRunResult{Workflow: workflowmodel.WorkflowRunResult{WorkflowKey: "lead.follow_up", Status: "completed"}}, nil
		}
		if decision, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("workflow"), resolvedPrincipal); err != nil || !handled || decision.Status != "processed" {
			t.Fatalf("workflow decision=%#v handled=%t error=%v", decision, handled, err)
		}

		executeEventAction = func(context.Context, string, string, string, integrationmodel.IntegrationEntrypointActionRequest, principalmodel.Principal) (businessintegration.IntegrationActionExecutionResult, error) {
			return businessintegration.IntegrationActionExecutionResult{}, errors.New("action failed")
		}
		if _, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("action"), resolvedPrincipal); err == nil || !handled {
			t.Fatalf("action failure handled=%t error=%v", handled, err)
		}
		executeEventAction = func(context.Context, string, string, string, integrationmodel.IntegrationEntrypointActionRequest, principalmodel.Principal) (businessintegration.IntegrationActionExecutionResult, error) {
			return businessintegration.IntegrationActionExecutionResult{Action: actionmodel.ActionResult{ObjectKey: "lead", RecordID: "lead-1", ActionKey: "lead.convert"}}, nil
		}
		if decision, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("action"), resolvedPrincipal); err != nil || !handled || decision.Status != "processed" {
			t.Fatalf("action decision=%#v handled=%t error=%v", decision, handled, err)
		}

		resolveEventIdentity = func(context.Context, integrationmodel.IntegrationExternalIdentityResolveRequest, principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
			return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, errors.New("identity failed")
		}
		if _, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("owner"), resolvedPrincipal); err == nil || !handled {
			t.Fatalf("identity failure handled=%t error=%v", handled, err)
		}
		resolveEventIdentity = func(context.Context, integrationmodel.IntegrationExternalIdentityResolveRequest, principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
			return integrationmodel.IntegrationExternalIdentityResolveResult{Mapped: true}, resolvedPrincipal, nil
		}
		if _, _, err := service.ExecuteIntegrationEventMapping(t.Context(), event("owner"), resolvedPrincipal); err == nil {
			t.Fatal("missing Activity schema must reject owner task")
		}
		schemaObjectMap = map[string]definitionmodel.ObjectSchema{"activity": {Key: "activity"}}
		records.createErr = errors.New("create failed")
		if _, _, err := service.ExecuteIntegrationEventMapping(t.Context(), event("owner"), resolvedPrincipal); err == nil {
			t.Fatal("Record create failure must reject owner task")
		}
		records.createErr = nil
		if decision, handled, err := service.ExecuteIntegrationEventMapping(t.Context(), event("owner"), resolvedPrincipal); err != nil || !handled || decision.Status != "processed" || len(records.created) != 1 {
			t.Fatalf("owner decision=%#v handled=%t created=%d error=%v", decision, handled, len(records.created), err)
		}

		if _, ok := service.IntegrationFindFirstRecordByField(t.Context(), "contact", "email", " ", resolvedPrincipal); ok {
			t.Fatal("blank lookup value must not query Records")
		}
		records.listErr = errors.New("list failed")
		if _, ok := service.IntegrationFindFirstRecordByField(t.Context(), "contact", "email", "user@example.com", resolvedPrincipal); ok {
			t.Fatal("failed Record lookup must not resolve a relation")
		}
		records.listErr, records.page = nil, recordmodel.RecordPageResult{}
		if _, ok := service.IntegrationFindFirstRecordByField(t.Context(), "contact", "email", "user@example.com", resolvedPrincipal); ok {
			t.Fatal("empty Record lookup must not resolve a relation")
		}
		records.page = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "contact-1"}}}
		if record, ok := service.IntegrationFindFirstRecordByField(t.Context(), "contact", "email", "user@example.com", resolvedPrincipal); !ok || record.ID != "contact-1" {
			t.Fatalf("record=%#v resolved=%t", record, ok)
		}
	}

	func TestIntegrationApplicationWiringUsesOwnerPortsAndCanonicalService(t *testing.T) {
		if service := integrationApplication(nil); service == nil {
			t.Fatal("nil Runtime must produce an independently usable Integration application service")
		}
		withDefaults := newIntegrationApplicationServiceWithDependencies(IntegrationRuntimeWiringDependencies{})
		if withDefaults == nil {
			t.Fatal("default Integration dependencies were not installed")
		}
		providedAutomation := automationapplication.NewAutomationApplicationService(automationapplication.AutomationApplicationDependencies{})
		withProvidedAutomation := newIntegrationApplicationServiceWithDependencies(IntegrationRuntimeWiringDependencies{Automation: providedAutomation})
		if withProvidedAutomation == nil {
			t.Fatal("provided Automation application port was replaced")
		}

		registry := businessintegration.NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
			{Key: "email", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "smtp"}, {Key: "postmark"}}},
			{Key: "webhook", Provider: "webhook"},
		}})
		config := &runtimeServicesIntegrationConfigRepository{connections: []integrationmodel.IntegrationConnection{
			{Key: "smtp-main", WorkspaceID: "workspace-primary", ConnectorKey: "email", ProviderKey: "smtp"},
			{Key: "wrong-main", WorkspaceID: "workspace-primary", ConnectorKey: "webhook", ProviderKey: "webhook"},
		}}
		var schema appschemamodel.ApplicationSchemaSnapshot
		service := newIntegrationApplicationServiceWithDependencies(IntegrationRuntimeWiringDependencies{ConnectorRegistry: registry, ConfigRepository: config, Schema: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
			return schema
		}})
		principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}
		if references, err := service.IntegrationConnectionReferences(t.Context(), "smtp-main", principal); err != nil || len(references) != 0 {
			t.Fatalf("nil-schema references=%#v error=%v", references, err)
		}
		schema = appschemamodel.ApplicationSchemaSnapshot{
			Actions:         []definitionmodel.ActionSchema{{Key: "customer.notify"}},
			AutomationRules: []automationmodel.AutomationRuleSchema{{Key: "customer.created"}},
			Workflows:       []definitionmodel.WorkflowSchema{{Key: "customer.follow_up"}},
			Integrations:    integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "customer.event", Payload: map[string]any{"connection_key": "smtp-main"}}}},
		}
		if references, err := service.IntegrationConnectionReferences(t.Context(), "smtp-main", principal); err != nil || len(references) == 0 {
			t.Fatalf("schema references=%#v error=%v", references, err)
		}
		if _, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "missing", "", "smtp", "workspace-primary"); err == nil {
			t.Fatal("missing connector must be rejected")
		}
		if provider, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "email", "", "postmark", "workspace-primary"); err != nil || provider != "postmark" {
			t.Fatalf("requested provider=%q error=%v", provider, err)
		}
		if _, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "email", "absent", "", "workspace-primary"); err == nil {
			t.Fatal("missing connection must be rejected")
		}
		if _, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "email", "wrong-main", "", "workspace-primary"); err == nil {
			t.Fatal("connection connector mismatch must be rejected")
		}
		if provider, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "email", "smtp-main", "", "workspace-primary"); err != nil || provider != "smtp" {
			t.Fatalf("connection provider=%q error=%v", provider, err)
		}

		service.RegisterSharedIntegrationOutboxSenders()
		service.RegisterDefaultIntegrationOutboxSenders()
		canonicalRuntime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
		if canonical := integrationApplication(canonicalRuntime); canonical != canonicalRuntime.integrationService {
			t.Fatal("Integration constructor did not reuse the canonical Runtime service")
		}

		events := struct {
			integrationrepository.IntegrationEventRepository
		}{}
		delivery := &runtimeServicesDeliveryRepository{}
		worker := struct {
			integrationrepository.IntegrationWorkerRepository
		}{}
		configured := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Dependencies: RuntimeServicesDependencies{
			IntegrationConfig: config, IntegrationEvents: events, IntegrationDelivery: delivery, IntegrationWorker: worker,
		}})
		if configured.integrationConfigRepo != config || configured.integrationEventRepo != events || configured.integrationDeliveryRepo != delivery || configured.integrationWorkerRepo != worker {
			t.Fatal("constructor did not retain explicit Integration repositories")
		}
	}

	func TestIntegrationEntrypointsUseNarrowActionWorkflowAndRecordPorts(t *testing.T) {
		failure := errors.New("entrypoint owner failed")
		delivery := &runtimeServicesDeliveryRepository{}
		config := &runtimeServicesIntegrationConfigRepository{}
		registry := businessintegration.NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "email", Provider: "email"}}})
		records := &runtimeServicesIntegrationAgentRecords{}
		workflowPort := &runtimeServicesIntegrationWorkflowApplication{result: workflowmodel.WorkflowRunResult{WorkflowKey: "customer.follow_up", Status: "completed"}}
		var invokeAction func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
		var agents []agentsdk.AgentSchema
		service := newIntegrationApplicationServiceWithDependencies(IntegrationRuntimeWiringDependencies{
			ConfigRepository: config, DeliveryRepository: delivery, ConnectorRegistry: registry,
			Records: records, Workflows: workflowPort,
			InvokeAction: func(ctx context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
				return invokeAction(ctx, invocation)
			},
			Schema: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
				return appschemamodel.ApplicationSchemaSnapshot{Agents: agents, Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "email"}}}}
			},
		})
		admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
		adminWithRequest := admin
		adminWithRequest.RequestID = "request-1"
		validIdentity := integrationmodel.IntegrationExternalIdentityResolveRequest{Provider: "slack", ExternalSubject: "user-1", OnUnmapped: "read_only"}
		missingIdentity := integrationmodel.IntegrationExternalIdentityResolveRequest{Provider: "slack"}

		if _, err := service.ExecuteIntegrationAction(t.Context(), "customer", "customer-1", "customer.activate", integrationmodel.IntegrationEntrypointActionRequest{ExternalIdentity: missingIdentity}, admin); err == nil {
			t.Fatal("Action entrypoint identity failure must propagate")
		}
		invokeAction = func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			return actionmodel.ActionInvocationResult{}, failure
		}
		if _, err := service.ExecuteIntegrationAction(t.Context(), "customer", "customer-1", "customer.activate", integrationmodel.IntegrationEntrypointActionRequest{ExternalIdentity: validIdentity}, admin); !errors.Is(err, failure) {
			t.Fatalf("Action entrypoint error=%v", err)
		}
		invokeAction = func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			return actionmodel.ActionInvocationResult{Record: &actionmodel.ActionResult{ActionKey: "customer.activate", ObjectKey: "customer", RecordID: "customer-1"}}, nil
		}
		if result, err := service.ExecuteIntegrationAction(t.Context(), "customer", "customer-1", "customer.activate", integrationmodel.IntegrationEntrypointActionRequest{ExternalIdentity: validIdentity}, admin); err != nil || result.Action.ActionKey != "customer.activate" {
			t.Fatalf("Action result=%#v error=%v", result, err)
		}

		if _, err := service.RunIntegrationWorkflow(t.Context(), "customer.follow_up", integrationmodel.IntegrationEntrypointWorkflowRequest{ExternalIdentity: missingIdentity}, admin); err == nil {
			t.Fatal("Workflow entrypoint identity failure must propagate")
		}
		*workflowPort = runtimeServicesIntegrationWorkflowApplication{err: failure}
		if _, err := service.RunIntegrationWorkflow(t.Context(), "customer.follow_up", integrationmodel.IntegrationEntrypointWorkflowRequest{ExternalIdentity: validIdentity}, admin); !errors.Is(err, failure) {
			t.Fatalf("Workflow entrypoint error=%v", err)
		}
		*workflowPort = runtimeServicesIntegrationWorkflowApplication{result: workflowmodel.WorkflowRunResult{WorkflowKey: "customer.follow_up", Status: "completed"}}
		if result, err := service.RunIntegrationWorkflow(t.Context(), "customer.follow_up", integrationmodel.IntegrationEntrypointWorkflowRequest{ExternalIdentity: validIdentity}, admin); err != nil || result.Workflow.Status != "completed" {
			t.Fatalf("Workflow result=%#v error=%v", result, err)
		}

		allTools := []string{"readRecord", "createRecord", "updateRecord", "deleteRecord", "callConnector"}
		agents = []agentsdk.AgentSchema{{Key: "other", Tools: allTools}, {Key: "operations", Tools: allTools}}
		invoke := func(principal principalmodel.Principal, agentKey, toolKey string, request integrationmodel.IntegrationAgentToolInvocationRequest) (integrationmodel.IntegrationAgentToolInvocationResult, error) {
			request.ExternalIdentity = validIdentity
			return service.InvokeIntegrationAgentTool(t.Context(), agentKey, toolKey, request, principal)
		}
		if _, err := invoke(principalmodel.Principal{}, "operations", "readRecord", integrationmodel.IntegrationAgentToolInvocationRequest{}); err == nil {
			t.Fatal("unknown principal must not invoke Agent Tool")
		}
		if _, err := invoke(admin, "", "readRecord", integrationmodel.IntegrationAgentToolInvocationRequest{}); err == nil {
			t.Fatal("blank Agent key must be rejected")
		}
		if _, err := invoke(admin, "operations", "", integrationmodel.IntegrationAgentToolInvocationRequest{}); err == nil {
			t.Fatal("blank Tool key must be rejected")
		}
		requestWithMissingIdentity := integrationmodel.IntegrationAgentToolInvocationRequest{ExternalIdentity: missingIdentity}
		if _, err := service.InvokeIntegrationAgentTool(t.Context(), "operations", "readRecord", requestWithMissingIdentity, admin); err == nil {
			t.Fatal("Agent Tool identity failure must propagate")
		}
		if _, err := invoke(admin, "missing", "readRecord", integrationmodel.IntegrationAgentToolInvocationRequest{}); err == nil {
			t.Fatal("missing Agent must be rejected")
		}
		agents = []agentsdk.AgentSchema{{Key: "operations", Tools: []string{"readRecord"}}}
		if _, err := invoke(admin, "operations", "deleteRecord", integrationmodel.IntegrationAgentToolInvocationRequest{}); err == nil {
			t.Fatal("disallowed Tool must be rejected")
		}
		agents = []agentsdk.AgentSchema{{Key: "operations", Tools: allTools}}
		if _, err := invoke(admin, "operations", "callConnector", integrationmodel.IntegrationAgentToolInvocationRequest{}); err == nil {
			t.Fatal("Connector Tool without connector must fail risk assessment")
		}

		if result, err := invoke(admin, "operations", "readRecord", integrationmodel.IntegrationAgentToolInvocationRequest{}); err != nil || result.Status != "prepared" || result.ApprovalPlan != nil {
			t.Fatalf("read Tool result=%#v error=%v", result, err)
		}
		if result, err := invoke(adminWithRequest, "operations", "createRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Input: map[string]any{"object_key": "customer", "data": map[string]any{"name": "Acme"}}}); err != nil || result.Status != "approval_required" || result.ApprovalPlan == nil {
			t.Fatalf("approval result=%#v error=%v", result, err)
		}
		if result, err := invoke(admin, "operations", "callConnector", integrationmodel.IntegrationAgentToolInvocationRequest{Input: map[string]any{"connector_key": "email"}}); err != nil || result.Status != "approval_required" || result.ActionInvocation.Metadata["connector_key"] != "email" {
			t.Fatalf("connector approval result=%#v error=%v", result, err)
		}
		if result, err := invoke(admin, "operations", "createRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Approved: true, Input: map[string]any{"object_key": "customer", "data": map[string]any{"name": "Acme"}}}); err != nil || result.Status != "executed" {
			t.Fatalf("create result=%#v error=%v", result, err)
		}
		records.createErr = failure
		if _, err := invoke(admin, "operations", "createRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Approved: true, Input: map[string]any{"object_key": "customer"}}); !errors.Is(err, failure) {
			t.Fatalf("create error=%v", err)
		}
		records.createErr = nil
		if result, err := invoke(admin, "operations", "updateRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Approved: true, Input: map[string]any{"object_key": "customer", "record_id": "customer-1"}}); err != nil || result.Status != "executed" {
			t.Fatalf("update result=%#v error=%v", result, err)
		}
		records.updateErr = failure
		if _, err := invoke(admin, "operations", "updateRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Approved: true, Input: map[string]any{"object_key": "customer", "record_id": "customer-1"}}); !errors.Is(err, failure) {
			t.Fatalf("update error=%v", err)
		}
		records.updateErr = nil
		if result, err := invoke(admin, "operations", "deleteRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Approved: true, Input: map[string]any{"object_key": "customer", "record_id": "customer-1"}}); err != nil || result.Status != "executed" {
			t.Fatalf("delete result=%#v error=%v", result, err)
		}
		records.deleteErr = failure
		if _, err := invoke(admin, "operations", "deleteRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Approved: true, Input: map[string]any{"object_key": "customer", "record_id": "customer-1"}}); !errors.Is(err, failure) {
			t.Fatalf("delete error=%v", err)
		}
		records.deleteErr = nil
		if result, err := invoke(admin, "operations", "readRecord", integrationmodel.IntegrationAgentToolInvocationRequest{Approved: true}); err != nil || result.Status != "prepared" {
			t.Fatalf("no-op Tool result=%#v error=%v", result, err)
		}
		delivery.invocationErr = failure
		if _, err := invoke(admin, "operations", "readRecord", integrationmodel.IntegrationAgentToolInvocationRequest{}); !errors.Is(err, failure) {
			t.Fatalf("invocation persistence error=%v", err)
		}
	}
*/
func TestBusinessRuntimeProjectionPropagatesOwnerFailures(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}
	failure := errors.New("owner read failed")
	portsFor := func(failAt string) businesssystemapplication.BusinessSystemRuntimeProjectionDependencies {
		objects := map[string]definitionmodel.ObjectSchema{
			"job_run":         {Key: "job_run"},
			"job_dead_letter": {Key: "job_dead_letter"},
		}
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
			ConnectorCatalog: func(context.Context, principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error) {
				if failAt == "connectors" {
					return nil, failure
				}
				return nil, nil
			},
			IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error) {
				if failAt == "connections" {
					return nil, failure
				}
				return nil, nil
			},
			IntegrationOutbox: func(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
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
			SchemaObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema { return objects },
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
	if err != nil || len(snapshot.Scheduler.Definitions) != 1 || len(snapshot.Scheduler.RecentRuns) != 0 || len(snapshot.Scheduler.DeadLetters) != 0 || len(snapshot.Reports) != 1 {
		t.Fatalf("snapshot=%#v error=%v", snapshot, err)
	}

	missing := portsFor("")
	missing.SchemaObjectMap = func(context.Context) map[string]definitionmodel.ObjectSchema { return nil }
	service = businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{Runtime: missing})
	if records, err := service.SnapshotObjectRecords(t.Context(), "missing", principal, 10); err != nil || records == nil || len(records) != 0 {
		t.Fatalf("missing records=%#v error=%v", records, err)
	}
}

func TestBusinessSystemSnapshotUsesNarrowOwnerPorts(t *testing.T) {
	failure := errors.New("owner projection failed")
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
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
			ApplicationDefinitions: func(_ context.Context, resourceType, _ string, _ principalmodel.Principal) ([]appschemamodel.ApplicationDefinition, error) {
				if failAt == "metadata" {
					return nil, failure
				}
				if resourceType != "action" {
					return nil, nil
				}
				return []appschemamodel.ApplicationDefinition{{ResourceType: resourceType, ResourceKey: "customer.activate", SourceKind: "manifest"}}, nil
			},
			Evidence: evidence,
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
				ConnectorCatalog: func(context.Context, principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error) {
					return nil, nil
				},
				IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error) {
					return nil, nil
				},
				IntegrationOutbox: func(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
					return nil, nil
				},
				SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
					return schema
				},
				SchemaObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema { return nil },
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
	runtime.applyManifestMetadata(" template ", " 1 ", " Runtime ", objects, actions, workflows, rules, nil, integrationmodel.IntegrationSchema{}, nil, nil, nil, nil)
	if runtime.templateID != "template" || runtime.templateVersion != "1" || runtime.name != "Runtime" || runtime.connectorRegistry == nil {
		t.Fatalf("runtime identity=%q/%q/%q connector=%#v", runtime.templateID, runtime.templateVersion, runtime.name, runtime.connectorRegistry)
	}
	if len(runtime.schema) != 1 || len(runtime.actions) != 1 || len(runtime.workflows) != 1 || len(runtime.automationRules) != 1 {
		t.Fatalf("projection objects=%d actions=%d workflows=%d rules=%d", len(runtime.schema), len(runtime.actions), len(runtime.workflows), len(runtime.automationRules))
	}
	runtime.dictionaryRuntime = appschemaservice.NewApplicationSchemaDictionaryDomainService(nil)
	runtime.applyManifestMetadata("template", "2", "Runtime", objects[1:], actions[1:], workflows[1:], rules[1:], []appschemamodel.DictionarySchema{{Key: "status"}}, integrationmodel.IntegrationSchema{}, nil, nil, nil, nil)
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
	baseAction := definitionmodel.ActionSchema{Key: "customer.activate", ObjectKey: "customer", Kind: "record_update", RequiresPermission: "customer.update", AuditEvent: "customer_activated"}
	baseRole := accessfixture.Bundle{
		Key: "operator", Permissions: []string{"customer.*"}, RecordScope: "all_records",
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
		return service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: actionKey, ObjectKey: objectKey, RecordID: "customer-1", Principal: principal})
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
	conditionalRepository := newRepository()
	conditionalRuntime, conditionalPrincipal := fixture(baseObject, conditionalAction, baseRole, conditionalRepository)
	conditionalResult, err := conditionalRuntime.Applications().Actions.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: conditionalAction.Key, ObjectKey: conditionalAction.ObjectKey, RecordID: baseRecord.ID,
		Input: map[string]any{"status": "active", "expected_version": 1}, Principal: conditionalPrincipal,
	})
	if err != nil || conditionalResult.Record == nil || conditionalRepository.record.Data["status"] != "active" {
		t.Fatalf("conditional System Operation result=%+v record=%+v error=%v", conditionalResult, conditionalRepository.record, err)
	}
	if _, leaked := conditionalRepository.record.Data["expected_version"]; leaked {
		t.Fatalf("conditional control field leaked into record patch: %+v", conditionalRepository.record.Data)
	}

	conflictRepository := newRepository()
	conflictRuntime, conflictPrincipal := fixture(baseObject, conditionalAction, baseRole, conflictRepository)
	_, err = conflictRuntime.Applications().Actions.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: conditionalAction.Key, ObjectKey: conditionalAction.ObjectKey, RecordID: baseRecord.ID,
		Input: map[string]any{"status": "active", "expected_version": 99}, Principal: conflictPrincipal,
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
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
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
	if service.reportQueriesService == nil || service.reportSnapshotsService == nil || service.reportExportsService == nil {
		t.Fatal("Report capability application services were not assembled")
	}
	if service.surfaceContextService == nil {
		t.Fatal("Surface Context owner service was not assembled")
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
