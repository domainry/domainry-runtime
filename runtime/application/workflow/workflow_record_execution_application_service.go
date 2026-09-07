package workflow

import (
	"encoding/base64"
	"encoding/json"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/telemetry"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	"go.opentelemetry.io/otel/attribute"
)

func (s *WorkflowApplicationService) triggeredWorkflows(ctx context.Context, objectKey string, record recordmodel.Record, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowRunSummary, error) {
	return s.triggeredWorkflowsWithChange(ctx, objectKey, record, nil, principal, trigger)
}

func (s *WorkflowApplicationService) TriggeredWorkflows(ctx context.Context, objectKey string, record recordmodel.Record, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowRunSummary, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return nil, err
	}
	return s.triggeredWorkflows(ctx, objectKey, record, principal, trigger)
}

func (s *WorkflowApplicationService) executeCommittedWorkflowIntents(ctx context.Context, intents []workflowmodel.WorkflowExecution, principal principalmodel.Principal) {
	// Without an active lifecycle context or worker repository, leave the
	// already-durable pending intent for the polling worker.
	if ctx == nil || s == nil || s.workerRepo == nil {
		return
	}
	for _, intent := range intents {
		workflow, ok := s.registry.Get(intent.WorkflowKey)
		if !ok {
			continue
		}
		claimed := intent
		claimed.Status, claimed.UpdatedAt = "running", time.Now().UTC().Format(time.RFC3339)
		claimOK := true
		var claimErr error
		claimOK, claimErr = s.workerRepo.UpdateExecutionWhere(ctx, principal.WorkspaceID, claimed, map[string]any{"status": "pending", "updated_at": intent.UpdatedAt})
		if claimErr != nil || !claimOK {
			continue
		}
		execution, err := s.executeWorkflowAttempt(ctx, workflow, workflowpolicy.WorkflowCloneMap(intent.Payload), principal, intent.Trigger, 1, true)
		claimed.Result = workflowpolicy.WorkflowCloneMap(claimed.Result)
		claimed.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			workflowpolicy.WorkflowMarkFailed(&claimed, workflow, err, time.Now().UTC())
		} else {
			claimed.Status, claimed.Message = "skipped", "workflow.message.retryContinued"
			claimed.Result["continued_execution_id"], claimed.Result["continued_status"] = execution.ID, execution.Status
		}
		_ = s.workerRepo.UpdateExecution(ctx, principal.WorkspaceID, claimed)
	}
}

func (s *WorkflowApplicationService) ExecuteCommittedWorkflowIntents(ctx context.Context, intents []workflowmodel.WorkflowExecution, principal principalmodel.Principal) {
	if workflowAuthorizeCommand(principal) != nil {
		return
	}
	s.executeCommittedWorkflowIntents(ctx, intents, principal)
}

func (s *WorkflowApplicationService) triggeredWorkflowsWithChange(ctx context.Context, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowRunSummary, error) {
	out := []workflowmodel.WorkflowRunSummary{}
	for _, workflow := range s.registry.List() {
		if !workflow.Enabled || !workflowpolicy.WorkflowMatchesRecordEvent(ctx, workflow, objectKey, record.Data, before, trigger) {
			continue
		}
		payload := workflowPayloadForRecordChange(objectKey, record, before, trigger)
		if principal.RequestID != "" {
			payload["request_id"] = principal.RequestID
		}
		if principal.UserID != "" {
			payload["initiating_user_id"] = principal.UserID
		}
		if principal.RoleKey != "" {
			payload["initiating_role_key"] = principal.RoleKey
		}
		execution, err := s.executeWorkflow(ctx, workflow, payload, principal, trigger)
		if err != nil {
			return nil, err
		}
		out = append(out, workflowmodel.WorkflowRunSummary{WorkflowKey: workflow.Key, Name: workflow.Name, Status: execution.Status, Action: workflow.Action, ExecutionID: execution.ID, Message: execution.Message})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkflowKey < out[j].WorkflowKey })
	return out, nil
}

func (s *WorkflowApplicationService) executeWorkflow(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger string) (workflowmodel.WorkflowExecution, error) {
	return s.executeWorkflowAttempt(ctx, workflow, payload, principal, trigger, 1, false)
}

func (s *WorkflowApplicationService) executeWorkflowWithIdempotencyKey(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger, idempotencyKey string) (workflowmodel.WorkflowExecution, error) {
	return s.executeWorkflowAttemptWithIdempotencyKey(ctx, workflow, payload, principal, trigger, idempotencyKey, 1, false)
}

func (s *WorkflowApplicationService) ExecuteWorkflow(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger string) (workflowmodel.WorkflowExecution, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowExecution{}, err
	}
	return s.executeWorkflow(ctx, workflow, payload, principal, trigger)
}

