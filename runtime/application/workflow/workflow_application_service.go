package workflow

import (
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/invocation"

	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowprojection "github.com/domainry/domainry-runtime/runtime/domain/workflow/projection"
)

// WorkflowApplicationService owns workflow-definition lifecycle behavior.
type WorkflowApplicationService struct {
	definitionRepo            workflowcontract.WorkflowDefinitionStore
	processRepo               workflowcontract.WorkflowProcessStore
	workerRepo                workflowcontract.WorkflowWorkerStore
	registry                  WorkflowRegistry
	processEngine             *WorkflowProcessEngine
	objectForAction           func(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	audit                     func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any)
	auditMetadata             func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	decisions                 workflowcontract.WorkflowDecisionRuntime
	routes                    workflowcontract.WorkflowRouteStore
	identity                  identitysdk.Projection
	principals                identitysdk.PrincipalResolver
	workloads                 identitysdk.WorkflowWorkloadIdentity
	workloadApplication       identitysdk.ApplicationScope
	workloadReleases          *WorkflowWorkloadReleaseState
	schema                    WorkflowSchemaProvider
	schemaMap                 func(context.Context) map[string]definitionmodel.ObjectSchema
	recordReader              WorkflowRecordReader
	canAccess                 func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	invokeAction              func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error)
	referenceValidator        *WorkflowReferenceValidator
	executionVisibility       *WorkflowExecutionVisibilityApplicationService
	worker                    workerplatform.Dependencies
	workflowHeartbeatInterval time.Duration
	continuationWakeups       chan WorkflowContinuationLocator
}

func NewWorkflowApplicationService(dependencies WorkflowDependencies) *WorkflowApplicationService {
	if dependencies.WorkloadReleases == nil {
		dependencies.WorkloadReleases = NewWorkflowWorkloadReleaseState()
	}
	processEngine := NewWorkflowProcessEngine(dependencies)
	return newWorkflowApplicationService(dependencies, processEngine)
}

type WorkflowContinuationLocator struct {
	WorkspaceID string
	ExecutionID string
}

type WorkflowContinuationRecoveryStore interface {
	ListWorkflowContinuationWorkspaces(context.Context, principalmodel.SystemScope, int) ([]string, error)
}

func WorkflowContinuationWakeups(service *WorkflowApplicationService) <-chan WorkflowContinuationLocator {
	if service == nil {
		return nil
	}
	return service.continuationWakeups
}

