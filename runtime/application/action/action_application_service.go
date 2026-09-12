package action

import (
	"context"
	"errors"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/telemetry"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"go.opentelemetry.io/otel/attribute"
)

type ActionAuthorization struct {
	ObjectForAction func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
}

func (a ActionAuthorization) Validate(principal principalmodel.Principal, action definitionmodel.ActionSchema) error {
	if !ActionAllowed(principal, action) {
		return apperror.New(apperror.KindForbidden, "backend.action.permission_denied", nil, nil)
	}
	if a.ObjectForAction != nil {
		_, err := a.ObjectForAction(principal, action.ObjectKey, actionpolicy.ActionName(action))
		return err
	}
	return nil
}

type ActionAssurance struct {
	Validate func(context.Context, actionmodel.ActionInvocation) (map[string]string, error)
}

type ActionAudit struct {
	BuildSuccess  func(context.Context, definitionmodel.ActionSchema, actionmodel.ActionInvocation, actionmodel.ActionInvocationResult) auditmodel.AuditEvent
	BuildFailure  func(context.Context, definitionmodel.ActionSchema, actionmodel.ActionInvocation, actionmodel.ActionInvocationResult, error) []auditmodel.AuditEvent
	AppendAttempt func(context.Context, auditapplication.AuditAppendRequest) error
	Bulk          func(context.Context, actionmodel.ActionBulkResult, []string, principalmodel.Principal)
}

type ActionApplicationDependencies struct {
	Catalog          *ActionCatalog
	SystemOperations *SystemOperationExecutor
	BusinessHandlers *BusinessHandlerExecutor
	Authorization    ActionAuthorization
	Assurance        ActionAssurance
	UnitOfWork       *ActionUnitOfWorkManager
	Audit            ActionAudit
	ProjectRecord    func(context.Context, principalmodel.Principal, string, recordmodel.Record) (recordmodel.Record, error)
	ProjectOutput    func(context.Context, principalmodel.Principal, definitionmodel.ActionSchema, map[string]any) (map[string]any, error)
	// ExecuteCommittedWorkflows activates the Workflow intents an Action staged
	// inside its own transaction. It runs only after the commit succeeded, so a
	// crash here leaves the durable pending intent for the Workflow worker.
	ExecuteCommittedWorkflows func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
}

// ActionApplicationService is the single governed invocation boundary used by
// HTTP, Workflow, Automation, Scheduler, Integration, Agent and Bulk callers.
type ActionApplicationService struct {
	dependencies ActionApplicationDependencies
	bulk         *ActionBulkApplicationService
}

// governedActionExecution is created only after the Action Application has
// completed authorization, payload normalization, assurance and idempotency
// claim. Keeping this type private prevents other Runtime packages from
// calling a Business Handler with an ungoverned invocation.
type governedActionExecution struct {
	invocation  actionmodel.ActionInvocation
	entry       ActionCatalogEntry
	payload     map[string]any
	executionID string
	unitOfWork  *actionUnitOfWork
}

func NewActionApplication(dependencies ActionApplicationDependencies) *ActionApplicationService {
	if dependencies.UnitOfWork == nil {
		dependencies.UnitOfWork = NewActionUnitOfWorkManager(nil)
	}
	if dependencies.Audit.BuildSuccess == nil {
		dependencies.Audit.BuildSuccess = buildActionSuccessAudit
	}
	if dependencies.Audit.BuildFailure == nil {
		dependencies.Audit.BuildFailure = buildActionFailureAudits
	}
	service := &ActionApplicationService{dependencies: dependencies}
	service.bulk = NewActionBulkApplicationService(ActionBulkDependencies{
		Allowed: ActionAllowed,
		Actions: func(context.Context) []definitionmodel.ActionSchema { return dependencies.Catalog.Definitions() },
		ValidateObject: func(_ context.Context, principal principalmodel.Principal, objectKey string) error {
			if dependencies.Authorization.ObjectForAction == nil {
				return nil
			}
			_, err := dependencies.Authorization.ObjectForAction(principal, objectKey, "read")
			return err
		},
		Invoke: func(ctx context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			return service.Invoke(ctx, actionmodel.ActionSourceBulk, invocation)
		},
		AuditBulk: dependencies.Audit.Bulk,
		Execution: dependencies.UnitOfWork.executionRuntime(),
	})
	return service
}

