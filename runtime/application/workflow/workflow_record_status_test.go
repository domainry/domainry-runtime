package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type recordStatusStore struct {
	workflowcontract.WorkflowProcessStore
	items     []workflowmodel.WorkflowProcessInstance
	err       error
	called    int
	workspace string
	filter    workflowmodel.WorkflowProcessFilter
}

func (r *recordStatusStore) ListProcesses(_ context.Context, workspace string, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	r.called++
	r.workspace, r.filter = workspace, filter
	return r.items, r.err
}

func recordStatusFixture(t *testing.T) (*WorkflowApplicationService, *recordStatusStore, principalmodel.Principal) {
	t.Helper()
	store := &recordStatusStore{items: []workflowmodel.WorkflowProcessInstance{{WorkspaceID: "workspace", ProcessID: "process", WorkflowKey: "flow", ObjectKey: "case", RecordID: "case-1", Status: "configuration_error", ErrorCode: "case.investigation_invalid", CreatedAt: "2026-11-17T00:00:00Z", UpdatedAt: "2026-11-17T00:01:00Z", Variables: map[string]any{"secret": "PRIVATE_VARIABLE"}, Result: map[string]any{"output": "PRIVATE_OUTPUT"}}}}
	service := &WorkflowApplicationService{processRepo: store,
		objectForAction: func(_ context.Context, p principalmodel.Principal, key, action string) (definitionmodel.ObjectSchema, error) {
			if p.UserID != "support" || p.RoleKey != "support_agent" || key != "case" || action != "read" {
				t.Fatal("wrong record authority", p, key, action)
			}
			return definitionmodel.ObjectSchema{Key: key}, nil
		},
		recordReader: workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{"case-1": {RecordID: "case-1"}}},
		canAccess: func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
			return true
		},
	}
	return service, store, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "support", RoleKey: "support_agent"}}
}

func TestRecordWorkflowStatusUsesReadableRecordWithoutWorkflowOperations(t *testing.T) {
	s, store, p := recordStatusFixture(t)
	got, err := s.ReadRecordWorkflow(t.Context(), p, "case", "case-1", "flow")
	if err != nil || !got.Found || got.Status != "configuration_error" || got.ErrorCode != "case.investigation_invalid" || got.UpdatedAt-got.StartedAt != 60000 || got.CompletedAt != 0 {
		t.Fatal(got, err)
	}
	if store.called != 1 || store.workspace != p.WorkspaceID || store.filter.WorkflowKey != "flow" || store.filter.ObjectKey != "case" || store.filter.RecordID != "case-1" || store.filter.Limit != 2 || store.filter.VisibleToUserID != "" {
		t.Fatal("unbounded or participant-scoped lookup", store)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "Variables") || strings.Contains(string(raw), "Result") {
		t.Fatal("private Workflow payload escaped", string(raw))
	}
}

func TestRecordWorkflowStatusDeniesBeforeOwnerLookupAndRechecksRevocation(t *testing.T) {
	for _, variant := range []string{"anonymous", "no_workspace", "record_permission", "missing", "row_scope", "revoked_during_read"} {
		t.Run(variant, func(t *testing.T) {
			s, store, p := recordStatusFixture(t)
			expectCalls := 0
			switch variant {
			case "anonymous":
				p.Known = false
			case "no_workspace":
				p.WorkspaceID = ""
			case "record_permission":
				s.objectForAction = func(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return definitionmodel.ObjectSchema{}, forbidden("record.read_denied")
				}
			case "missing":
				s.recordReader = workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{}}
			case "row_scope":
				s.canAccess = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
					return false
				}
			case "revoked_during_read":
				expectCalls = 1
				s.canAccess = func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
					return store.called == 0
				}
			}
			got, err := s.ReadRecordWorkflow(t.Context(), p, "case", "case-1", "flow")
			if err == nil || got.Found || store.called != expectCalls {
				t.Fatal("authority bypass", got, err, store.called)
			}
		})
	}
}

func TestRecordWorkflowStatusRejectsForeignAmbiguousAndUnavailableState(t *testing.T) {
	for _, variant := range []string{"workspace", "record", "object", "workflow", "ambiguous", "storage", "raw_error", "invalid_time", "unknown_status"} {
		t.Run(variant, func(t *testing.T) {
			s, store, p := recordStatusFixture(t)
			switch variant {
			case "workspace":
				store.items[0].WorkspaceID = "other"
			case "record":
				store.items[0].RecordID = "other"
			case "object":
				store.items[0].ObjectKey = "other"
			case "workflow":
				store.items[0].WorkflowKey = "other"
			case "ambiguous":
				store.items = append(store.items, store.items[0])
			case "storage":
				store.err = errors.New("database unavailable")
			case "raw_error":
				store.items[0].ErrorCode = "PRIVATE raw output"
			case "invalid_time":
				store.items[0].UpdatedAt = "yesterday"
			case "unknown_status":
				store.items[0].Status = "maybe_failed"
			}
			got, err := s.ReadRecordWorkflow(t.Context(), p, "case", "case-1", "flow")
			if err == nil || got.Found {
				t.Fatal(got, err)
			}
			if variant == "ambiguous" && apperror.CodeOf(err) != "backend.workflow.record_status_ambiguous" {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordWorkflowAbsenceAndRecoveryDoNotInventFailure(t *testing.T) {
	s, store, p := recordStatusFixture(t)
	store.items = nil
	got, err := s.ReadRecordWorkflow(t.Context(), p, "case", "case-1", "flow")
	if err != nil || got.Found {
		t.Fatal(got, err)
	}
	store.items = []workflowmodel.WorkflowProcessInstance{{WorkspaceID: p.WorkspaceID, ProcessID: "process", WorkflowKey: "flow", ObjectKey: "case", RecordID: "case-1", Status: "completed", ErrorCode: "old.failure", CompletedAt: "2026-11-17T00:02:00Z"}}
	got, err = s.ReadRecordWorkflow(t.Context(), p, "case", "case-1", "flow")
	if err != nil || got.Status != "completed" || got.ErrorCode != "" || got.CompletedAt < 1_000_000_000_000 {
		t.Fatal(got, err)
	}
}
