package composition

import (
	"context"
	"testing"
	"time"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type compositionRecordPolicyProbe struct {
	object         definitionmodel.ObjectSchema
	query          recordmodel.RecordListQuery
	access         bool
	write          bool
	objectCalls    int
	reportCalls    int
	normalizeCalls int
	accessCalls    int
	writeCalls     int
}

func (p *compositionRecordPolicyProbe) objectForAction(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	p.objectCalls++
	return p.object, nil
}

func (p *compositionRecordPolicyProbe) ensureReportSnapshotAccess(definitionmodel.ObjectSchema, string, principalmodel.Principal) error {
	p.reportCalls++
	return nil
}

func (p *compositionRecordPolicyProbe) normalizeListQuery(definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery {
	p.normalizeCalls++
	return p.query
}

func (p *compositionRecordPolicyProbe) canAccessRecord(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	p.accessCalls++
	return p.access
}

func (p *compositionRecordPolicyProbe) canAccessPersistedRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error) {
	p.accessCalls++
	return p.access, nil
}

func (p *compositionRecordPolicyProbe) canWriteRecordScope(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool {
	p.writeCalls++
	return p.write
}

func TestRecordPolicyWiringDelegatesEveryPort(t *testing.T) {
	probe := &compositionRecordPolicyProbe{
		object: definitionmodel.ObjectSchema{Key: "customer"},
		query:  recordmodel.RecordListQuery{Page: 2, PageSize: 7},
		access: true,
		write:  true,
	}
	read := recordReadPolicyAdapter{policy: probe}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}
	object, err := read.ObjectForAction(principal, "customer", "read")
	if err != nil || object.Key != "customer" {
		t.Fatalf("object=%#v error=%v", object, err)
	}
	if err := read.EnsureReportSnapshotAccess(object, "read", principal); err != nil {
		t.Fatalf("report access error=%v", err)
	}
	if query := read.NormalizeListQuery(object, recordmodel.RecordListQuery{}, principal); query.Page != 2 || query.PageSize != 7 {
		t.Fatalf("normalized query=%#v", query)
	}
	if !read.CanAccessRecord(principal, object, recordmodel.Record{ID: "customer-1"}) {
		t.Fatal("record access result was not delegated")
	}
	if probe.objectCalls != 1 || probe.reportCalls != 1 || probe.normalizeCalls != 1 || probe.accessCalls != 1 {
		t.Fatalf("read adapter calls=%#v", probe)
	}

	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}},
	}})
	queryPolicy := recordQueryPolicyAdapter{service: runtime.RecordQueryPolicyDomainService}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	if got := queryPolicy.normalizeListQuery(object, recordmodel.RecordListQuery{Page: 1}, admin); got.Page != 1 {
		t.Fatalf("query adapter result=%#v", got)
	}
	if !queryPolicy.canAccessRecord(admin, object, recordmodel.Record{ID: "customer-1"}) {
		t.Fatal("admin must access record through query adapter")
	}
	if !queryPolicy.canWriteRecordScope(admin, object, map[string]any{"name": "Ada"}) {
		t.Fatal("admin must write record through query adapter")
	}
	if resolved, err := queryPolicy.objectForAction(admin, " customer ", "read"); err != nil || resolved.Key != "customer" {
		t.Fatalf("query policy object=%#v error=%v", resolved, err)
	}
	if err := queryPolicy.ensureReportSnapshotAccess(object, "read", admin); err != nil {
		t.Fatalf("query policy report access error=%v", err)
	}

	mutation := recordMutationPolicyAdapter{pipeline: runtime.PipelineApplicationService, validation: runtime.RecordValidationDomainService}
	data := map[string]any{"name": "Ada"}
	_ = mutation.validatePipelineDefaults(t.Context(), object, "customer-1", data, admin)
	_ = mutation.applyPipelineItemDefaults(t.Context(), object, data, admin, false)
	_ = mutation.validateRelationReferences(t.Context(), object, data, admin)
	_ = mutation.validateDomainPolicies(t.Context(), object, nil, data, "customer-1", "update", admin)
	_ = mutation.validateUnique(t.Context(), "workspace-primary", "customer", object, "customer-1", data)
	_ = mutation.validateDuplicateIdentity(t.Context(), "workspace-primary", object, "customer-1", data)
}