func (s *ActionApplicationService) ReplaceDefinitions(actions []definitionmodel.ActionSchema) {
	if s != nil && s.dependencies.Catalog != nil {
		s.dependencies.Catalog.Replace(actions)
	}
}

// Definitions returns the effective published Action contracts after execution
// owner resolution. In particular, source-owned handlers contribute their
// backend-verified read/write effect set through the frozen Handler registry.
func (s *ActionApplicationService) Definitions() []definitionmodel.ActionSchema {
	if s == nil || s.dependencies.Catalog == nil {
		return nil
	}
	return s.dependencies.Catalog.Definitions()
}

func (s *ActionApplicationService) CatalogValidationErrors() []error {
	if s == nil {
		return nil
	}
	errors := s.dependencies.Catalog.ValidationErrors()
	errors = append(errors, s.dependencies.SystemOperations.ValidationErrors()...)
	errors = append(errors, s.dependencies.UnitOfWork.ValidationErrors()...)
	if s.dependencies.Catalog.HasBusinessHandlerOwner() {
		errors = append(errors, s.dependencies.BusinessHandlers.ValidationErrors()...)
	}
	return errors
}

func (s *ActionApplicationService) ActionsForObject(ctx context.Context, objectKey string, principal principalmodel.Principal) ([]definitionmodel.ActionSchema, error) {
	if err := actionAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	return s.bulk.ActionsForObject(ctx, objectKey, principal)
}

func (s *ActionApplicationService) ExecuteBulkAction(ctx context.Context, objectKey, actionKey string, request actionmodel.ActionBulkRequest, principal principalmodel.Principal) (actionmodel.ActionBulkResult, error) {
	return s.bulk.ExecuteBulkAction(ctx, objectKey, actionKey, request, principal)
}

