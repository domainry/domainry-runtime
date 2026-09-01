package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	apperror "github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// runtimeIntegrationTriggerRelay breaks the module-open cycle: Integration is
// opened before Runtime application services are assembled, then receives the
// concrete Action/Workflow sink as soon as that assembly is complete.
type runtimeIntegrationTriggerRelay struct {
	mu   sync.RWMutex
	sink integrationsdk.TriggerSink
}

func (r *runtimeIntegrationTriggerRelay) Bind(sink integrationsdk.TriggerSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sink = sink
}

func (r *runtimeIntegrationTriggerRelay) Trigger(ctx context.Context, request integrationsdk.TriggerRequest) (integrationsdk.RuntimeExecutionReceipt, error) {
	r.mu.RLock()
	sink := r.sink
	r.mu.RUnlock()
	if sink == nil {
		return integrationsdk.RuntimeExecutionReceipt{}, fmt.Errorf("Runtime Integration trigger sink is not ready")
	}
	return sink.Trigger(ctx, request)
}

type runtimeIntegrationTriggerSink struct {
	actions interface {
		Invoke(context.Context, actionmodel.ActionSource, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
	}
	workflows interface {
		RunIntegrationWorkflow(context.Context, string, map[string]any, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error)
	}
	principals identitysdk.PrincipalResolver
}

func newRuntimeIntegrationTriggerSink(records *composition.RuntimeServices, principals identitysdk.PrincipalResolver) integrationsdk.TriggerSink {
	if records == nil {
		return runtimeIntegrationTriggerSink{principals: principals}
	}
	applications := records.Applications()
	return runtimeIntegrationTriggerSink{actions: applications.Actions, workflows: applications.Workflows, principals: principals}
}

func (s runtimeIntegrationTriggerSink) Trigger(ctx context.Context, request integrationsdk.TriggerRequest) (integrationsdk.RuntimeExecutionReceipt, error) {
	if err := validateRuntimeIntegrationTrigger(request); err != nil {
		return integrationsdk.RuntimeExecutionReceipt{}, err
	}
	principal, err := s.principal(ctx, request)
	if err != nil {
		return runtimeIntegrationReceipt(request, "", "failed", apperror.CodeOf(err)), err
	}
	switch strings.TrimSpace(request.Target.Type) {
	case "action":
		if s.actions == nil {
			err := fmt.Errorf("Runtime Action application is unavailable")
			return runtimeIntegrationReceipt(request, "", "failed", "backend.integration.runtime_action_unavailable"), err
		}
		executionPrincipal := principal.WithExactSystemCapabilities(request.Target.ActionKey)
		result, invokeErr := s.actions.Invoke(ctx, actionmodel.ActionSourceIntegration, actionmodel.ActionInvocation{
			ActionKey: request.Target.ActionKey, ObjectKey: request.Target.ObjectKey, RecordID: request.Target.RecordID,
			Input: cloneIntegrationTriggerInput(request.Target.Input), Principal: executionPrincipal, Actor: principal,
			RequestID: request.EventID, IdempotencyKey: request.IdempotencyKey,
		})
		receipt := runtimeIntegrationReceipt(request, result.InvocationID, result.Status, result.ErrorCode)
		if invokeErr != nil && receipt.ErrorCode == "" {
			receipt.ErrorCode = apperror.CodeOf(invokeErr)
		}
		return receipt, invokeErr
	case "workflow":
		if s.workflows == nil {
			err := fmt.Errorf("Runtime Workflow application is unavailable")
			return runtimeIntegrationReceipt(request, "", "failed", "backend.integration.runtime_workflow_unavailable"), err
		}
		payload := cloneIntegrationTriggerInput(request.Target.Input)
		setIntegrationTriggerDefault(payload, "integration_event_id", request.EventID)
		setIntegrationTriggerDefault(payload, "integration_mapping_key", request.MappingKey)
		setIntegrationTriggerDefault(payload, "integration_idempotency_key", request.IdempotencyKey)
		executionPrincipal := principal.WithExactSystemCapabilities("workflow.run." + strings.TrimSpace(request.Target.WorkflowKey))
		result, runErr := s.workflows.RunIntegrationWorkflow(ctx, request.Target.WorkflowKey, payload, executionPrincipal)
		receipt := runtimeIntegrationReceipt(request, result.Execution.ID, result.Status, "")
		if runErr != nil {
			receipt.Status, receipt.ErrorCode = "failed", apperror.CodeOf(runErr)
		}
		return receipt, runErr
	default:
		return integrationsdk.RuntimeExecutionReceipt{}, fmt.Errorf("Runtime Integration trigger target type %q is unsupported", request.Target.Type)
	}
}

func (s runtimeIntegrationTriggerSink) principal(ctx context.Context, request integrationsdk.TriggerRequest) (principalmodel.Principal, error) {
	actorID := strings.TrimSpace(request.Principal.ActorID)
	if actorID == "" {
		principal := principalmodel.NewSystemPrincipal(
			"integration:worker",
			principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "execute verified Integration event mapping"),
		)
		principal.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
		principal.RequestID, principal.CorrelationID, principal.CausationID = request.EventID, request.EventID, request.EventID
		return principal, nil
	}
	if s.principals == nil {
		return principalmodel.Principal{}, fmt.Errorf("Identity principal resolver is unavailable for Integration actor %q", actorID)
	}
	resolution, err := s.principals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(actorID), RoleKey: strings.TrimSpace(request.Principal.RoleKey)})
	if err != nil {
		return principalmodel.Principal{}, fmt.Errorf("resolve Integration actor %q: %w", actorID, err)
	}
	if workspaceID := strings.TrimSpace(resolution.Principal.WorkspaceID); workspaceID != "" && workspaceID != strings.TrimSpace(request.WorkspaceID) {
		return principalmodel.Principal{}, fmt.Errorf("Integration actor workspace %q does not match trigger workspace %q", workspaceID, request.WorkspaceID)
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	principal := principalmodel.NewPrincipalFromIdentity(resolution.Principal, request.EventID)
	principal.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	principal.CorrelationID, principal.CausationID = request.EventID, request.EventID
	return principal, nil
}

