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
	ObjectKey     string
	RunAsRole     string
	ConnectionKey string
	Payload       []byte
}

type ExecutionRequest struct {
	ExecutionID    string
	DefinitionKey  string
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

type AgentTargetRequest struct {
	ExecutionID    string
	IdempotencyKey string
	Operation      string
	ScheduledFor   time.Time
	Payload        []byte
}

type AgentTargetReceipt struct {
	ID     string
	Status string
}

// AgentTargetRuntime is a Runtime composition port. Its implementation maps
// Scheduler facts to the public Agent SDK after current Identity resolution;
// this application service never imports Scheduler or Agent implementations.
type AgentTargetRuntime interface {
	ExecuteAgentTarget(context.Context, AgentTargetRequest) (AgentTargetReceipt, error)
}

type NotificationTargetRequest struct {
	ExecutionID    string
	IdempotencyKey string
	Operation      string
	ScheduledFor   time.Time
	Payload        []byte
}

type NotificationTargetReceipt struct {
	ID     string
	Status string
}

// NotificationTargetRuntime is the source-neutral handoff owned by Runtime's
// composition root. Notification keeps inbox and channel-delivery lifecycle;
// this service only addresses the configured capability.
type NotificationTargetRuntime interface {
	ExecuteNotificationTarget(context.Context, NotificationTargetRequest) (NotificationTargetReceipt, error)
}

type BusinessActionTargetRequest struct {
	ExecutionID     string
	DefinitionKey   string
	IdempotencyKey  string
	Operation       string
	ObjectKey       string
	RunAsRole       string
	ScheduledFor    time.Time
	AuthorizationID string
	Payload         []byte
}

type BusinessActionTargetReceipt struct {
	ID     string
	Status string
}

// BusinessActionTargetRuntime is implemented by Runtime composition so the
// schedule-agnostic dispatcher never imports the Action or Identity owners.
type BusinessActionTargetRuntime interface {
	ExecuteBusinessActionTarget(context.Context, BusinessActionTargetRequest) (BusinessActionTargetReceipt, error)
}

type TargetExecutionApplicationService struct {
	workflows       WorkflowTargetRuntime
	reportSnapshots ReportSnapshotRuntime
	agent           AgentTargetRuntime
	notifications   NotificationTargetRuntime
	businessActions BusinessActionTargetRuntime
}

func NewTargetExecutionApplicationService(workflows WorkflowTargetRuntime) *TargetExecutionApplicationService {
	return &TargetExecutionApplicationService{workflows: workflows}
}

func (s *TargetExecutionApplicationService) UseReportSnapshotRuntime(runtime ReportSnapshotRuntime) {
	if s != nil {
		s.reportSnapshots = runtime
	}
}

func (s *TargetExecutionApplicationService) UseAgentTargetRuntime(runtime AgentTargetRuntime) {
	if s != nil {
		s.agent = runtime
	}
}

func (s *TargetExecutionApplicationService) UseNotificationTargetRuntime(runtime NotificationTargetRuntime) {
	if s != nil {
		s.notifications = runtime
	}
}

func (s *TargetExecutionApplicationService) UseBusinessActionTargetRuntime(runtime BusinessActionTargetRuntime) {
	if s != nil {
		s.businessActions = runtime
	}
}

func (s *TargetExecutionApplicationService) Execute(ctx context.Context, request ExecutionRequest) (ExecutionReceipt, error) {
	if s == nil {
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
	case "business_action":
		if s.businessActions == nil {
			return ExecutionReceipt{}, dispatchError(apperror.KindUnavailable, "backend.dispatch.business_action_target_unavailable")
		}
		receipt, err := s.businessActions.ExecuteBusinessActionTarget(ctx, BusinessActionTargetRequest{
			ExecutionID: executionID, DefinitionKey: strings.TrimSpace(request.DefinitionKey), IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
			Operation: strings.TrimSpace(request.Target.Operation), ObjectKey: strings.TrimSpace(request.Target.ObjectKey), RunAsRole: strings.TrimSpace(request.Target.RunAsRole),
			ScheduledFor: request.DueAt.UTC(), Payload: append([]byte(nil), request.Target.Payload...),
		})
		if err != nil {
			return ExecutionReceipt{}, err
		}
		receiptID := strings.TrimSpace(receipt.ID)
		if receiptID == "" {
			receiptID = executionID
		}
		status := strings.TrimSpace(receipt.Status)
		if status == "" {
			status = "accepted"
		}
		return ExecutionReceipt{ID: receiptID, Owner: "business_action", Status: status}, nil
	case "workflow":
		if s.workflows == nil {
			return ExecutionReceipt{}, dispatchError(apperror.KindUnavailable, "backend.dispatch.workflow_target_unavailable")
		}
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
	case "agent":
		if s.agent == nil {
			return ExecutionReceipt{}, dispatchError(apperror.KindUnavailable, "backend.dispatch.agent_target_unavailable")
		}
		receipt, err := s.agent.ExecuteAgentTarget(ctx, AgentTargetRequest{
			ExecutionID: executionID, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), Operation: strings.TrimSpace(request.Target.Operation),
			ScheduledFor: request.DueAt.UTC(), Payload: append([]byte(nil), request.Target.Payload...),
		})
		if err != nil {
			return ExecutionReceipt{}, err
		}
		receiptID := strings.TrimSpace(receipt.ID)
		if receiptID == "" {
			receiptID = executionID
		}
		status := strings.TrimSpace(receipt.Status)
		if status == "" {
			status = "accepted"
		}
		return ExecutionReceipt{ID: receiptID, Owner: "agent", Status: status}, nil
	case "notification":
		if s.notifications == nil {
			return ExecutionReceipt{}, dispatchError(apperror.KindUnavailable, "backend.dispatch.notification_target_unavailable")
		}
		receipt, err := s.notifications.ExecuteNotificationTarget(ctx, NotificationTargetRequest{
			ExecutionID: executionID, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), Operation: strings.TrimSpace(request.Target.Operation),
			ScheduledFor: request.DueAt.UTC(), Payload: append([]byte(nil), request.Target.Payload...),
		})
		if err != nil {
			return ExecutionReceipt{}, err
		}
		receiptID := strings.TrimSpace(receipt.ID)
		if receiptID == "" {
			receiptID = executionID
		}
		status := strings.TrimSpace(receipt.Status)
		if status == "" {
			status = "accepted"
		}
		return ExecutionReceipt{ID: receiptID, Owner: "notification", Status: status}, nil
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