func WakeWorkflowContinuation(service *WorkflowApplicationService, locator WorkflowContinuationLocator) {
	if service == nil || service.continuationWakeups == nil || strings.TrimSpace(locator.ExecutionID) == "" {
		return
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil {
		return
	}
	select {
	case service.continuationWakeups <- WorkflowContinuationLocator{WorkspaceID: workspace.String(), ExecutionID: strings.TrimSpace(locator.ExecutionID)}:
	default:
		// Wakeups are bounded acceleration hints. Durable recovery owns progress.
	}
}

func (s *WorkflowApplicationService) wakeWorkflowExecution(execution workflowmodel.WorkflowExecution) {
	if s == nil || !workflowpolicy.WorkflowExecutionDue(execution, s.worker.Clock.Now()) {
		return
	}
	WakeWorkflowContinuation(s, WorkflowContinuationLocator{WorkspaceID: execution.WorkspaceID, ExecutionID: execution.ID})
}

func (s *WorkflowApplicationService) ProcessWorkflowContinuation(ctx context.Context, locator WorkflowContinuationLocator, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	if s == nil || s.workerRepo == nil {
		return workflowmodel.WorkflowProcessResult{}, apperror.New(apperror.KindUnavailable, "backend.workflow.worker_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil || strings.TrimSpace(locator.ExecutionID) == "" {
		return workflowmodel.WorkflowProcessResult{}, apperror.New(apperror.KindBadRequest, "backend.workflow.continuation_invalid", err, nil)
	}
	principal.WorkspaceID = workspace.String()
	return s.processDueWorkflowExecutions(ctx, "", time.Time{}, 1, principal, false, strings.TrimSpace(locator.ExecutionID), "")
}

func (s *WorkflowApplicationService) ResumeTimerNode(ctx context.Context, workspaceID, processID, nodeID string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	return s.processEngine.ResumeTimerNode(ctx, workspaceID, processID, nodeID, principal)
}

func newWorkflowApplicationService(dependencies WorkflowDependencies, processEngine *WorkflowProcessEngine) *WorkflowApplicationService {
	dependencies.Worker = workerplatform.NormalizeDependencies(dependencies.Worker)
	return &WorkflowApplicationService{
		definitionRepo: dependencies.Definitions,
		processRepo:    dependencies.Processes, workerRepo: dependencies.Workers,
		registry:        dependencies.WorkflowRegistry,
		processEngine:   processEngine,
		objectForAction: dependencies.ObjectForAction, audit: dependencies.Audit, auditMetadata: dependencies.AuditMetadata,
		decisions:        processEngine.DecisionRuntime(),
		routes:           dependencies.Routes,
		identity:         dependencies.Identity,
		principals:       dependencies.Principals,
		workloadReleases: dependencies.WorkloadReleases,
		schema:           dependencies.Schema,
		schemaMap:        dependencies.ObjectMap, recordReader: dependencies.RecordReader, canAccess: dependencies.CanAccessRecord,
		invokeAction:        dependencies.InvokeAction,
		referenceValidator:  NewWorkflowReferenceValidator(dependencies.Schema, dependencies.ObjectMap, dependencies.Identity),
		executionVisibility: NewWorkflowExecutionVisibilityApplicationService(dependencies.RecordReader, dependencies.CanAccessRecord),
		worker:              dependencies.Worker,
		continuationWakeups: make(chan WorkflowContinuationLocator, 256),
	}
}

func (s *WorkflowApplicationService) workflowProjectionActions(ctx context.Context, principal principalmodel.Principal) []definitionmodel.ActionSchema {
	if s.schema == nil {
		return nil
	}
	return s.schema.WorkflowSchemaSnapshot(ctx, principal).Actions
}

func (s *WorkflowApplicationService) WorkflowExecutions(ctx context.Context, principal principalmodel.Principal, objectKey string, recordID string, limit int) ([]workflowmodel.WorkflowExecution, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	objectKey = strings.TrimSpace(objectKey)
	recordID = strings.TrimSpace(recordID)
	var object definitionmodel.ObjectSchema
	if objectKey != "" {
		var err error
		object, err = s.objectForAction(ctx, principal, objectKey, "read")
		if err != nil {
			return nil, err
		}
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	listLimit := limit
	if objectKey != "" && listLimit < 500 {
		listLimit = 500
	}
	executions, err := s.workerRepo.ListExecutions(ctx, principal.WorkspaceID, listLimit)
	if err != nil {
		return nil, internalError("list workflow executions", err)
	}
	if objectKey != "" || recordID != "" {
		filtered := make([]workflowmodel.WorkflowExecution, 0, len(executions))
		for _, execution := range executions {
			if !workflowprojection.WorkflowExecutionMatchesRecordFilter(execution, objectKey, recordID) {
				continue
			}
			if objectKey != "" {
				allowed, err := s.executionVisibility.WorkflowExecutionVisible(ctx, principal, object, execution)
				if err != nil {
					return nil, internalError("get workflow execution record", err)
				}
				if !allowed {
					continue
				}
			}
			filtered = append(filtered, execution)
			if len(filtered) >= limit {
				break
			}
		}
		return filtered, nil
	}
	return executions, nil
}

func (s *WorkflowApplicationService) ProcessWorkflowExecutions(ctx context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessResult{}, err
	}
	return s.ProcessDueWorkflowExecutions(ctx, limit, workflowWorkerPrincipal())
}

func (s *WorkflowApplicationService) RunWorkflow(ctx context.Context, workflowKey string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return s.runInvokedWorkflow(ctx, workflowKey, payload, principal, invocationcontract.WorkflowEntryManual, "manual", "")
}

// RunWorkflowWithKey executes a caller-initiated manual workflow under the
// caller's logical operation key. Internal callers that rely on authored
// workflow idempotency continue to use RunWorkflow.
func (s *WorkflowApplicationService) RunWorkflowWithKey(ctx context.Context, workflowKey string, payload map[string]any, callerKey string, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	callerKey = strings.TrimSpace(callerKey)
	if callerKey == "" {
		return workflowmodel.WorkflowRunResult{}, badRequest(idempotency.ErrorCodeMissingKey)
	}
	return s.runInvokedWorkflow(ctx, workflowKey, payload, principal, invocationcontract.WorkflowEntryManual, "manual", callerKey)
}

func (s *WorkflowApplicationService) RunAutomationWorkflow(ctx context.Context, workflowKey string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return s.runInvokedWorkflow(ctx, workflowKey, payload, principal, invocationcontract.WorkflowEntryAutomation, "automation", "")
}

func (s *WorkflowApplicationService) RunAgentWorkflow(ctx context.Context, workflowKey string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return s.runInvokedWorkflow(ctx, workflowKey, payload, principal, invocationcontract.WorkflowEntryAgent, "agent", "")
}

func (s *WorkflowApplicationService) RunIntegrationWorkflow(ctx context.Context, workflowKey string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return s.runInvokedWorkflow(ctx, workflowKey, payload, principal, invocationcontract.WorkflowEntryIntegrationEvent, "integration_event", "")
}

func (s *WorkflowApplicationService) runInvokedWorkflow(ctx context.Context, workflowKey string, payload map[string]any, principal principalmodel.Principal, mode invocationcontract.WorkflowEntryMode, trigger, callerKey string) (workflowmodel.WorkflowRunResult, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	workflow, ok := s.registry.Get(workflowKey)
	if !ok {
		return workflowmodel.WorkflowRunResult{}, notFound("backend.workflow.not_found")
	}
	if len(invocationcontract.ValidateWorkflowPermission(workflow, principal)) > 0 {
		return workflowmodel.WorkflowRunResult{}, forbidden("backend.workflow.run_permission_required")
	}
	// Cross-resource callers validate their authored input contract before
	// publication. This execution boundary receives concrete rendered values and
	// independently closes target mode and permission before running anything.
	if issues := invocationcontract.ValidateWorkflowTarget(workflow, mode); len(issues) > 0 {
		return workflowmodel.WorkflowRunResult{}, workflowInvocationError(issues[0])
	}
	executionPayload := payload
	if mode == invocationcontract.WorkflowEntryManual {
		executionPayload = manualWorkflowPayload(payload, principal)
	}
	var execution workflowmodel.WorkflowExecution
	var err error
	if strings.TrimSpace(callerKey) == "" {
		execution, err = s.executeWorkflow(ctx, workflow, executionPayload, principal, trigger)
	} else {
		execution, err = s.executeWorkflowWithIdempotencyKey(ctx, workflow, executionPayload, principal, trigger, callerKey)
	}
	if err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	return workflowmodel.WorkflowRunResult{
		WorkflowKey: workflow.Key, Name: workflow.Name, Status: execution.Status,
		Action: workflow.Action, Payload: executionPayload, Execution: execution,
	}, nil
}

func manualWorkflowPayload(payload map[string]any, principal principalmodel.Principal) map[string]any {
	trusted := workflowpolicy.WorkflowCloneMap(payload)
	if userID := strings.TrimSpace(principal.UserID); userID != "" {
		trusted["initiating_user_id"] = userID
	} else {
		delete(trusted, "initiating_user_id")
	}
	if roleKey := strings.TrimSpace(principal.RoleKey); roleKey != "" {
		trusted["initiating_role_key"] = roleKey
	} else {
		delete(trusted, "initiating_role_key")
	}
	return trusted
}

func workflowInvocationError(issue invocationcontract.Issue) error {
	params := []string{"field", issue.Field, "expected", issue.Expected, "actual", issue.Actual, "reference", issue.Reference}
	switch issue.Code {
	case "invocation.workflow_disabled":
		return badRequest("backend.workflow.disabled")
	case "invocation.workflow_entry_mode_invalid":
		return badRequest("backend.workflow.entry_mode_invalid", params...)
	default:
		return badRequest("backend.workflow.invocation_invalid", params...)
	}
}
