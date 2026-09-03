package action

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// SystemOperationHandler executes one Runtime-owned operation after the shared Action Application gates.
type SystemOperationHandler func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (ActionExecutionResult, error)

type SystemOperationBinding struct {
	Key     string
	Handler SystemOperationHandler
}

type SystemOperationHandlers struct {
	Create            SystemOperationHandler
	Update            SystemOperationHandler
	Delete            SystemOperationHandler
	Restore           SystemOperationHandler
	Transition        SystemOperationHandler
	ConditionalUpdate SystemOperationHandler
}

type SystemOperationExecutor struct {
	handlers         map[string]SystemOperationHandler
	validationErrors []error
}

func NewRuntimeSystemOperationExecutor(catalog *SystemOperationCatalog, handlers SystemOperationHandlers, extensions ...SystemOperationBinding) *SystemOperationExecutor {
	standard := []SystemOperationBinding{
		{Key: SystemOperationCreate, Handler: handlers.Create},
		{Key: SystemOperationUpdate, Handler: handlers.Update},
		{Key: SystemOperationDelete, Handler: handlers.Delete},
		{Key: SystemOperationRestore, Handler: handlers.Restore},
		{Key: SystemOperationTransition, Handler: handlers.Transition},
		{Key: SystemOperationConditionalUpdate, Handler: handlers.ConditionalUpdate},
	}
	return NewSystemOperationExecutor(catalog, append(append([]SystemOperationBinding(nil), extensions...), standard...)...)
}

func NewSystemOperationExecutor(catalog *SystemOperationCatalog, bindings ...SystemOperationBinding) *SystemOperationExecutor {
	result := &SystemOperationExecutor{handlers: map[string]SystemOperationHandler{}}
	descriptors := map[string]bool{}
	if catalog == nil {
		result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation catalog is required"))
	} else {
		for _, err := range catalog.ValidationErrors() {
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation catalog: %w", err))
		}
		for _, descriptor := range catalog.Descriptors() {
			descriptors[descriptor.Key] = true
		}
	}
	for _, binding := range bindings {
		key := strings.TrimSpace(binding.Key)
		switch {
		case key == "":
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation binding key is required"))
		case result.handlers[key] != nil:
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation binding %s is duplicated", key))
		case binding.Handler == nil:
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation %s has no handler", key))
		default:
			result.handlers[key] = binding.Handler
		}
		if key != "" && !descriptors[key] {
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation handler %s has no catalog descriptor", key))
		}
	}
	for key := range descriptors {
		if result.handlers[key] == nil {
			result.validationErrors = append(result.validationErrors, fmt.Errorf("system operation %s has no handler", key))
		}
	}
	sort.Slice(result.validationErrors, func(i, j int) bool { return result.validationErrors[i].Error() < result.validationErrors[j].Error() })
	return result
}

func (e *SystemOperationExecutor) execute(ctx context.Context, governed governedActionExecution) (ActionExecutionResult, error) {
	key := governed.entry.SystemOperation
	if e == nil || len(e.validationErrors) > 0 {
		return ActionExecutionResult{}, missingExecutorPort("system_operation:" + strings.TrimSpace(key))
	}
	handler, ok := e.handlers[strings.TrimSpace(key)]
	if !ok {
		return ActionExecutionResult{}, missingExecutorPort("system_operation:" + strings.TrimSpace(key))
	}
	return handler(ctx, governed.invocation, governed.entry.Definition, governed.payload)
}

func (e *SystemOperationExecutor) ValidationErrors() []error {
	if e == nil {
		return []error{fmt.Errorf("system operation executor is required")}
	}
	return append([]error(nil), e.validationErrors...)
}