func validateRuntimeIntegrationTrigger(request integrationsdk.TriggerRequest) error {
	for name, value := range map[string]string{
		"event_id": request.EventID, "workspace_id": request.WorkspaceID, "mapping_key": request.MappingKey,
		"idempotency_key": request.IdempotencyKey, "target_type": request.Target.Type,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("Runtime Integration trigger %s is required", name)
		}
	}
	switch strings.TrimSpace(request.Target.Type) {
	case "action":
		if strings.TrimSpace(request.Target.ObjectKey) == "" || strings.TrimSpace(request.Target.ActionKey) == "" {
			return fmt.Errorf("Runtime Integration action trigger requires object_key and action_key")
		}
	case "workflow":
		if strings.TrimSpace(request.Target.WorkflowKey) == "" {
			return fmt.Errorf("Runtime Integration workflow trigger requires workflow_key")
		}
	default:
		return fmt.Errorf("Runtime Integration trigger target type %q is unsupported", request.Target.Type)
	}
	return nil
}

func runtimeIntegrationReceipt(request integrationsdk.TriggerRequest, executionID, status, errorCode string) integrationsdk.RuntimeExecutionReceipt {
	return integrationsdk.RuntimeExecutionReceipt{
		EventID: request.EventID, MappingKey: request.MappingKey, ExecutionID: executionID,
		TargetType: strings.TrimSpace(request.Target.Type), Status: strings.TrimSpace(status), ErrorCode: strings.TrimSpace(errorCode),
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func cloneIntegrationTriggerInput(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+3)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func setIntegrationTriggerDefault(input map[string]any, key, value string) {
	if _, exists := input[key]; !exists {
		input[key] = value
	}
}