func (s *ActionApplicationService) Invoke(ctx context.Context, source actionmodel.ActionSource, invocation actionmodel.ActionInvocation) (result actionmodel.ActionInvocationResult, err error) {
	if !actionSourceValid(source) {
		return actionmodel.ActionInvocationResult{}, apperror.New(apperror.KindBadRequest, "backend.action.source_invalid", nil, nil)
	}
	// The entrypoint owns provenance. Never trust a Source value carried by an
	// upstream DTO, because that would allow one ingress to impersonate another.
	invocation.Source = source
	invocation = ActionNormalizeInvocation(invocation)
	ctx, span := telemetry.StartUseCase(ctx, "action.invoke", attribute.String("action.key", invocation.ActionKey), attribute.String("object.key", invocation.ObjectKey))
	defer func() { telemetry.EndUseCase(span, err, "") }()
	var unitOfWork *actionUnitOfWork
	defer func() {
		if recovered := recover(); recovered != nil {
			if unitOfWork != nil {
				unitOfWork.rollBack(context.WithoutCancel(ctx))
			}
			result, err = s.failOwnedInvocation(
				context.WithoutCancel(ctx),
				unitOfWork,
				result,
				apperror.New(apperror.KindInternal, "backend.action.handler_panicked", errors.New("Action execution panicked"), nil),
				nil,
			)
		}
	}()
	if err := actionAuthorizeCommand(invocation.Principal); err != nil {
		return actionmodel.ActionInvocationResult{}, err
	}
	if invocation.ActionKey == "" {
		return actionmodel.ActionInvocationResult{}, apperror.New(apperror.KindBadRequest, "backend.action.key_required", nil, nil)
	}
	if invocation.IdempotencyKey == "" {
		return actionmodel.ActionInvocationResult{}, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"action": invocation.ActionKey, "source": string(source)})
	}
	if _, exists := invocation.Input["idempotency_key"]; exists {
		return actionmodel.ActionInvocationResult{}, apperror.New(apperror.KindBadRequest, "backend.validation.unknown_field", nil, map[string]string{"field": "idempotency_key", "object": invocation.ActionKey})
	}
	entry, ok := s.dependencies.Catalog.Entry(invocation.ActionKey)
	if !ok {
		return actionmodel.ActionInvocationResult{}, apperror.New(apperror.KindNotFound, "backend.action.not_found", nil, nil)
	}
	if entry.ResolutionError != nil {
		return actionmodel.ActionInvocationResult{}, actionOwnerResolutionError(entry)
	}
	action := entry.Definition
	if strings.TrimSpace(invocation.ObjectKey) == "" {
		invocation.ObjectKey = action.ObjectKey
	}
	if action.ObjectKey != invocation.ObjectKey {
		return s.failPreClaim(ctx, action, invocation, apperror.New(apperror.KindBadRequest, "backend.action.object_mismatch", nil, nil))
	}
	if invocation.RecordID == "" && !actionpolicy.ActionIsObjectKind(action.Kind) {
		return s.failPreClaim(ctx, action, invocation, apperror.New(apperror.KindBadRequest, "backend.action.object_action_required", nil, map[string]string{"action": action.Key}))
	}
	if invocation.RecordID != "" && !actionpolicy.ActionIsRecordKind(action.Kind) {
		return s.failPreClaim(ctx, action, invocation, apperror.New(apperror.KindBadRequest, "backend.action.record_action_required", nil, map[string]string{"action": action.Key}))
	}
	if err := validateActionTargetOrganizationInvocation(action, invocation); err != nil {
		return s.failPreClaim(ctx, action, invocation, err)
	}
	if err := s.dependencies.Authorization.Validate(invocation.Principal, action); err != nil {
		return s.failPreClaim(ctx, action, invocation, err)
	}
	payload, err := ActionNormalizePayload(action, invocation.Input)
	if err != nil {
		return s.failPreClaim(ctx, action, invocation, err)
	}
	invocation.Input = payload
	if assured, err := actionValidateInvocationAssurance(ctx, s.dependencies.Assurance.Validate, invocation); err != nil {
		return s.failPreClaim(ctx, action, invocation, err)
	} else {
		invocation = assured
	}
	invocationID := invocation.IdempotencyKey
	result = actionmodel.ActionInvocationResult{
		InvocationID: invocationID, Status: "running", Source: invocation.Source, AuditEvent: action.AuditEvent,
		AuditEvidence: map[string]string{"action_key": action.Key, "object_key": action.ObjectKey, "record_id": invocation.RecordID, "request_id": invocation.RequestID, "process_id": invocation.ProcessID, "node_id": invocation.NodeID},
	}
	var cached actionmodel.ActionInvocationResult
	var replay bool
	cached, unitOfWork, replay, err = s.dependencies.UnitOfWork.begin(ctx, invocation, action)
	if err != nil {
		return failInvocation(result, err)
	}
	if replay {
		return cached, nil
	}
	executed, err := s.execute(ctx, governedActionExecution{
		invocation: invocation, entry: entry, payload: payload, executionID: unitOfWork.executionID(), unitOfWork: unitOfWork,
	})
	if err != nil {
		unitOfWork.rollBack(ctx)
		return s.failOwnedInvocation(
			context.WithoutCancel(ctx),
			unitOfWork,
			result,
			err,
			s.dependencies.Audit.BuildFailure(ctx, action, invocation, result, err),
		)
	}
	executed.Commits, err = enforceActionOptimisticConcurrency(action, invocation, executed.Commits)
	if err != nil {
		unitOfWork.rollBack(ctx)
		return s.failOwnedInvocation(
			context.WithoutCancel(ctx),
			unitOfWork,
			result,
			err,
			s.dependencies.Audit.BuildFailure(ctx, action, invocation, result, err),
		)
	}
	executed, err = s.projectExecutionResult(ctx, action, invocation.Principal, executed)
	if err != nil {
		unitOfWork.rollBack(ctx)
		return s.failOwnedInvocation(context.WithoutCancel(ctx), unitOfWork, result, err, nil)
	}
	result.Record, result.Object = executed.Record, executed.Object
	result.Status = "success"
	if executed.Record != nil {
		result.Output = ActionRecordInvocationOutput(*executed.Record)
		if executed.Record.Record.ID != "" {
			result.RecordVersions = map[string]string{executed.Record.Record.ID: executed.Record.Record.UpdatedAt}
		}
	} else if executed.Object != nil {
		result.Status, result.Output = executed.Object.Status, ActionObjectInvocationOutput(*executed.Object)
	}
	// actionReceiptResult returns backend.action.result_invalid before any
	// malformed owner result can be committed.
	receiptResult, err := actionReceiptResult(result)
	if err != nil {
		unitOfWork.rollBack(ctx)
		return s.failOwnedInvocation(context.WithoutCancel(ctx), unitOfWork, result, err, nil)
	}
	if actionmodel.AcceptanceFailurePoint(ctx) == actionmodel.AcceptanceFailureBeforeCommit {
		unitOfWork.rollBack(ctx)
		return s.failOwnedInvocation(context.WithoutCancel(ctx), unitOfWork, result, apperror.New(apperror.KindInternal, actionmodel.AcceptanceFailureInjectedCode, nil, map[string]string{"action": action.Key, "point": actionmodel.AcceptanceFailureBeforeCommit}), nil)
	}
	auditEvent := s.dependencies.Audit.BuildSuccess(ctx, action, invocation, result)
	err = unitOfWork.commit(ctx, receiptResult, executed.Commits, []auditmodel.AuditEvent{auditEvent})
	if err != nil {
		return s.failOwnedInvocation(context.WithoutCancel(ctx), unitOfWork, result, err, nil)
	}
	if executed.PostCommit != nil {
		executed.PostCommit(&result)
	}
	s.executeCommittedWorkflowStarts(ctx, executed.Commits, invocation.Principal)
	return result, nil
}