func (s *WorkflowApplicationService) executeWorkflowAttempt(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger string, attempt int, ignoreIdempotency bool) (execution workflowmodel.WorkflowExecution, err error) {
	return s.executeWorkflowAttemptWithIdempotencyKey(ctx, workflow, payload, principal, trigger, workflowpolicy.WorkflowIdempotencyKey(workflow, payload), attempt, ignoreIdempotency)
}

func (s *WorkflowApplicationService) executeWorkflowAttemptWithIdempotencyKey(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger, idempotencyKey string, attempt int, ignoreIdempotency bool) (execution workflowmodel.WorkflowExecution, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "workflow.execute", attribute.String("workflow.key", workflow.Key), attribute.String("workflow.trigger", trigger))
	defer func() { telemetry.EndUseCase(span, err, execution.Status) }()
	if workflow.Graph == nil || workflow.Graph.Version != 2 || len(workflow.Graph.Nodes) == 0 {
		return workflowmodel.WorkflowExecution{}, badRequest("backend.workflow.graph_v2_required", "workflow", strings.TrimSpace(workflow.Key))
	}
	return s.executeWorkflowGraphProcessWithIdempotencyKey(ctx, workflow, payload, principal, trigger, idempotencyKey, attempt, ignoreIdempotency)
}

func (s *WorkflowApplicationService) executeGlobalScheduledWorkflow(ctx context.Context, workflow definitionmodel.WorkflowSchema, principal principalmodel.Principal, scheduledFor time.Time) (workflowmodel.WorkflowExecution, bool, error) {
	return s.executeGlobalScheduledWorkflowWithKey(ctx, workflow, principal, scheduledFor, "")
}

func (s *WorkflowApplicationService) executeGlobalScheduledWorkflowWithKey(ctx context.Context, workflow definitionmodel.WorkflowSchema, principal principalmodel.Principal, scheduledFor time.Time, callbackIdempotencyKey string) (workflowmodel.WorkflowExecution, bool, error) {
	payload := map[string]any{"scheduled_at": scheduledFor.UTC().Format(time.RFC3339Nano)}
	key := scheduledWorkflowExecutionKey(workflow, payload, callbackIdempotencyKey, "")
	execution, err := s.executeWorkflowAttemptWithIdempotencyKey(ctx, workflow, payload, principal, "scheduled:"+workflow.Key, key, 1, false)
	if err != nil {
		switch apperror.CodeOf(err) {
		case idempotency.ErrorCodeKeyReused, idempotency.ErrorCodeInProgress:
			return workflowmodel.WorkflowExecution{}, false, nil
		}
		return workflowmodel.WorkflowExecution{}, false, err
	}
	return execution, execution.Status != "duplicate" || strings.TrimSpace(callbackIdempotencyKey) != "", nil
}

func scheduledWorkflowExecutionKey(workflow definitionmodel.WorkflowSchema, payload map[string]any, callbackIdempotencyKey, recordID string) string {
	callbackIdempotencyKey = strings.TrimSpace(callbackIdempotencyKey)
	if callbackIdempotencyKey == "" {
		return workflowpolicy.WorkflowIdempotencyKey(workflow, payload)
	}
	return workflowCommandKey("scheduled.callback", strings.TrimSpace(workflow.Key)+":"+strings.TrimSpace(recordID), callbackIdempotencyKey, map[string]any{"record_id": strings.TrimSpace(recordID)})
}

type scheduledWorkflowCheckpoint struct {
	Version     int    `json:"v"`
	WorkflowKey string `json:"workflow_key,omitempty"`
	ObjectKey   string `json:"object_key,omitempty"`
	AfterID     string `json:"after_id,omitempty"`
}