func TestWorkflowDependencyWiringCoversCancellationLookupAndMissingOwner(t *testing.T) {
	if dependencies := workflowDependencies(nil); dependencies.ObjectMap != nil {
		t.Fatalf("nil runtime dependencies=%#v", dependencies)
	}

	partial := &runtimeAssembly{}
	if objects := workflowDependencies(partial).ObjectMap(t.Context()); len(objects) != 0 {
		t.Fatalf("partial runtime objects=%#v", objects)
	}

	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}},
		Actions: []definitionmodel.ActionSchema{{Key: "customer.activate", ObjectKey: "customer", Kind: "record"}},
	}})
	dependencies := workflowDependencies(runtime)
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})

	if objects := dependencies.ObjectMap(t.Context()); objects["customer"].Key != "customer" {
		t.Fatalf("workflow object map=%#v", objects)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if objects := dependencies.ObjectMap(canceled); len(objects) != 0 {
		t.Fatalf("canceled workflow object map=%#v", objects)
	}
	if _, err := dependencies.ObjectForAction(canceled, admin, "customer", "read"); err == nil {
		t.Fatal("canceled object lookup must fail")
	}
	if object, err := dependencies.ObjectForAction(t.Context(), admin, "customer", "read"); err != nil || object.Key != "customer" {
		t.Fatalf("workflow object=%#v error=%v", object, err)
	}
	if dependencies.CanAccessRecord(canceled, admin, definitionmodel.ObjectSchema{Key: "customer"}, recordmodel.Record{ID: "customer-1"}) {
		t.Fatal("canceled record access must fail")
	}
	if !dependencies.CanAccessRecord(t.Context(), admin, definitionmodel.ObjectSchema{Key: "customer"}, recordmodel.Record{ID: "customer-1"}) {
		t.Fatal("admin record access must succeed")
	}
	if dependencies.ActionExists(canceled, "customer.activate") {
		t.Fatal("canceled action lookup must fail")
	}
	if !dependencies.ActionExists(t.Context(), " customer.activate ") || dependencies.ActionExists(t.Context(), "missing") {
		t.Fatal("workflow action lookup did not normalize or distinguish absence")
	}

	service := runtime.actionService
	runtime.actionService = nil
	if _, err := dependencies.InvokeAction(t.Context(), workflowapplication.WorkflowBusinessActionInvocation{ActionKey: "customer.activate"}); err == nil {
		t.Fatal("missing Action owner must fail")
	}
	runtime.actionService = service
	if _, err := dependencies.InvokeAction(t.Context(), workflowapplication.WorkflowBusinessActionInvocation{ActionKey: "missing", Principal: admin}); err == nil {
		t.Fatal("configured Action owner must receive invocation")
	}
}

func TestRuntimeFacadeNilContracts(t *testing.T) {
	var services *RuntimeServices
	if services.Applications().Scheduler == nil {
		t.Fatal("nil facade must expose stateless Scheduler validation")
	}
	if schema := services.Schema(); len(schema.Objects) != 0 {
		t.Fatalf("nil facade schema=%#v", schema)
	}
	if schema := services.SchemaForPrincipal(t.Context(), principalmodel.Principal{}); len(schema.Objects) != 0 {
		t.Fatalf("nil facade principal schema=%#v", schema)
	}

	partial := &RuntimeServices{}
	if partial.Applications().Scheduler != nil || len(partial.Schema().Objects) != 0 || len(partial.SchemaForPrincipal(t.Context(), principalmodel.Principal{}).Objects) != 0 {
		t.Fatal("partial facade must preserve its empty immutable state")
	}
}

func TestRuntimeConstructionRejectsNilContext(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil construction context must panic")
		}
	}()
	_ = NewRuntimeServices(nil, RuntimeServicesConfig{})
}