func (s *ActionApplicationService) failPreClaim(ctx context.Context, action definitionmodel.ActionSchema, invocation actionmodel.ActionInvocation, failure error) (actionmodel.ActionInvocationResult, error) {
	if s.dependencies.Audit.AppendAttempt == nil {
		return actionmodel.ActionInvocationResult{}, failure
	}
	code := strings.TrimSpace(apperror.CodeOf(failure))
	result, event := "failed", "action_invocation_failed"
	if apperror.KindOf(failure) == apperror.KindForbidden {
		result, event = "denied", "action_invocation_denied"
	}
	idempotencyKey := ""
	if requestID := strings.TrimSpace(invocation.RequestID); requestID != "" {
		idempotencyKey = event + ":" + requestID
	}
	if err := s.dependencies.Audit.AppendAttempt(ctx, auditapplication.AuditAppendRequest{
		IdempotencyKey: idempotencyKey, Event: event, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID,
		Principal: invocation.Principal, Summary: "Action invocation rejected before execution",
		Metadata: map[string]any{
			"action_key": action.Key, "owner_source": invocation.Source, "result": result, "reason": code, "error_code": code,
		},
	}); err != nil {
		return actionmodel.ActionInvocationResult{}, apperror.New(apperror.KindInternal, "backend.action.audit_failed", err, map[string]string{"action": action.Key})
	}
	return actionmodel.ActionInvocationResult{}, failure
}

func (s *ActionApplicationService) executeCommittedWorkflowStarts(ctx context.Context, commits []transactionmodel.RecordMutationCommit, principal principalmodel.Principal) {
	if s.dependencies.ExecuteCommittedWorkflows == nil {
		return
	}
	intents := []workflowmodel.WorkflowExecution{}
	for _, commit := range commits {
		for _, start := range commit.WorkflowStarts {
			intents = append(intents, start.Intent)
		}
	}
	if len(intents) > 0 {
		s.dependencies.ExecuteCommittedWorkflows(ctx, intents, principal)
	}
}

