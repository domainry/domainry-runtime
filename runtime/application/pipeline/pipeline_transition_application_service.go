package pipeline

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	pipelinepolicy "github.com/domainry/domainry-runtime/runtime/domain/pipeline/policy"
	pipelineprojection "github.com/domainry/domainry-runtime/runtime/domain/pipeline/projection"
	pipelineservice "github.com/domainry/domainry-runtime/runtime/domain/pipeline/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type PipelineTransitionDependencies struct {
	Pipeline          *PipelineApplicationService
	ActionAllowed     func(principalmodel.Principal, definitionmodel.ActionSchema) bool
	ObjectForAction   func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	GetRecord         func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	CanAccess         func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	CheckPrecondition func(context.Context, definitionmodel.ActionSchema, recordmodel.Record, principalmodel.Principal) error
	Audit             func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	PlanUpdate        func(context.Context, string, string, map[string]any, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	PlanCreate        func(context.Context, string, map[string]any, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	CommitPlans       func(context.Context, []transactionmodel.MutationPlan) error
	ExecuteWorkflows  func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
	Now               func() time.Time
}

// PipelineTransitionApplicationService owns only typed stage-transition
// legality and projection. Record defaults, Automation, validation, Audit,
// Outbox, Workflow intents and commit are delegated to the canonical Record
// Mutation Planner/Committer.
type PipelineTransitionApplicationService struct {
	dependencies PipelineTransitionDependencies
}

func NewPipelineTransitionApplicationService(dependencies PipelineTransitionDependencies) *PipelineTransitionApplicationService {
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	return &PipelineTransitionApplicationService{dependencies: dependencies}
}

func (s *PipelineTransitionApplicationService) IsAction(action definitionmodel.ActionSchema) bool {
	return pipelinepolicy.PipelineIsRuntimeAction(action)
}

func (s *PipelineTransitionApplicationService) Execute(ctx context.Context, objectKey, recordID string, action definitionmodel.ActionSchema, data map[string]any, principal principalmodel.Principal) (actionmodel.ActionResult, error) {
	result, plans, err := s.Plan(ctx, objectKey, recordID, action, data, principal)
	if err != nil {
		return actionmodel.ActionResult{}, err
	}
	if err := s.commitPlans(ctx, plans, principal); err != nil {
		return actionmodel.ActionResult{}, err
	}
	return result, nil
}

// Plan validates and prepares a pipeline transition without committing it.
// Action Application uses this path so its Runtime-owned UoW remains the only
// owner of the mutation and receipt transaction.
func (s *PipelineTransitionApplicationService) Plan(ctx context.Context, objectKey, recordID string, action definitionmodel.ActionSchema, data map[string]any, principal principalmodel.Principal) (actionmodel.ActionResult, []transactionmodel.MutationPlan, error) {
	if s.dependencies.ActionAllowed != nil && !s.dependencies.ActionAllowed(principal, action) {
		s.audit(ctx, "action_denied", objectKey, recordID, principal, "Action permission denied: "+action.Key, nil, map[string]any{"action_key": action.Key, "decision": "denied", "reason": "permission"})
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindForbidden, "backend.action.permission_denied", nil)
	}
	object, err := s.dependencies.ObjectForAction(principal, objectKey, actionpolicy.ActionName(action))
	if err != nil {
		s.audit(ctx, "action_denied", objectKey, recordID, principal, "Action data permission denied: "+action.Key, nil, map[string]any{"action_key": action.Key, "decision": "denied", "reason": "data_permission"})
		return actionmodel.ActionResult{}, nil, err
	}
	record, ok, err := s.dependencies.GetRecord(ctx, principal.WorkspaceID, object, recordID)
	if err != nil {
		return actionmodel.ActionResult{}, nil, pipelineTransitionInternal("get pipeline item", err)
	}
	if !ok {
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindNotFound, "backend.record.not_found", nil)
	}
	if s.dependencies.CanAccess != nil && !s.dependencies.CanAccess(principal, object, record) {
		s.audit(ctx, "action_denied", objectKey, recordID, principal, "Action record scope denied: "+action.Key, record.Data, map[string]any{"action_key": action.Key, "decision": "denied", "reason": "record_scope"})
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	if s.dependencies.CheckPrecondition != nil {
		if err := s.dependencies.CheckPrecondition(ctx, action, record, principal); err != nil {
			return actionmodel.ActionResult{}, nil, pipelineTransitionBadRequest(err)
		}
	}
	before := recordvalidation.RecordCloneData(record.Data)
	if err := pipelinepolicy.PipelineValidateExpectedVersion(before, data); err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	if action.Key == "pipeline_item.reopen" {
		return s.planReopen(ctx, object, record, before, action, data, principal)
	}
	return s.planAdvance(ctx, object, record, before, action, data, principal)
}

