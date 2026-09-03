package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowRecordRepositoryEdgeStub struct {
	recordrepository.RecordRepository
	record  recordmodel.Record
	found   bool
	getErr  error
	page    recordmodel.RecordPageResult
	listErr error
}

func (s workflowRecordRepositoryEdgeStub) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return s.record, s.found, s.getErr
}

func (s workflowRecordRepositoryEdgeStub) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return s.page, s.listErr
}

func TestWorkflowRecordReaderAdapterNilAndRepositoryDelegation(t *testing.T) {
	normalize := func(_ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
		query.AuthorizationMode = recordmodel.RecordQueryAuthorizationUnrestricted
		return query
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	empty := NewWorkflowRecordReaderAdapter(nil, normalize)
	if _, found, err := empty.GetWorkflowRecord(t.Context(), "workspace", definitionmodel.ObjectSchema{}, "record", principal); err != nil || found {
		t.Fatalf("nil get found=%v err=%v", found, err)
	}
	if page, err := empty.ListWorkflowRecords(t.Context(), "workspace", definitionmodel.ObjectSchema{}, recordmodel.RecordListQuery{}, principal); err != nil || page.Total != 0 {
		t.Fatalf("nil list page=%+v err=%v", page, err)
	}
	repository := workflowRecordRepositoryEdgeStub{record: recordmodel.Record{ID: "record"}, found: true, page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "record"}}, Total: 1}}
	adapter := NewWorkflowRecordReaderAdapter(repository, normalize)
	if record, found, err := adapter.GetWorkflowRecord(t.Context(), "workspace", definitionmodel.ObjectSchema{}, "record", principal); err != nil || !found || record.ID != "record" {
		t.Fatalf("record=%+v found=%v err=%v", record, found, err)
	}
	if page, err := adapter.ListWorkflowRecords(t.Context(), "workspace", definitionmodel.ObjectSchema{}, recordmodel.RecordListQuery{}, principal); err != nil || page.Total != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	failure := errors.New("repository")
	adapter = NewWorkflowRecordReaderAdapter(workflowRecordRepositoryEdgeStub{getErr: failure, listErr: failure}, normalize)
	if _, _, err := adapter.GetWorkflowRecord(t.Context(), "workspace", definitionmodel.ObjectSchema{}, "record", principal); !errors.Is(err, failure) {
		t.Fatalf("get error=%v", err)
	}
	if _, err := adapter.ListWorkflowRecords(t.Context(), "workspace", definitionmodel.ObjectSchema{}, recordmodel.RecordListQuery{}, principal); !errors.Is(err, failure) {
		t.Fatalf("list error=%v", err)
	}
}

func TestWorkflowProcessRuntimeAuthorizationWrappersAndFailedNode(t *testing.T) {
	store := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	runtime := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store})
	engine := runtime.ProcessEngine()
	if engine == nil || engine.DecisionRuntime() != runtime {
		t.Fatalf("runtime engine=%p decision=%T", engine, engine.DecisionRuntime())
	}
	unknown := principalmodel.Principal{}
	if _, err := engine.RunWithContext(t.Context(), workflowmodel.WorkflowProcessInstance{}, nil, nil, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("run authorization=%v", err)
	}
	if _, err := engine.ResolveRecipients(t.Context(), workflowmodel.WorkflowProcessInstance{}, nil, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("recipient authorization=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{}}}
	completed, err := engine.RunWithContext(t.Context(), process, nil, nil, principal)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	recipients, err := engine.ResolveRecipients(t.Context(), process, nil, principal)
	if err != nil || len(recipients) != 0 {
		t.Fatalf("recipients=%v err=%v", recipients, err)
	}
	workflow := definitionmodel.WorkflowSchema{Key: "flow", Name: "Flow"}
	if WorkflowDefinitionHash(workflow) == "" {
		t.Fatal("definition hash empty")
	}
	graph := &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "node", Type: "action"}}}
	if node, found := WorkflowGraphNode(graph, "node"); !found || node.ID != "node" {
		t.Fatalf("node=%+v found=%v", node, found)
	}
	if _, found := WorkflowGraphNode(nil, "node"); found {
		t.Fatal("node found in nil graph")
	}
	if WorkflowProcessID(t.Context(), "process") == "" {
		t.Fatal("process id empty")
	}
	engine.recordFailedNode(t.Context(), process, definitionmodel.WorkflowGraphNode{ID: "node", Type: "action"}, map[string]any{"value": true}, errors.New("failure"), "actor")
	if len(store.nodes["process"]) != 1 || store.nodes["process"][0].ErrorCode != "failure" {
		t.Fatalf("nodes=%+v", store.nodes["process"])
	}
}

