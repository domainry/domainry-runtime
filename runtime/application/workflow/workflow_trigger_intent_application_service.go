package workflow

import (
	"context"
	"fmt"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func WorkflowPrepareTriggerIntents(ctx context.Context, workflows map[string]definitionmodel.WorkflowSchema, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, []workflowmodel.WorkflowRunSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	intents := []workflowmodel.WorkflowExecution{}
	summaries := []workflowmodel.WorkflowRunSummary{}
	for _, workflow := range workflows {
		if !workflow.Enabled || !workflowpolicy.WorkflowMatchesRecordEvent(ctx, workflow, objectKey, record.Data, before, trigger) {
			continue
		}
		if workflow.Graph == nil || workflow.Graph.Version != 2 || len(workflow.Graph.Nodes) == 0 {
			return nil, nil, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.workflow.graph_v2_required", Params: map[string]string{"workflow": strings.TrimSpace(workflow.Key)}}
		}
		payload := WorkflowPayloadForRecordChange(objectKey, record, before, trigger)
		if principal.RequestID != "" {
			payload["request_id"] = principal.RequestID
		}
		if principal.UserID != "" {
			payload["initiating_user_id"] = principal.UserID
		}
		if principal.RoleKey != "" {
			payload["initiating_role_key"] = principal.RoleKey
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		intent := workflowmodel.WorkflowExecution{
			ID: workflowTriggerIntentID(ctx), WorkflowKey: workflow.Key, Name: workflow.Name, Trigger: trigger,
			Status: "pending", ActionType: "workflow_graph", Action: workflowpolicy.WorkflowCloneMap(workflow.Action), Payload: payload,
			Result: map[string]any{"transactional_intent": true}, ObjectKey: objectKey, RecordID: record.ID,
			ActorID: principal.UserID, RunAs: workflowpolicy.WorkflowRunAs(workflow), IdempotencyKey: workflowpolicy.WorkflowIdempotencyKey(workflow, payload),
			Attempt: 0, MaxAttempts: workflowpolicy.WorkflowMaxAttempts(workflow), Message: "workflow.message.queued", CreatedAt: now, UpdatedAt: now,
		}
		intents = append(intents, intent)
		summaries = append(summaries, workflowmodel.WorkflowRunSummary{WorkflowKey: workflow.Key, Name: workflow.Name, Status: intent.Status, Action: workflow.Action, ExecutionID: intent.ID, Message: intent.Message})
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].WorkflowKey < intents[j].WorkflowKey })
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].WorkflowKey < summaries[j].WorkflowKey })
	return intents, summaries, nil
}

func workflowTriggerIntentID(ctx context.Context) string {
	_ = ctx
	return fmt.Sprintf("intent_%d", time.Now().UnixNano())
}