func TestRecordApplicationDependencyClosuresUseCanonicalOwners(t *testing.T) {
	dynamicPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "persisted-user", AuthorizationRevision: "identity-revision-7"}}, accessfixture.Bundle{Key: "persisted-role", RecordScope: "all_records"})
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{
		Objects:                   []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}},
		IdentityProfileExtensions: []profilebindingmodel.Binding{{ObjectKey: "customer", IdentityRelationField: "owner"}},
	}, Dependencies: RuntimeServicesDependencies{AgentPrincipals: agentPrincipalDirectoryStub{principal: dynamicPrincipal}}})
	dependencies := buildRecordApplicationDependencies(runtime)
	principal := dependencies.ResolveBatchPrincipal(t.Context(), "persisted-user", "persisted-role")
	if !principal.Known || principal.AuthorizationRevision != dynamicPrincipal.AuthorizationRevision || principal.RoleKey != dynamicPrincipal.RoleKey {
		t.Fatalf("batch principal did not use persisted Identity directory: %#v", principal)
	}
	if objects := dependencies.SchemaMap(); objects["customer"].Key != "customer" {
		t.Fatalf("dependency schema map=%#v", objects)
	}
	extensions := dependencies.IdentityProfileExtensions()
	if len(extensions) != 1 || extensions[0].ObjectKey != "customer" {
		t.Fatalf("identity profile extensions=%#v", extensions)
	}
	extensions[0].ObjectKey = "changed"
	if got := dependencies.IdentityProfileExtensions()[0].ObjectKey; got != "customer" {
		t.Fatalf("identity profile extensions leaked caller mutation: %q", got)
	}
	if err := dependencies.UpdateInternal(t.Context(), "workspace-primary", definitionmodel.ObjectSchema{Key: "customer"}, recordmodel.Record{ID: "customer-1"}, "test"); err == nil {
		t.Fatal("missing record repository must reject internal update")
	}
}

func TestRecordDomainWiringInvokesSchemaWorkflowAndPipelinePorts(t *testing.T) {
	order := definitionmodel.ObjectSchema{
		Key:    "order",
		Fields: []definitionmodel.FieldSchema{{Key: "activity", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "activity"}}},
		Validations: []definitionmodel.ValidationSchema{{Type: "state_machine", FieldKey: "status", Config: map[string]any{
			"transitions": []any{map[string]any{
				"from": "draft", "to": "approved", "self_patch": map[string]any{"reviewed": true},
			}},
		}}},
	}
	activity := definitionmodel.ObjectSchema{Key: "activity", Name: "Activity"}
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{
		Objects:   []definitionmodel.ObjectSchema{order, activity},
		Workflows: []definitionmodel.WorkflowSchema{{Key: "order.follow_up", Name: "Follow up"}},
	}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})

	repository := &pipelineFailureRepository{records: map[string]map[string]recordmodel.Record{
		"activity": {"activity-1": {ID: "activity-1", Data: map[string]any{"subject": "existing"}}},
	}}
	validation := newRecordValidationService(runtime, repository, recordQueryPolicyAdapter{service: runtime.RecordQueryPolicyDomainService}, nil, nil)
	if err := validation.ValidateRelations(t.Context(), order, map[string]any{"activity": "activity-1"}, principal); err != nil {
		t.Fatalf("relation validation error=%v", err)
	}

	effects := newRecordStateMachineEffects()
	next := map[string]any{"status": "approved"}
	if changed, err := effects.ApplySelfEffects(t.Context(), order, map[string]any{"status": "draft"}, next, "order-1", principal); err != nil || !changed || next["reviewed"] != true {
		t.Fatalf("self effects changed=%t next=%#v error=%v", changed, next, err)
	}

	pipelineRuntime, _, _, pipelineRecord, pipelinePrincipal := pipelineFailureFixture(t, 0, false, false)
	preconditions := newActionPreconditionService(pipelineRuntime.PipelineApplicationService)
	if err := preconditions.Check(t.Context(), definitionmodel.ActionSchema{Preconditions: []string{"current stage is terminal"}}, pipelineRecord, pipelinePrincipal); err == nil {
		t.Fatal("non-terminal pipeline stage must reject the action")
	}
}

