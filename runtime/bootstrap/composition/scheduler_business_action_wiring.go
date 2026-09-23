package composition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

const schedulerManagedWorkloadPrefix = "scheduler:"
const schedulerBusinessActionMaximumPayloadBytes = schedule.MaximumDefinitionPayloadBytes

type schedulerBusinessActionAuthorization struct {
	DefinitionKey string `json:"definition_key"`
	ActionKey     string `json:"action_key"`
	ObjectKey     string `json:"object_key"`
	RunAsRole     string `json:"run_as_role"`
}

func schedulerBusinessActionAuthorizationVersion(definitionKey, actionKey, objectKey, runAsRole string) string {
	payload, _ := json.Marshal(schedulerBusinessActionAuthorization{
		DefinitionKey: strings.TrimSpace(definitionKey), ActionKey: strings.TrimSpace(actionKey),
		ObjectKey: strings.TrimSpace(objectKey), RunAsRole: strings.TrimSpace(runAsRole),
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

// SchedulerBusinessActionWorkloadBindings projects the exact authorization
// identity embedded in Scheduler targets. Schedule/status changes deliberately
// do not rotate this identity; changing Action, Object, or role does.
func SchedulerBusinessActionWorkloadBindings(definitions []schedulersdk.Definition) ([]workflowapplication.ManagedWorkloadBinding, error) {
	result := make([]workflowapplication.ManagedWorkloadBinding, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if strings.ToLower(strings.TrimSpace(definition.Target.Owner)) != "business_action" {
			continue
		}
		definitionKey := strings.TrimSpace(definition.Key)
		actionKey := strings.TrimSpace(definition.Target.Operation)
		objectKey := strings.TrimSpace(definition.Target.ObjectKey)
		roleKey := strings.TrimSpace(definition.Target.RunAsRole)
		if definitionKey == "" || actionKey == "" || objectKey == "" || roleKey == "" {
			return nil, fmt.Errorf("Scheduler business Action workload declaration is incomplete for %q", definitionKey)
		}
		workloadKey := schedulerManagedWorkloadPrefix + definitionKey
		if _, duplicate := seen[workloadKey]; duplicate {
			return nil, fmt.Errorf("duplicate Scheduler business Action workload %q", workloadKey)
		}
		seen[workloadKey] = struct{}{}
		result = append(result, workflowapplication.ManagedWorkloadBinding{
			WorkloadKey: workloadKey, DefinitionVersionID: schedulerBusinessActionAuthorizationVersion(definitionKey, actionKey, objectKey, roleKey),
			DefinitionVersion: 1, RoleKey: roleKey, ActionKeys: []string{actionKey},
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkloadKey < result[j].WorkloadKey })
	return result, nil
}

type schedulerManagedPrincipalRuntime interface {
	ResolveManagedWorkloadPrincipal(context.Context, workflowapplication.ManagedWorkloadExecution) (principalmodel.Principal, error)
}

type schedulerActionRuntime interface {
	Invoke(context.Context, actionmodel.ActionSource, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
}

type schedulerBusinessActionRuntime struct {
	principals schedulerManagedPrincipalRuntime
	actions    schedulerActionRuntime
}

func newSchedulerBusinessActionRuntime(records *runtimeAssembly) dispatchapplication.BusinessActionTargetRuntime {
	if records == nil {
		return schedulerBusinessActionRuntime{}
	}
	return schedulerBusinessActionRuntime{principals: records.workflowApplicationService, actions: records.actionService}
}

func (runtime schedulerBusinessActionRuntime) ExecuteBusinessActionTarget(ctx context.Context, request dispatchapplication.BusinessActionTargetRequest) (dispatchapplication.BusinessActionTargetReceipt, error) {
	definitionKey := strings.TrimSpace(request.DefinitionKey)
	actionKey := strings.TrimSpace(request.Operation)
	objectKey := strings.TrimSpace(request.ObjectKey)
	roleKey := strings.TrimSpace(request.RunAsRole)
	if runtime.actions == nil || runtime.principals == nil {
		return dispatchapplication.BusinessActionTargetReceipt{}, apperror.New(apperror.KindUnavailable, "backend.scheduler.business_action_runtime_unavailable", nil, nil)
	}
	if definitionKey == "" || actionKey == "" || objectKey == "" || roleKey == "" || strings.TrimSpace(request.ExecutionID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return dispatchapplication.BusinessActionTargetReceipt{}, apperror.New(apperror.KindBadRequest, "backend.scheduler.business_action_target_invalid", nil, nil)
	}
	payload, err := decodeSchedulerBusinessActionPayload(request.Payload)
	if err != nil {
		return dispatchapplication.BusinessActionTargetReceipt{}, apperror.New(apperror.KindBadRequest, "backend.scheduler.business_action_payload_invalid", nil, nil)
	}
	versionID := schedulerBusinessActionAuthorizationVersion(definitionKey, actionKey, objectKey, roleKey)
	principal, err := runtime.principals.ResolveManagedWorkloadPrincipal(ctx, workflowapplication.ManagedWorkloadExecution{
		WorkloadKey: schedulerManagedWorkloadPrefix + definitionKey, DefinitionVersionID: versionID, DefinitionVersion: 1, RoleKey: roleKey,
		ExecutionID: strings.TrimSpace(request.ExecutionID), SourceEventID: strings.TrimSpace(request.ExecutionID), IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	})
	if err != nil {
		return dispatchapplication.BusinessActionTargetReceipt{}, err
	}
	result, err := runtime.actions.Invoke(ctx, actionmodel.ActionSourceScheduler, actionmodel.ActionInvocation{
		PreventExecutionReclaim: true, ActionKey: actionKey, ObjectKey: objectKey, Input: payload, Principal: principal, Actor: principal,
		RequestID: strings.TrimSpace(request.IdempotencyKey), IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	})
	if err != nil {
		return dispatchapplication.BusinessActionTargetReceipt{}, err
	}
	return dispatchapplication.BusinessActionTargetReceipt{ID: result.InvocationID, Status: result.Status}, nil
}

func decodeSchedulerBusinessActionPayload(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	if len(raw) > schedulerBusinessActionMaximumPayloadBytes {
		return nil, fmt.Errorf("Scheduler business Action payload exceeds %d bytes", schedulerBusinessActionMaximumPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, fmt.Errorf("Scheduler business Action payload must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("Scheduler business Action payload must contain exactly one JSON object")
	}
	return payload, nil
}

var _ dispatchapplication.BusinessActionTargetRuntime = schedulerBusinessActionRuntime{}
var _ schedulerActionRuntime = (*actionapplication.ActionApplicationService)(nil)