type RecordSystemOperationDependencies struct {
	ObjectForKey          func(string) (definitionmodel.ObjectSchema, bool)
	PlanCreateMutation    func(context.Context, string, map[string]any, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	PlanUpdateMutation    func(context.Context, string, string, map[string]any, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	PlanDeleteMutation    func(context.Context, string, string, string, principalmodel.Principal) ([]transactionmodel.MutationPlan, error)
	PlanRestoreMutation   func(context.Context, string, string, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	PlanConditionalUpdate func(context.Context, string, string, transactionmodel.ConditionalUpdateInput, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
}

func NewRecordSystemOperationHandlers(dependencies RecordSystemOperationDependencies) SystemOperationHandlers {
	update := recordUpdateSystemOperation(dependencies)
	return SystemOperationHandlers{
		Create: func(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, payload map[string]any) (ActionExecutionResult, error) {
			if dependencies.PlanCreateMutation == nil {
				return ActionExecutionResult{}, missingExecutorPort("system_operation:plan_create")
			}
			plan, record, err := dependencies.PlanCreateMutation(ctx, action.ObjectKey, payload, "", ActionPersistencePrincipal(invocation.Principal, action, "create"))
			if err != nil {
				return ActionExecutionResult{}, err
			}
			result := actionmodel.ActionObjectResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, Status: "success", Message: "backend.action.executed", CreatedRecords: []actionmodel.ActionObjectRecordRef{{ObjectKey: action.ObjectKey, RecordID: record.ID}}}
			return ActionExecutionResult{Object: &result, Commits: []transactionmodel.RecordMutationCommit{plan.CanonicalCommit()}}, nil
		},
		Update: update,
		Transition: func(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, payload map[string]any) (ActionExecutionResult, error) {
			if dependencies.ObjectForKey == nil || dependencies.PlanUpdateMutation == nil {
				return ActionExecutionResult{}, missingExecutorPort("system_operation:plan_update")
			}
			object, ok := dependencies.ObjectForKey(action.ObjectKey)
			if !ok {
				return ActionExecutionResult{}, apperror.New(apperror.KindInternal, "backend.action.object_schema_missing", nil, map[string]string{"object": action.ObjectKey})
			}
			// A transition invoked without its state payload used to plan an
			// empty patch: the mutation committed nothing while the action
			// still reported success and wrote an "Executed action" audit
			// event. Fail closed instead of silently doing nothing.
			if len(actionservice.ActionDataPatch(object, payload)) == 0 {
				return ActionExecutionResult{}, apperror.New(apperror.KindBadRequest, "backend.action.transition_payload_required", nil, map[string]string{"action": action.Key, "object": action.ObjectKey})
			}
			return update(ctx, invocation, action, payload)
		},
		Delete: func(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, _ map[string]any) (ActionExecutionResult, error) {
			if dependencies.PlanDeleteMutation == nil {
				return ActionExecutionResult{}, missingExecutorPort("system_operation:plan_delete")
			}
			plans, err := dependencies.PlanDeleteMutation(ctx, action.ObjectKey, invocation.RecordID, "", ActionPersistencePrincipal(invocation.Principal, action, "delete"))
			if err != nil {
				return ActionExecutionResult{}, err
			}
			result := actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID, Message: "backend.action.executed"}
			return ActionExecutionResult{Record: &result, Commits: canonicalMutationCommits(plans)}, nil
		},
		Restore: func(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, _ map[string]any) (ActionExecutionResult, error) {
			if dependencies.PlanRestoreMutation == nil {
				return ActionExecutionResult{}, missingExecutorPort("system_operation:plan_restore")
			}
			plan, record, err := dependencies.PlanRestoreMutation(ctx, action.ObjectKey, invocation.RecordID, "", ActionPersistencePrincipal(invocation.Principal, action, "update"))
			if err != nil {
				return ActionExecutionResult{}, err
			}
			result := actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID, Message: "backend.action.executed", Record: record}
			return ActionExecutionResult{Record: &result, Commits: []transactionmodel.RecordMutationCommit{plan.CanonicalCommit()}}, nil
		},
		ConditionalUpdate: func(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, payload map[string]any) (ActionExecutionResult, error) {
			if dependencies.ObjectForKey == nil || dependencies.PlanConditionalUpdate == nil {
				return ActionExecutionResult{}, missingExecutorPort("system_operation:plan_conditional_update")
			}
			object, ok := dependencies.ObjectForKey(action.ObjectKey)
			if !ok {
				return ActionExecutionResult{}, apperror.New(apperror.KindInternal, "backend.action.object_schema_missing", nil, map[string]string{"object": action.ObjectKey})
			}
			patch := actionservice.ActionDataPatch(object, payload)
			if expected, ok := payload["expected_version"]; ok {
				patch["expected_version"] = expected
			}
			if expected := strings.TrimSpace(fmt.Sprint(payload["expected_updated_at"])); expected != "" && expected != "<nil>" {
				patch["expected_updated_at"] = expected
			}
			plan, record, err := dependencies.PlanConditionalUpdate(ctx, action.ObjectKey, invocation.RecordID, transactionmodel.ConditionalUpdateInput{Patch: patch}, ActionPersistencePrincipal(invocation.Principal, action, "update"))
			if err != nil {
				return ActionExecutionResult{}, err
			}
			result := actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID, Message: "backend.action.executed", Record: record}
			return ActionExecutionResult{Record: &result, Commits: []transactionmodel.RecordMutationCommit{plan.CanonicalCommit()}}, nil
		},
	}
}

func recordUpdateSystemOperation(dependencies RecordSystemOperationDependencies) SystemOperationHandler {
	return func(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, payload map[string]any) (ActionExecutionResult, error) {
		if dependencies.PlanUpdateMutation == nil {
			return ActionExecutionResult{}, missingExecutorPort("system_operation:plan_update")
		}
		plan, record, err := dependencies.PlanUpdateMutation(ctx, action.ObjectKey, invocation.RecordID, payload, ActionPersistencePrincipal(invocation.Principal, action, "update"))
		if err != nil {
			return ActionExecutionResult{}, err
		}
		result := actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID, Message: "backend.action.executed", Record: record}
		return ActionExecutionResult{Record: &result, Commits: []transactionmodel.RecordMutationCommit{plan.CanonicalCommit()}}, nil
	}
}

func canonicalMutationCommits(plans []transactionmodel.MutationPlan) []transactionmodel.RecordMutationCommit {
	commits := make([]transactionmodel.RecordMutationCommit, len(plans))
	for index := range plans {
		commits[index] = plans[index].CanonicalCommit()
	}
	return commits
}