func (s *PipelineTransitionApplicationService) planAdvance(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, before map[string]any, action definitionmodel.ActionSchema, data map[string]any, principal principalmodel.Principal) (actionmodel.ActionResult, []transactionmodel.MutationPlan, error) {
	currentStageID := pipelineTransitionClean(record.Data["current_stage"])
	if currentStageID == "" {
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindBadRequest, "backend.pipeline.current_stage_required", nil)
	}
	pipelineID := pipelineTransitionClean(record.Data["pipeline"])
	toStage, err := s.destinationStage(ctx, principal.WorkspaceID, pipelineID, currentStageID, data)
	if err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	if strings.TrimSpace(toStage.ID) == "" {
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindBadRequest, "backend.pipeline.next_stage_not_found", nil)
	}
	if toStage.ID == currentStageID {
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindBadRequest, "backend.pipeline.same_stage", nil)
	}
	if err := s.dependencies.Pipeline.ValidateStagePermission(toStage, principal); err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	if err := pipelineservice.ValidateRequiredFields(toStage, record.Data); err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	return s.PlanPersist(ctx, object, record, before, action, data, currentStageID, toStage.ID, principal)
}

// advance remains the direct non-Action caller boundary. Runtime Action
// dispatch uses Plan and commits through ActionUnitOfWorkManager instead.
func (s *PipelineTransitionApplicationService) advance(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, before map[string]any, action definitionmodel.ActionSchema, data map[string]any, principal principalmodel.Principal) (actionmodel.ActionResult, error) {
	result, plans, err := s.planAdvance(ctx, object, record, before, action, data, principal)
	if err != nil {
		return actionmodel.ActionResult{}, err
	}
	if err := s.commitPlans(ctx, plans, principal); err != nil {
		return actionmodel.ActionResult{}, err
	}
	return result, nil
}

func (s *PipelineTransitionApplicationService) planReopen(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, before map[string]any, action definitionmodel.ActionSchema, data map[string]any, principal principalmodel.Principal) (actionmodel.ActionResult, []transactionmodel.MutationPlan, error) {
	currentStageID, pipelineID := pipelineTransitionClean(record.Data["current_stage"]), pipelineTransitionClean(record.Data["pipeline"])
	toStageID := strings.TrimSpace(actionpolicy.ActionFirstString(data, "to_stage", "next_stage", "stage"))
	if toStageID == "" {
		toStage, err := s.dependencies.Pipeline.FirstStage(ctx, principal.WorkspaceID, pipelineID)
		if err != nil {
			return actionmodel.ActionResult{}, nil, err
		}
		toStageID = toStage.ID
	}
	if toStageID == "" {
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindBadRequest, "backend.pipeline.next_stage_not_found", nil)
	}
	toStage, err := s.dependencies.Pipeline.GetStage(ctx, principal.WorkspaceID, toStageID)
	if err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	if currentStageID != "" && toStage.ID == currentStageID {
		return actionmodel.ActionResult{}, nil, pipelineTransitionError(apperror.KindBadRequest, "backend.pipeline.same_stage", nil)
	}
	if err := pipelineservice.ValidateStageBelongsToPipeline(toStage, pipelineID); err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	return s.PlanPersist(ctx, object, record, before, action, data, currentStageID, toStageID, principal)
}