func TestWorkflowProcessRuntimeResumeTimerNodeEdges(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	process := workflowmodel.WorkflowProcessInstance{
		ID:             "process",
		WorkspaceID:    principal.WorkspaceID,
		Status:         "waiting",
		CurrentNodeIDs: []string{"timer"},
		DefinitionSnapshot: definitionmodel.WorkflowSchema{
			Graph: &definitionmodel.WorkflowGraphSchema{},
		},
	}
	newEngine := func(store *workflowProcessStoreEdgeStub) *WorkflowProcessEngine {
		return NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store}).ProcessEngine()
	}
	newStore := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{
			workflowExecutionProcessStub: workflowExecutionProcessStub{
				processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process},
				nodes: map[string][]workflowmodel.WorkflowNodeInstance{
					"process": {{ProcessID: "process", WorkspaceID: principal.WorkspaceID, NodeID: "timer", Status: "waiting"}},
				},
			},
		}
	}

	if _, err := newEngine(newStore()).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	store := newStore()
	store.getProcessErr = errors.New("process")
	if _, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get process error=%v", err)
	}
	store = newStore()
	delete(store.processes, "process")
	if _, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); apperror.CodeOf(err) != "backend.workflow.process_not_found" {
		t.Fatalf("missing process error=%v", err)
	}
	store = newStore()
	store.listNodesErr = errors.New("nodes")
	if _, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list nodes error=%v", err)
	}
	store = newStore()
	store.nodes["process"] = []workflowmodel.WorkflowNodeInstance{{NodeID: "timer", Status: "success"}}
	if resumed, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); err != nil || resumed.ID != process.ID {
		t.Fatalf("completed timer process=%+v error=%v", resumed, err)
	}
	store = newStore()
	running := process
	running.Status = "running"
	store.processes["process"] = running
	if _, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); apperror.CodeOf(err) != "backend.workflow.timer_node_not_waiting" {
		t.Fatalf("running process error=%v", err)
	}
	store = newStore()
	missingCurrent := process
	missingCurrent.CurrentNodeIDs = []string{"other"}
	store.processes["process"] = missingCurrent
	if _, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); apperror.CodeOf(err) != "backend.workflow.timer_node_not_waiting" {
		t.Fatalf("missing current node error=%v", err)
	}
	store = newStore()
	store.nodes["process"] = []workflowmodel.WorkflowNodeInstance{{NodeID: "timer", Status: "running"}}
	if _, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); apperror.CodeOf(err) != "backend.workflow.timer_node_not_waiting" {
		t.Fatalf("missing waiting instance error=%v", err)
	}
	store = newStore()
	store.updateNodeErr = errors.New("update")
	store.nodes["process"][0].Output = map[string]any{"existing": true}
	if _, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update timer node error=%v", err)
	}
	store = newStore()
	store.nodes["process"] = append([]workflowmodel.WorkflowNodeInstance{{NodeID: "other", Status: "waiting"}}, store.nodes["process"]...)
	resumed, err := newEngine(store).ResumeTimerNode(t.Context(), principal.WorkspaceID, "process", "timer", principal)
	if err != nil || resumed.Status != "completed" || len(store.updatedNodes) != 1 || store.updatedNodes[0].Output["resumed"] != true {
		t.Fatalf("resumed=%+v updated=%+v error=%v", resumed, store.updatedNodes, err)
	}
	if containsWorkflowString([]string{"other"}, "timer") {
		t.Fatal("unexpected timer match")
	}
}
