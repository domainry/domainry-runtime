package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type workflowIdempotencyWorkerEdgeStub struct {
	workflowExecutionWorkerStub
	claim         workflowmodel.WorkflowExecutionClaimResult
	claimErr      error
	claims        []workflowmodel.WorkflowExecutionClaimResult
	completionErr error
	claimRequests []workflowmodel.WorkflowExecutionClaimRequest
	completions   []workflowmodel.WorkflowExecutionReceiptCompletion
}

func (s *workflowIdempotencyWorkerEdgeStub) TryBeginExecution(_ context.Context, request workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error) {
	s.claimRequests = append(s.claimRequests, request)
	if len(s.claims) > 0 {
		claim := s.claims[0]
		s.claims = s.claims[1:]
		return claim, nil
	}
	return s.claim, s.claimErr
}

func (s *workflowIdempotencyWorkerEdgeStub) CompleteExecutionReceipt(_ context.Context, completion workflowmodel.WorkflowExecutionReceiptCompletion) error {
	s.completions = append(s.completions, completion)
	return s.completionErr
}

func workflowIdempotencyService(worker *workflowIdempotencyWorkerEdgeStub, audits *[]string) *WorkflowApplicationService {
	return &WorkflowApplicationService{
		workerRepo: worker,
		auditMetadata: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
			*audits = append(*audits, event)
		},
	}
}

func TestWorkflowExecutionIdempotencyClaimDecisionMatrix(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "flow", Name: "Flow"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user"}, RequestID: "request-owner"}
	receipt := workflowmodel.WorkflowExecutionReceipt{ID: "receipt", WorkspaceID: "workspace-1", WorkflowKey: "flow", IdempotencyKey: "key", ExecutionID: "execution", LeaseOwner: "owner", FencingToken: 3}
	execution := workflowmodel.WorkflowExecution{ID: "execution", Status: "completed"}

	for name, testCase := range map[string]struct {
		decision     idempotency.Decision
		receipt      workflowmodel.WorkflowExecutionReceipt
		executions   map[string]workflowmodel.WorkflowExecution
		getErr       error
		claimErr     error
		expectedCode string
		replayed     bool
	}{
		"acquired":             {decision: idempotency.DecisionAcquired, receipt: receipt},
		"replay":               {decision: idempotency.DecisionReplay, receipt: receipt, executions: map[string]workflowmodel.WorkflowExecution{"execution": execution}, replayed: true},
		"replay missing id":    {decision: idempotency.DecisionReplay, receipt: workflowmodel.WorkflowExecutionReceipt{ID: "receipt"}, expectedCode: idempotency.ErrorCodeReceiptUnavailable},
		"replay read failure":  {decision: idempotency.DecisionReplay, receipt: receipt, getErr: errors.New("get"), expectedCode: "backend.internal"},
		"replay not found":     {decision: idempotency.DecisionReplay, receipt: receipt, executions: map[string]workflowmodel.WorkflowExecution{}, expectedCode: idempotency.ErrorCodeReceiptUnavailable},
		"fingerprint conflict": {decision: idempotency.DecisionFingerprintConflict, receipt: receipt, expectedCode: idempotency.ErrorCodeKeyReused},
		"in progress":          {decision: idempotency.DecisionInProgress, receipt: receipt, expectedCode: idempotency.ErrorCodeInProgress},
		"unknown":              {decision: idempotency.Decision("unknown"), receipt: receipt, expectedCode: idempotency.ErrorCodeReceiptUnavailable},
		"claim failure":        {claimErr: errors.New("claim"), expectedCode: "backend.internal"},
	} {
		t.Run(name, func(t *testing.T) {
			audits := []string{}
			worker := &workflowIdempotencyWorkerEdgeStub{
				workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: testCase.executions, getErr: testCase.getErr},
				claim:                       workflowmodel.WorkflowExecutionClaimResult{Decision: testCase.decision, Receipt: testCase.receipt}, claimErr: testCase.claimErr,
			}
			service := workflowIdempotencyService(worker, &audits)
			claim, actual, replayed, err := service.beginWorkflowExecution(t.Context(), workflow, map[string]any{"id": "record"}, principal, "manual", "key", 1, false)
			if apperror.CodeOf(err) != testCase.expectedCode && !(err == nil && testCase.expectedCode == "") {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
			if replayed != testCase.replayed {
				t.Fatalf("replayed=%v", replayed)
			}
			if replayed && actual.ID != execution.ID {
				t.Fatalf("execution=%+v", actual)
			}
			if testCase.claimErr == nil && claim.Decision != testCase.decision {
				t.Fatalf("claim=%+v", claim)
			}
			if testCase.claimErr == nil && len(worker.claimRequests) != 1 {
				t.Fatalf("claim requests=%d", len(worker.claimRequests))
			}
		})
	}
}