func (s *PipelineTransitionApplicationService) reopen(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, before map[string]any, action definitionmodel.ActionSchema, data map[string]any, principal principalmodel.Principal) (actionmodel.ActionResult, error) {
	result, plans, err := s.planReopen(ctx, object, record, before, action, data, principal)
	if err != nil {
		return actionmodel.ActionResult{}, err
	}
	if err := s.commitPlans(ctx, plans, principal); err != nil {
		return actionmodel.ActionResult{}, err
	}
	return result, nil
}

func (s *PipelineTransitionApplicationService) destinationStage(ctx context.Context, workspaceID, pipelineID, currentStageID string, data map[string]any) (recordmodel.Record, error) {
	if toStageID := strings.TrimSpace(actionpolicy.ActionFirstString(data, "to_stage", "next_stage", "stage", "current_stage")); toStageID != "" {
		toStage, err := s.dependencies.Pipeline.GetStage(ctx, workspaceID, toStageID)
		if err != nil {
			return recordmodel.Record{}, err
		}
		if pipelineID == "" && currentStageID != "" {
			current, err := s.dependencies.Pipeline.GetStage(ctx, workspaceID, currentStageID)
			if err != nil {
				return recordmodel.Record{}, err
			}
			pipelineID = pipelineTransitionClean(current.Data["pipeline"])
		}
		if err := pipelineservice.ValidateStageBelongsToPipeline(toStage, pipelineID); err != nil {
			return recordmodel.Record{}, err
		}
		return toStage, nil
	}
	current, err := s.dependencies.Pipeline.GetStage(ctx, workspaceID, currentStageID)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if pipelineID == "" {
		pipelineID = pipelineTransitionClean(current.Data["pipeline"])
	}
	return s.dependencies.Pipeline.NextStage(ctx, workspaceID, pipelineID, current)
}

func (s *PipelineTransitionApplicationService) Persist(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, before map[string]any, action definitionmodel.ActionSchema, data map[string]any, fromStageID, toStageID string, principal principalmodel.Principal) (actionmodel.ActionResult, error) {
	result, plans, err := s.PlanPersist(ctx, object, record, before, action, data, fromStageID, toStageID, principal)
	if err != nil {
		return actionmodel.ActionResult{}, err
	}
	if err := s.commitPlans(ctx, plans, principal); err != nil {
		return actionmodel.ActionResult{}, err
	}
	return result, nil
}

func (s *PipelineTransitionApplicationService) PlanPersist(ctx context.Context, object definitionmodel.ObjectSchema, record recordmodel.Record, before map[string]any, action definitionmodel.ActionSchema, data map[string]any, fromStageID, toStageID string, principal principalmodel.Principal) (actionmodel.ActionResult, []transactionmodel.MutationPlan, error) {
	if s.dependencies.PlanUpdate == nil || s.dependencies.PlanCreate == nil {
		return actionmodel.ActionResult{}, nil, pipelineTransitionInternal("canonical mutation dependencies", nil)
	}
	toStage, err := s.dependencies.Pipeline.GetStage(ctx, principal.WorkspaceID, toStageID)
	if err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	now := s.dependencies.Now().UTC().Format(time.RFC3339)
	candidate := recordvalidation.RecordCloneData(record.Data)
	currentVersion, ok := actionpolicy.ActionPipelineInt(candidate["version"])
	if !ok {
		currentVersion = 1
	}
	candidate["current_stage"], candidate["version"], candidate["last_transition_at"] = toStageID, currentVersion+1, now
	if status := strings.TrimSpace(actionpolicy.ActionFirstString(data, "status")); status != "" {
		candidate["status"] = status
	}
	actionpolicy.ActionApplyPipelineCompletionFields(candidate, toStage, data, now, action.Key == "pipeline_item.reopen")
	_ = s.dependencies.Pipeline.ApplyStageSLA(candidate, toStage, now, true)
	patch := pipelineprojection.PipelineTransitionChangedPatch(object, before, candidate)
	historyData := map[string]any{
		"pipeline_item": record.ID, "from_stage": fromStageID, "to_stage": toStageID,
		"actor": principal.UserID, "owner": pipelineTransitionValueOr(principal.UserID, "system"),
		"note": actionpolicy.ActionFirstString(data, "note", "comment"), "reason": actionpolicy.ActionFirstString(data, "reason"),
		"field_changes": pipelineprojection.PipelineTransitionFieldChangesJSON(object, before, candidate), "transitioned_at": now,
	}
	ctx = pipelineMutationContext(ctx, action, object.Key, patch, historyData)
	itemPlan, plannedRecord, err := s.dependencies.PlanUpdate(ctx, object.Key, record.ID, patch, principal)
	if err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	historyPlan, _, err := s.dependencies.PlanCreate(ctx, "pipeline_item_history", historyData, "", principal)
	if err != nil {
		return actionmodel.ActionResult{}, nil, err
	}
	plans := []transactionmodel.MutationPlan{itemPlan, historyPlan}
	intents := pipelineCommittedWorkflowIntents(plans)
	return actionmodel.ActionResult{
		ActionKey: action.Key, ObjectKey: object.Key, RecordID: plannedRecord.ID,
		Message: "backend.pipeline.transitioned", Record: recordpolicy.RecordFilterReadable(principal, object, plannedRecord),
		TriggeredWorkflows: pipelineWorkflowSummaries(intents),
	}, plans, nil
}