func validateActionTargetOrganizationInvocation(action definitionmodel.ActionSchema, invocation actionmodel.ActionInvocation) error {
	targetID := strings.TrimSpace(invocation.TargetOrganizationID)
	if invocation.RecordID != "" {
		if targetID != "" {
			return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_forbidden", nil, nil)
		}
		return nil
	}
	if action.TargetOrganization == nil {
		if targetID != "" {
			return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_forbidden", nil, nil)
		}
		return nil
	}
	switch strings.TrimSpace(action.TargetOrganization.Source) {
	case definitionmodel.ActionTargetOrganizationSourceExplicit:
		if targetID == "" {
			return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_required", nil, nil)
		}
	case definitionmodel.ActionTargetOrganizationSourceExplicitOrSoleAuthorizedStore:
		// An explicit store remains caller-selectable for multi-store roles. When
		// omitted, Runtime resolves only an unambiguous authorized store; project
		// input and browser code never infer or write ownership.
	case definitionmodel.ActionTargetOrganizationSourceProvisionedStore:
		if targetID != "" {
			return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_forbidden", nil, nil)
		}
	case definitionmodel.ActionTargetOrganizationSourceDeliveredOrganizationUnit:
		if targetID != "" {
			return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_forbidden", nil, nil)
		}
	case definitionmodel.ActionTargetOrganizationSourceRecordOwner:
		return apperror.New(apperror.KindInternal, "backend.action.target_organization_contract_invalid", nil, map[string]string{"action": action.Key})
	default:
		return apperror.New(apperror.KindInternal, "backend.action.target_organization_contract_invalid", nil, map[string]string{"action": action.Key})
	}
	return nil
}

func (s *ActionApplicationService) execute(ctx context.Context, governed governedActionExecution) (ActionExecutionResult, error) {
	actionResource, actionOperation := definitionmodel.ActionPermissionSubject(governed.entry.Definition)
	ctx = recordmutation.WithMutationInvocation(ctx, recordmutation.MutationInvocation{
		Source: transactionmodel.MutationSourceAction, ActionKey: governed.entry.Definition.Key, IdempotencyKey: governed.invocation.IdempotencyKey,
		ActionResource: actionResource, ActionOperation: actionOperation,
		EffectAuthority: actionEffectAuthority(governed.entry.Definition.EffectSet), AssuranceEvidence: governed.invocation.AssuranceEvidence,
		WorkflowTriggers: []string{"action_executed:" + governed.entry.Definition.Key},
	})
	switch governed.entry.Owner {
	case ActionOwnerSystemOperation:
		var err error
		ctx, err = governed.unitOfWork.beginWriting(ctx)
		if err != nil {
			return ActionExecutionResult{}, err
		}
		return s.dependencies.SystemOperations.execute(ctx, governed)
	case ActionOwnerBusinessHandler:
		return s.dependencies.BusinessHandlers.execute(ctx, governed)
	default:
		return ActionExecutionResult{}, actionOwnerResolutionError(governed.entry)
	}
}

func invocationResultFromRecord(invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, record actionmodel.ActionResult) actionmodel.ActionInvocationResult {
	return actionmodel.ActionInvocationResult{InvocationID: invocation.IdempotencyKey, Status: "success", Source: invocation.Source, AuditEvent: action.AuditEvent, Output: ActionRecordInvocationOutput(record), Record: &record}
}

func invocationResultFromObject(invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, object actionmodel.ActionObjectResult) actionmodel.ActionInvocationResult {
	return actionmodel.ActionInvocationResult{InvocationID: invocation.IdempotencyKey, Status: object.Status, Source: invocation.Source, AuditEvent: action.AuditEvent, Output: ActionObjectInvocationOutput(object), Object: &object}
}
