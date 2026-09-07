// Package dispatch owns Runtime's schedule-agnostic target-execution port.
// Callers resolve when and why an execution should happen before crossing this
// boundary; Runtime only invokes the addressed Runtime-resident target.
package dispatch

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type Target struct {
	Type          string
	Owner         string
	Operation     string
	ConnectionKey string
	Payload       []byte
}

type ExecutionRequest struct {
	ExecutionID    string
	IdempotencyKey string
	DueAt          time.Time
	Target         Target
	Limit          int
	Principal      principalmodel.Principal
}

type ExecutionReceipt struct {
	ID     string
	Owner  string
	Status string
}

type WorkflowTargetRequest struct {
	ExecutionID    string
	IdempotencyKey string
	Operation      string
	EffectiveAt    time.Time
	Limit          int
	Principal      principalmodel.Principal
}

type WorkflowTargetRuntime interface {
	ExecuteWorkflowTarget(context.Context, WorkflowTargetRequest) (workflowmodel.WorkflowProcessResult, error)
}

type ReportSnapshotRuntime interface {
	RefreshSnapshot(context.Context, string, string, principalmodel.Principal) (reportmodel.ReportSnapshot, error)
}

type TargetExecutionApplicationService struct {
	workflows       WorkflowTargetRuntime
	reportSnapshots ReportSnapshotRuntime
}

func NewTargetExecutionApplicationService(workflows WorkflowTargetRuntime) *TargetExecutionApplicationService {
	return &TargetExecutionApplicationService{workflows: workflows}
}

func (s *TargetExecutionApplicationService) UseReportSnapshotRuntime(runtime ReportSnapshotRuntime) {
	if s != nil {
		s.reportSnapshots = runtime
	}
}

func (s *TargetExecutionApplicationService) Execute(ctx context.Context, request ExecutionRequest) (ExecutionReceipt, error) {
	if s == nil || s.workflows == nil {
		return ExecutionReceipt{}, dispatchError(apperror.KindUnavailable, "backend.dispatch.target_executor_unavailable")
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 25
	}
	principal := request.Principal
	principal.WorkspaceID = dispatchWorkspaceID(principal)
	executionID := strings.TrimSpace(request.ExecutionID)

	switch strings.TrimSpace(request.Target.Owner) {
	case "workflow":
		result, err := s.workflows.ExecuteWorkflowTarget(ctx, WorkflowTargetRequest{ExecutionID: executionID, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), Operation: strings.TrimSpace(request.Target.Operation), EffectiveAt: request.DueAt.UTC(), Limit: limit, Principal: principal})
		if err != nil {
			return ExecutionReceipt{}, err
		}
		receiptID := executionID
		if len(result.Executions) > 0 && strings.TrimSpace(result.Executions[0].ID) != "" {
			receiptID = strings.TrimSpace(result.Executions[0].ID)
		}
		return ExecutionReceipt{ID: receiptID, Owner: "workflow", Status: "accepted"}, nil
	case "report_snapshot_refresh":
		if s.reportSnapshots == nil {
			return ExecutionReceipt{}, dispatchError(apperror.KindBadRequest, "backend.dispatch.report_snapshot_runtime_unavailable", "target_owner", request.Target.Owner)
		}
		snapshot, err := s.reportSnapshots.RefreshSnapshot(ctx, strings.TrimSpace(request.Target.Operation), strings.TrimSpace(request.IdempotencyKey), principal)
		if err != nil {
			return ExecutionReceipt{}, err
		}
		receiptID := strings.TrimSpace(snapshot.ID)
		if receiptID == "" {
			receiptID = executionID
		}
		return ExecutionReceipt{ID: receiptID, Owner: "report_snapshot_refresh", Status: "accepted"}, nil
	default:
		return ExecutionReceipt{}, dispatchError(apperror.KindBadRequest, "backend.dispatch.unsupported_target_owner", "target_owner", strings.TrimSpace(request.Target.Owner))
	}
}

func dispatchWorkspaceID(principal principalmodel.Principal) string {
	if workspaceID, err := principalmodel.NewWorkspaceID(principal.WorkspaceID); err == nil {
		return workspaceID.String()
	}
	if principal.SystemScope.Valid() {
		return principalmodel.InstallationWorkspaceID
	}
	return ""
}

func dispatchError(kind apperror.ErrorKind, code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: nil}
}