func (s *PipelineTransitionApplicationService) commitPlans(ctx context.Context, plans []transactionmodel.MutationPlan, principal principalmodel.Principal) error {
	if s.dependencies.CommitPlans == nil {
		return pipelineTransitionInternal("canonical mutation dependencies", nil)
	}
	if err := s.dependencies.CommitPlans(ctx, plans); err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
			return pipelineTransitionError(apperror.KindConflict, "backend.record.version_conflict", err)
		}
		return pipelineTransitionInternal("commit pipeline transition", err)
	}
	intents := pipelineCommittedWorkflowIntents(plans)
	if s.dependencies.ExecuteWorkflows != nil && len(intents) > 0 {
		s.dependencies.ExecuteWorkflows(ctx, intents, principal)
	}
	return nil
}

func pipelineMutationContext(ctx context.Context, action definitionmodel.ActionSchema, objectKey string, patch, history map[string]any) context.Context {
	authority := map[string][]string{objectKey: mutationFields(patch), "pipeline_item_history": mutationFields(history)}
	return recordmutation.WithMutationInvocation(ctx, recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: action.Key, EffectAuthority: authority,
		WorkflowTriggers: []string{"action_executed:" + action.Key},
	})
}

func mutationFields(data map[string]any) []string {
	fields := make([]string, 0, len(data))
	for field := range data {
		fields = append(fields, field)
	}
	return fields
}

func pipelineCommittedWorkflowIntents(plans []transactionmodel.MutationPlan) []workflowmodel.WorkflowExecution {
	var intents []workflowmodel.WorkflowExecution
	for _, plan := range plans {
		intents = append(intents, plan.CanonicalCommit().WorkflowIntents...)
	}
	return intents
}

func pipelineWorkflowSummaries(intents []workflowmodel.WorkflowExecution) []workflowmodel.WorkflowRunSummary {
	summaries := make([]workflowmodel.WorkflowRunSummary, 0, len(intents))
	for _, intent := range intents {
		summaries = append(summaries, workflowmodel.WorkflowRunSummary{WorkflowKey: intent.WorkflowKey, Status: intent.Status, Action: intent.Action, ExecutionID: intent.ID, Message: intent.Message})
	}
	return summaries
}

func (s *PipelineTransitionApplicationService) audit(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, metadata map[string]any) {
	if s != nil && s.dependencies.Audit != nil {
		s.dependencies.Audit(ctx, event, objectKey, recordID, principal, summary, before, nil, metadata)
	}
}
