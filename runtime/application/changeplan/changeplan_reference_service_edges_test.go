package changeplan

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type changePlanReferenceRuntimeFake struct {
	processes    []workflowmodel.WorkflowProcessInstance
	processErr   error
	records      map[string][]recordmodel.Record
	recordErrors map[string]error
	messages     []integrationmodel.IntegrationOutboxMessage
	messageErr   error
}

func (f *changePlanReferenceRuntimeFake) WorkflowProcesses(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	return f.processes, f.processErr
}

func (f *changePlanReferenceRuntimeFake) PublishedSchedulerDefinitions(context.Context, principalmodel.Principal) ([]recordmodel.Record, error) {
	return append([]recordmodel.Record(nil), f.records["scheduler"]...), f.recordErrors["scheduler"]
}

func (f *changePlanReferenceRuntimeFake) SnapshotObjectRecords(_ context.Context, objectKey string, _ principalmodel.Principal, _ int) ([]recordmodel.Record, error) {
	return f.records[objectKey], f.recordErrors[objectKey]
}

func (f *changePlanReferenceRuntimeFake) ListIntegrationOutboxMessages(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return f.messages, f.messageErr
}

type changePlanFrontendFake struct {
	snapshot changeplanmodel.FrontendCapabilities
	err      error
}

func (f changePlanFrontendFake) FrontendReferenceSnapshot(context.Context, principalmodel.Principal) (changeplanmodel.FrontendCapabilities, error) {
	return f.snapshot, f.err
}

type changePlanEvidenceFake struct {
	seeds []businessseedmodel.BusinessSeedProvenance
	err   error
}

func (f changePlanEvidenceFake) ListSeedProvenance(context.Context) ([]businessseedmodel.BusinessSeedProvenance, error) {
	return f.seeds, f.err
}

func TestChangePlanReferenceServiceAuthorizationAndDependencyErrors(t *testing.T) {
	service := NewChangePlanReferenceApplicationService(nil, nil, nil, nil)
	if _, err := service.Graph(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("scope error = %v", err)
	}
	if _, err := service.Graph(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error = %v", err)
	}
	wantErr := errors.New("dependency failed")
	service.frontend = changePlanFrontendFake{err: wantErr}
	if _, err := service.Graph(t.Context(), changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("frontend error = %v", err)
	}
	service.frontend = changePlanFrontendFake{}
	service.evidence = changePlanEvidenceFake{err: wantErr}
	if _, err := service.Graph(t.Context(), changePlanAdmin()); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("seed error = %v", err)
	}
	service.evidence = changePlanEvidenceFake{}
	runtime := &changePlanReferenceRuntimeFake{records: map[string][]recordmodel.Record{}, recordErrors: map[string]error{"scheduler": wantErr}}
	service.runtime = runtime
	if _, err := service.Graph(t.Context(), changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("scheduler error = %v", err)
	}
	runtime.recordErrors = map[string]error{}
	runtime.processErr = wantErr
	if _, err := service.Graph(t.Context(), changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("workflow process error = %v", err)
	}
	runtime.processErr = nil
	runtime.recordErrors["job_run"] = wantErr
	if _, err := service.Graph(t.Context(), changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("job run error = %v", err)
	}
	runtime.recordErrors = map[string]error{"job_dead_letter": wantErr}
	if _, err := service.Graph(t.Context(), changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("dead-letter error = %v", err)
	}
	runtime.recordErrors = map[string]error{}
	runtime.messageErr = wantErr
	if _, err := service.Graph(t.Context(), changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("outbox error = %v", err)
	}
	if referenceInternalError("read", wantErr) == nil || stringIndex(3) != "3" {
		t.Fatal("reference helpers failed")
	}
}

func TestChangePlanReferenceServiceBuildsOptionalRuntimeEvidence(t *testing.T) {
	runtime := &changePlanReferenceRuntimeFake{
		records: map[string][]recordmodel.Record{
			"job_definition":  {{ID: "job-1", Data: map[string]any{"name": "Nightly", "target_type": "workflow", "target_key": "approval"}}, {ID: "job-2", Data: map[string]any{"target_type": "report", "target_key": "orders"}}, {ID: "job-3", Data: map[string]any{"target_type": "ignored"}}},
			"job_run":         {{ID: "run-1", Data: map[string]any{"status": "leased", "scheduler_definition_key": "job-1"}}},
			"job_dead_letter": {{ID: "dead-1", Data: map[string]any{"status": "open", "scheduler_definition_key": "job-1"}}},
		},
		recordErrors: map[string]error{},
		processes: []workflowmodel.WorkflowProcessInstance{
			{ID: "running", WorkflowKey: "approval", WorkflowName: "Approval", ObjectKey: "order", Status: "running"},
			{ID: "waiting", WorkflowKey: "approval", Status: "waiting"},
			{ID: "config", WorkflowKey: "approval", Status: "configuration_error"},
			{ID: "done", WorkflowKey: "approval", Status: "completed"},
		},
		messages: []integrationmodel.IntegrationOutboxMessage{
			{ID: "pending", Status: "pending", ConnectorKey: "erp", Operation: "send", ConnectionKey: "primary"},
			{ID: "sent", Status: "sent"}, {ID: "cancelled", Status: "cancelled"},
		},
	}
	frontend := changePlanFrontendFake{snapshot: changeplanmodel.FrontendCapabilities{Manifest: &changeplanmodel.FrontendManifest{Entries: []changeplanmodel.FrontendSupportEntry{{
		SupportKey: "orders", Route: "/orders", FeatureModule: "orders-page", CapabilityKeys: []string{"object.list"}, RequiredPermissions: []string{"order.read"}, ActorRoles: []string{"sales"}, BusinessObjects: []string{"order"}, ViewKeys: []string{"order_list"}, ImplementedActions: []string{"order.approve"}, ReportKeys: []string{"orders"}, FieldKeys: []string{"order.status"}, AcceptanceClaims: []string{"order-list-refresh"},
	}}}}}
	evidence := changePlanEvidenceFake{seeds: []businessseedmodel.BusinessSeedProvenance{{SeedKey: "order_seed", ObjectKey: "order", RecordID: "order-1", SourceKind: "fixture"}}}
	service := NewChangePlanReferenceApplicationService(nil, runtime, evidence, frontend)
	graph, err := service.Graph(t.Context(), changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) < 20 || len(graph.Edges) < 20 || graph.Hash == "" {
		t.Fatalf("optional graph too small: nodes=%d edges=%d hash=%q", len(graph.Nodes), len(graph.Edges), graph.Hash)
	}

	frontend.snapshot.Manifest = nil
	service.frontend = frontend
	service.runtime = nil
	service.evidence = nil
	if graph, err := service.Graph(t.Context(), changePlanAdmin()); err != nil || graph.Hash == "" {
		t.Fatalf("nil optional graph = %+v, %v", graph, err)
	}
}