func TestWorkflowSchemaWiringHandlesOptionalIdentityDirectory(t *testing.T) {
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{Manifest: manifestmodel.ManifestSchema{
		Actions:                []definitionmodel.ActionSchema{{Key: "customer.activate", ObjectKey: "customer", Kind: "record"}},
		AgentServicePrincipals: []agentsdk.AgentServicePrincipalBinding{{Key: "review-service", Enabled: true}},
	}, Dependencies: RuntimeServicesDependencies{IdentityDirectory: compositionIdentityDirectory{}}})
	provider := runtimeWorkflowSchemaProvider{records: runtime}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if snapshot := provider.WorkflowSchemaSnapshot(canceled, principalmodel.Principal{}); len(snapshot.Actions) != 0 {
		t.Fatalf("canceled workflow snapshot=%#v", snapshot)
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	if snapshot := provider.WorkflowSchemaSnapshot(t.Context(), admin); len(snapshot.Actions) != 1 {
		t.Fatalf("workflow snapshot=%#v", snapshot)
	}
	runtime.actionService.ReplaceDefinitions([]definitionmodel.ActionSchema{{
		Key: "customer.activate", ObjectKey: "customer", Kind: "record",
		EffectSet: &definitionmodel.ActionEffectSet{
			Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "customer"}, {ObjectKey: "account"}},
		},
	}})
	snapshot := provider.WorkflowSchemaSnapshot(t.Context(), principalmodel.Principal{})
	if len(snapshot.Actions) != 1 || snapshot.Actions[0].EffectSet == nil || len(snapshot.Actions[0].EffectSet.Write) != 2 {
		t.Fatalf("workflow schema did not use executable Action contract: %#v", snapshot.Actions)
	}
	if len(snapshot.AgentServicePrincipals) != 1 || snapshot.AgentServicePrincipals[0].Key != "review-service" {
		t.Fatalf("internal workflow schema hid service principals: %#v", snapshot.AgentServicePrincipals)
	}
	registry := runtime.connectorRegistry
	runtime.connectorRegistry = nil
	if provider.ConnectorAdapterExists(t.Context(), "missing") || provider.ConnectorAdapterExists(canceled, "missing") {
		t.Fatal("connector lookup must reject missing registry and canceled context")
	}
	runtime.connectorRegistry = registry
	if provider.ConnectorAdapterExists(t.Context(), "missing") {
		t.Fatal("unknown connector must not report a ready adapter")
	}
}

func TestWorkflowSchedulerTimerModeAndFailureEdges(t *testing.T) {
	repository := &pipelineFailureRepository{records: map[string]map[string]recordmodel.Record{}}
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest:     manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "record_timer", Name: "Record timer"}}},
		Dependencies: RuntimeServicesDependencies{Records: repository},
	})
	scheduler := runtimeWorkflowScheduler{recordTimers: runtime.recordTimerService}
	createdAt := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	request := workflowapplication.WorkflowWaitTimerRequest{
		WorkspaceID: "workspace-primary", ProcessID: "process", NodeID: "timer", ObjectKey: "order", RecordID: "order-1",
		CreatedAt: createdAt, Variables: map[string]any{"starts_at": createdAt.Format(time.RFC3339Nano)},
	}

	request.Contract = definitionmodel.WorkflowTimerNodeContract{DurationSeconds: 60}
	if timerID, err := scheduler.ScheduleWorkflowWaitTimer(t.Context(), request); err != nil || timerID == "" {
		t.Fatalf("default timer=%q error=%v", timerID, err)
	}
	request.Contract = definitionmodel.WorkflowTimerNodeContract{
		TimerKey: "business-at", Purpose: "business", At: createdAt.Format(time.RFC3339Nano),
		DurationSeconds: 10, BusinessCalendarKey: "weekday", OffsetSeconds: 60, Timezone: "UTC",
	}
	if timerID, err := scheduler.ScheduleWorkflowWaitTimer(t.Context(), request); err != nil || timerID == "" {
		t.Fatalf("business-at timer=%q error=%v", timerID, err)
	}
	request.Contract = definitionmodel.WorkflowTimerNodeContract{
		TimerKey: "business-base", Purpose: "business", BusinessCalendarKey: "weekday", OffsetSeconds: 60, Timezone: "UTC",
	}
	if timerID, err := scheduler.ScheduleWorkflowWaitTimer(t.Context(), request); err != nil || timerID == "" {
		t.Fatalf("business-base timer=%q error=%v", timerID, err)
	}
	request.Contract = definitionmodel.WorkflowTimerNodeContract{
		TimerKey: "relative", Purpose: "relative", SourceField: "starts_at", OffsetSeconds: 60, Timezone: "UTC",
	}
	request.Variables = map[string]any{"after": map[string]any{"starts_at": createdAt.Format(time.RFC3339Nano)}}
	if timerID, err := scheduler.ScheduleWorkflowWaitTimer(t.Context(), request); err != nil || timerID == "" {
		t.Fatalf("relative timer=%q error=%v", timerID, err)
	}
	request.Contract = definitionmodel.WorkflowTimerNodeContract{
		TimerKey: "absolute", Purpose: "absolute", At: createdAt.Add(time.Hour).Format(time.RFC3339Nano), Timezone: "UTC",
	}
	if timerID, err := scheduler.ScheduleWorkflowWaitTimer(t.Context(), request); err != nil || timerID == "" {
		t.Fatalf("absolute timer=%q error=%v", timerID, err)
	}
	repository.failCommitAt = repository.commitCount + 1
	request.Contract.TimerKey = "absolute-failure"
	if _, err := scheduler.ScheduleWorkflowWaitTimer(t.Context(), request); err == nil {
		t.Fatal("timer persistence failure was ignored")
	}
	repository.failCommitAt = 0

	if _, err := scheduler.ScheduleWorkflowApprovalDeadlineTimer(t.Context(), workflowapplication.WorkflowApprovalDeadlineTimerRequest{
		WorkspaceID: "workspace-primary", ProcessID: "process", NodeID: "approval", TaskID: "task-invalid",
		Phase: "invalid", DueAt: createdAt.Add(time.Hour), CreatedAt: createdAt,
	}); err == nil {
		t.Fatal("invalid approval deadline phase accepted")
	}
	if timerID, err := scheduler.ScheduleWorkflowApprovalDeadlineTimer(t.Context(), workflowapplication.WorkflowApprovalDeadlineTimerRequest{
		WorkspaceID: "workspace-primary", ProcessID: "process", NodeID: "approval", TaskID: "task-reminder",
		Phase: "reminder", DueAt: createdAt.Add(time.Hour), CreatedAt: createdAt,
	}); err != nil || timerID == "" {
		t.Fatalf("reminder timer=%q error=%v", timerID, err)
	}
	repository.failCommitAt = repository.commitCount + 1
	if _, err := scheduler.ScheduleWorkflowApprovalDeadlineTimer(t.Context(), workflowapplication.WorkflowApprovalDeadlineTimerRequest{
		WorkspaceID: "workspace-primary", ProcessID: "process", NodeID: "approval", TaskID: "task-escalation",
		Phase: "escalation", DueAt: createdAt.Add(2 * time.Hour), CreatedAt: createdAt,
	}); err == nil {
		t.Fatal("approval timer persistence failure was ignored")
	}
}