func encodeScheduledWorkflowCheckpoint(value scheduledWorkflowCheckpoint) string {
	value.Version = 1
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeScheduledWorkflowCheckpoint(value string) (scheduledWorkflowCheckpoint, error) {
	if strings.TrimSpace(value) == "" {
		return scheduledWorkflowCheckpoint{Version: 1}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return scheduledWorkflowCheckpoint{}, workflowScheduledCheckpointInvalid(err)
	}
	var checkpoint scheduledWorkflowCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil || checkpoint.Version != 1 {
		return scheduledWorkflowCheckpoint{}, workflowScheduledCheckpointInvalid(err)
	}
	return checkpoint, nil
}

func workflowScheduledCheckpointInvalid(err error) error {
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.workflow.scheduled_checkpoint_invalid", Err: err}
}

// ProcessScheduledWorkflowWindowPage traverses at most limit candidate records
// and returns an opaque durable continuation. This is the Scheduler-owned run
// integration boundary; ordinary Workflow continuation polling keeps using the
// existing execution worker path.
func (s *WorkflowApplicationService) ProcessScheduledWorkflowWindowPage(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, checkpointValue string, principal principalmodel.Principal) (workflowmodel.WorkflowScheduledPage, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowScheduledPage{}, err
	}
	if limit <= 0 {
		limit = 25
	} else if limit > 500 {
		limit = 500
	}
	checkpoint, err := decodeScheduledWorkflowCheckpoint(checkpointValue)
	if err != nil {
		return workflowmodel.WorkflowScheduledPage{}, err
	}
	targetKey = strings.TrimPrefix(strings.TrimSpace(targetKey), "scheduled:")
	if targetKey == "*" {
		targetKey = ""
	}
	registered := s.registry.List()
	workflows := make([]definitionmodel.WorkflowSchema, 0, len(registered))
	for _, workflow := range registered {
		if workflow.Enabled && workflow.TriggerContract != nil && strings.TrimSpace(workflow.TriggerContract.Type) == "scheduled" && (targetKey == "" || workflow.Key == targetKey) {
			workflows = append(workflows, workflow)
		}
	}
	if targetKey != "" && len(workflows) == 0 {
		return workflowmodel.WorkflowScheduledPage{}, notFound("backend.workflow.scheduled_target_not_found", "workflow", targetKey)
	}
	sort.Slice(workflows, func(i, j int) bool { return workflows[i].Key < workflows[j].Key })
	page := workflowmodel.WorkflowScheduledPage{WorkflowProcessResult: workflowmodel.WorkflowProcessResult{Executions: []workflowmodel.WorkflowExecution{}}}
	resumeReached := checkpoint.WorkflowKey == ""
	for _, workflow := range workflows {
		if !resumeReached {
			if workflow.Key != checkpoint.WorkflowKey {
				continue
			}
			resumeReached = true
		}
		objectKeys := workflowpolicy.WorkflowTriggerObjectKeys(workflow)
		if len(objectKeys) == 0 {
			if checkpoint.WorkflowKey == workflow.Key && checkpoint.ObjectKey != "" {
				return workflowmodel.WorkflowScheduledPage{}, workflowScheduledCheckpointInvalid(nil)
			}
			execution, completed, executeErr := s.executeGlobalScheduledWorkflow(ctx, workflow, principal, scheduledFor.UTC())
			if executeErr != nil {
				return workflowmodel.WorkflowScheduledPage{}, executeErr
			}
			if completed {
				page.Executions = append(page.Executions, execution)
				page.Processed++
			}
			continue
		}
		scanPrincipal, resolveErr := s.workflowPrincipal(ctx, workflow, principal)
		if resolveErr != nil {
			return workflowmodel.WorkflowScheduledPage{}, resolveErr
		}
		for _, objectKey := range objectKeys {
			if checkpoint.WorkflowKey == workflow.Key && checkpoint.ObjectKey != "" && objectKey != checkpoint.ObjectKey {
				continue
			}
			if err := ctx.Err(); err != nil {
				return workflowmodel.WorkflowScheduledPage{}, err
			}
			object, ok := s.schemaMap(ctx)[objectKey]
			if !ok {
				checkpoint = scheduledWorkflowCheckpoint{}
				continue
			}
			afterID := ""
			if checkpoint.WorkflowKey == workflow.Key && checkpoint.ObjectKey == objectKey {
				afterID = checkpoint.AfterID
			}
			for page.Scanned < limit {
				remaining := limit - page.Scanned
				pageSize := min(200, remaining)
				result, listErr := s.recordReader.ListWorkflowRecords(ctx, scanPrincipal.WorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: pageSize, SkipTotal: true, AfterID: afterID, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}}, scanPrincipal)
				if listErr != nil {
					return workflowmodel.WorkflowScheduledPage{}, internalError("list records for scheduled workflow", listErr)
				}
				for _, record := range result.Items {
					page.Scanned++
					afterID = record.ID
					if workflowpolicy.WorkflowConditionMatches(ctx, workflow, record.Data) {
						payload := workflowPayloadForRecord(objectKey, record)
						payload["scheduled_at"] = scheduledFor.UTC().Format(time.RFC3339Nano)
						execution, executeErr := s.executeWorkflowAttempt(ctx, workflow, payload, principal, "scheduled:"+workflow.Key, 1, false)
						if executeErr != nil {
							if code := apperror.CodeOf(executeErr); code != idempotency.ErrorCodeKeyReused && code != idempotency.ErrorCodeInProgress {
								return workflowmodel.WorkflowScheduledPage{}, executeErr
							}
						} else if execution.Status != "duplicate" {
							page.Executions = append(page.Executions, execution)
							page.Processed++
						}
					}
					if page.Scanned >= limit {
						page.Checkpoint = encodeScheduledWorkflowCheckpoint(scheduledWorkflowCheckpoint{WorkflowKey: workflow.Key, ObjectKey: objectKey, AfterID: afterID})
						return page, nil
					}
				}
				if !result.HasNext || len(result.Items) == 0 {
					break
				}
			}
			checkpoint = scheduledWorkflowCheckpoint{}
		}
	}
	if !resumeReached {
		return workflowmodel.WorkflowScheduledPage{}, workflowScheduledCheckpointInvalid(nil)
	}
	page.Complete = true
	return page, nil
}