func TestWorkflowExecutionIdempotencyBypassFingerprintOwnerAndCompletion(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "flow"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "", UserID: "user"}}
	audits := []string{}
	worker := &workflowIdempotencyWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, claim: workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: workflowmodel.WorkflowExecutionReceipt{ID: "receipt", WorkflowKey: "flow", WorkspaceID: "default", LeaseOwner: "owner", FencingToken: 2}}}
	service := workflowIdempotencyService(worker, &audits)
	if _, _, found, err := service.beginWorkflowExecution(t.Context(), workflow, nil, principal, "manual", "", 0, false); err != nil || found {
		t.Fatalf("empty key found=%v err=%v", found, err)
	}
	if _, _, found, err := service.beginWorkflowExecution(t.Context(), workflow, nil, principal, "manual", "key", 0, true); err != nil || found {
		t.Fatalf("ignored found=%v err=%v", found, err)
	}
	if _, _, _, err := service.beginWorkflowExecution(t.Context(), workflow, map[string]any{"invalid": func() {}}, principal, "manual", "key", 0, false); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("fingerprint error=%v", err)
	}
	claim, _, _, err := service.beginWorkflowExecution(t.Context(), workflow, nil, principal, "manual", "key", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(worker.claimRequests) != 1 || worker.claimRequests[0].LeaseOwner == "" || worker.claimRequests[0].Receipt.WorkspaceID != "default" {
		t.Fatalf("claim request=%+v", worker.claimRequests)
	}
	if err := service.completeWorkflowExecutionReceipt(t.Context(), workflowmodel.WorkflowExecutionClaimResult{}, workflowmodel.WorkflowExecution{}, principal); err != nil {
		t.Fatal(err)
	}
	emptyReceipt := claim
	emptyReceipt.Receipt.ID = ""
	if err := service.completeWorkflowExecutionReceipt(t.Context(), emptyReceipt, workflowmodel.WorkflowExecution{}, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.completeWorkflowExecutionReceipt(t.Context(), claim, workflowmodel.WorkflowExecution{ID: "execution"}, principal); err != nil {
		t.Fatal(err)
	}
	if len(worker.completions) != 1 || len(audits) != 1 || !strings.Contains(audits[0], "succeeded") {
		t.Fatalf("completions=%v audits=%v", worker.completions, audits)
	}
	worker.completionErr = errors.New("complete")
	if err := service.completeWorkflowExecutionReceipt(t.Context(), claim, workflowmodel.WorkflowExecution{ID: "execution"}, principal); err == nil {
		t.Fatal("expected completion error")
	}
	service.auditMetadata = nil
	service.auditWorkflowIdempotency(t.Context(), "flow", principal, claim, "replayed")
	service.auditWorkflowIdempotency(t.Context(), "flow", principal, workflowmodel.WorkflowExecutionClaimResult{}, "replayed")
	if workflowWorkspaceID(" workspace ") != "workspace" || workflowWorkspaceID(" ") != "default" {
		t.Fatal("workspace normalization mismatch")
	}
}

func TestWorkflowCommandReplayAndRecordingHelpers(t *testing.T) {
	key := workflowCommandKey(" task.decision ", " task ", " caller ", map[string]any{"decision": "approve"})
	if key == "" || workflowCommandResultField(" task.decision:x ") != "idempotency_command_task_decision_x" {
		t.Fatalf("key=%q field=%q", key, workflowCommandResultField(" task.decision:x "))
	}
	process := workflowmodel.WorkflowProcessInstance{}
	workflowRecordCommand(&process, "task.decision:task", key)
	if !workflowProcessHasCommand(process, "task.decision:task", " "+key+" ") {
		t.Fatalf("process result=%v", process.Result)
	}
	workflowRecordCommand(&process, "other", " value ")
	if workflowStringValue(nil) != "" || workflowStringValue(" value ") != "value" {
		t.Fatal("string normalization mismatch")
	}
	task := workflowmodel.WorkflowTask{ID: "task", ProcessID: "process"}
	req := workflowmodel.WorkflowTaskDecisionRequest{Decision: " APPROVE ", Comment: " ok ", IdempotencyKey: "caller"}
	decisionKey := workflowCommandKey("task.decision", "task", "caller", map[string]any{"decision": "approve", "comment": "ok"})
	replayProcess := workflowmodel.WorkflowProcessInstance{ID: "process"}
	workflowRecordCommand(&replayProcess, "task.decision:task", decisionKey)
	store := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": replayProcess}}
	actual, replayed := workflowDecisionReplay(t.Context(), "workspace", store, task, req)
	if !replayed || actual.ID != "process" {
		t.Fatalf("process=%+v replayed=%v", actual, replayed)
	}
	if _, replayed := workflowDecisionReplay(t.Context(), "workspace", &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}}, task, req); replayed {
		t.Fatal("missing process replayed")
	}
	errorStore := &workflowProcessGetErrorStub{}
	if _, replayed := workflowDecisionReplay(t.Context(), "workspace", errorStore, task, req); replayed {
		t.Fatal("failed process replayed")
	}
}

type workflowProcessGetErrorStub struct{}

func (workflowProcessGetErrorStub) GetProcess(context.Context, string, string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	return workflowmodel.WorkflowProcessInstance{}, false, errors.New("get")
}