func TestOptionalMetadataAndIntegrationCompositionFallbacks(t *testing.T) {
	foundation := &runtimeAssembly{}
	initializeSchemaAndRecordFoundation(foundation, RuntimeServicesDependencies{})
	if foundation.auditApplicationService == nil {
		t.Fatal("schema foundation did not create fallback audit owner")
	}
	if assembleApplicationSchema(nil) == nil {
		t.Fatal("nil runtime must still produce a metadata application owner")
	}
	if publicationHandoffApplication(nil) == nil {
		t.Fatal("nil runtime must still produce an integration application owner")
	}
	runtime := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{})
	if got := assembleApplicationSchema(runtime); got != runtime.applicationSchemaService {
		t.Fatal("metadata composition did not reuse the canonical owner")
	}
	if got := publicationHandoffApplication(runtime); got == nil {
		t.Fatal("publication handoff composition was not assembled")
	}
	if users, err := listRuntimeSurfaceContextDirectoryUsers(t.Context(), &runtimeAssembly{}); err != nil || users != nil {
		t.Fatalf("missing surface identity directory users=%#v error=%v", users, err)
	}
}

func TestPipelineCompositionCoversOptionalOwnerBoundaries(t *testing.T) {
	runtime, repository, object, record, principal := pipelineFailureFixture(t, 0, false, false)
	pipelineWithoutAccess := newPipelineApplicationService(runtime, repository, nil)
	if err := pipelineWithoutAccess.ValidateDefaults(t.Context(), definitionmodel.ObjectSchema{Key: "pipeline"}, "pipeline_1", map[string]any{"default_stage": "stage_old"}, principal); err != nil {
		t.Fatalf("pipeline default without access policy error=%v", err)
	}

	originalRepository := runtime.recordRepo
	runtime.recordRepo = nil
	transition := newPipelineTransitionApplicationService(runtime)
	if _, err := transition.Execute(t.Context(), object.Key, record.ID, pipelineAction(), map[string]any{"to_stage": "stage_new", "expected_version": 1}, principal); err == nil {
		t.Fatal("pipeline transition must not find a record without its repository")
	}
	runtime.recordRepo = originalRepository

	current := repository.records[object.Key][record.ID]
	originalEffects := runtime.recordStateMachineEffects
	runtime.recordStateMachineEffects = nil
	transition = newPipelineTransitionApplicationService(runtime)
	if _, err := transition.Persist(t.Context(), object, current, current.Data, pipelineAction(), nil, "stage_new", "stage_old", principal); err != nil {
		t.Fatalf("pipeline transition without optional post-commit owners error=%v", err)
	}
	runtime.recordStateMachineEffects = originalEffects

}
