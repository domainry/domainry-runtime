package changeplan

import (
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

	"context"
	"encoding/json"
	"errors"
	"sort"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func businessReferenceResourceType(resourceType string) string {
	return changeplanpolicy.ChangePlanCanonicalResourceType(resourceType)
}

type Runtime interface {
	CanonicalizeMetadataCandidate(context.Context, []metadatamodel.MetadataDefinitionMutation) ([]metadatamodel.MetadataDefinitionMutation, error)
	ReloadMetadata(context.Context, principalmodel.Principal) (string, error)
}

type AcceptanceScenarioRuntime interface {
	SimulateActionCandidate(context.Context, string, metadatamodel.MetadataDefinitionUpsertRequest, map[string]any, map[string]any, principalmodel.Principal) (AcceptanceScenarioRuntimeResult, error)
}

type AcceptanceScenarioRuntimeResult struct {
	Valid              bool
	SideEffectFree     bool
	ExecutionPerformed bool
	ErrorCodes         []string
	Payload            []byte
}

// ChangePlanApplicationService owns business change-plan lifecycle behavior.
type ChangePlanApplicationService struct {
	repository changeplanrepository.ChangePlanRepository
	operations changeplanrepository.ChangePlanOperationRepository
	metadata   metadatarepository.DefinitionMutationRepository
	audit      auditrepository.AuditEventWriterRepository
	runtime    Runtime
	scenarios  AcceptanceScenarioRuntime
}

func NewChangePlanApplicationService(repository changeplanrepository.ChangePlanRepository, metadataRepository metadatarepository.DefinitionMutationRepository, auditRepository auditrepository.AuditEventWriterRepository, runtime Runtime, scenarioRuntime ...AcceptanceScenarioRuntime) *ChangePlanApplicationService {
	operations, _ := repository.(changeplanrepository.ChangePlanOperationRepository)
	service := &ChangePlanApplicationService{repository: repository, operations: operations, metadata: metadataRepository, audit: auditRepository, runtime: runtime}
	if len(scenarioRuntime) > 0 {
		service.scenarios = scenarioRuntime[0]
	}
	return service
}

func changePlanAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); !principal.Known || err != nil {
		return changePlanError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func changePlanAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); !principal.Known || err != nil {
		return changePlanError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func badRequest(code string, params ...string) error {
	return changePlanError(apperror.KindBadRequest, code, nil, params...)
}

func forbidden(code string, params ...string) error {
	return changePlanError(apperror.KindForbidden, code, nil, params...)
}

func notFound(code string, params ...string) error {
	return changePlanError(apperror.KindNotFound, code, nil, params...)
}

func conflict(code string, params ...string) error {
	return changePlanError(apperror.KindConflict, code, nil, params...)
}

func internalError(operation string, err error) error {
	return changePlanError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func changePlanError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}

func ErrorCodeOf(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode()
	}
	return "backend.internal"
}

func wrapMetadataError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	var versionConflict *metadatamodel.MetadataDefinitionConflictError
	if errors.As(err, &versionConflict) {
		return conflict("backend.metadata.definition_version_conflict", "resource_type", versionConflict.ResourceType, "resource_key", versionConflict.ResourceKey, "expected_hash", versionConflict.ExpectedHash, "current_hash", versionConflict.CurrentHash)
	}
	return internalError("apply metadata definition mutations", err)
}

func buildAuditEvent(event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadataValues map[string]any) auditmodel.AuditEvent {
	metadataValues = recordvalidation.RecordCloneData(metadataValues)
	if principal.RequestID != "" {
		metadataValues["request_id"] = principal.RequestID
	}
	if principal.UserID != "" {
		metadataValues["actor_id"] = principal.UserID
	}
	if principal.RoleKey != "" {
		metadataValues["role_key"] = principal.RoleKey
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	metadataValues["workspace_id"] = workspaceID
	now := time.Now().UTC()
	actorID := strings.TrimSpace(principal.UserID)
	if actorID == "" {
		actorID = "system"
	}
	return auditmodel.AuditEvent{ID: auditmodel.NewEventID(now), WorkspaceID: workspaceID, Event: event, ObjectKey: objectKey, RecordID: recordID, ActorID: actorID, RoleKey: principal.RoleKey, Summary: summary, Before: recordvalidation.RecordCloneData(before), After: recordvalidation.RecordCloneData(after), Metadata: metadataValues, CreatedAt: now.Format(time.RFC3339)}
}
func (s *ChangePlanApplicationService) SimulateDraftScenarios(ctx context.Context, planID string, expectedRevision int, principal principalmodel.Principal) (changeplanmodel.BusinessAcceptanceScenarioSimulation, error) {
	if err := changePlanAuthorizeQuery(principal); err != nil {
		return changeplanmodel.BusinessAcceptanceScenarioSimulation{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.BusinessAcceptanceScenarioSimulation{}, forbidden("auth.permission_denied")
	}
	draft, plan, err := s.draftForScenarioSimulation(ctx, planID, expectedRevision, principal)
	if err != nil {
		return changeplanmodel.BusinessAcceptanceScenarioSimulation{}, err
	}
	mutations, _, err := s.metadataChangePlanMutations(ctx, plan, principal)
	if err != nil {
		return changeplanmodel.BusinessAcceptanceScenarioSimulation{}, err
	}
	result, err := s.simulateAcceptanceScenarios(ctx, plan, mutations, principal)
	result.DraftRevision = draft.Revision
	return result, err
}

func (s *ChangePlanApplicationService) draftForScenarioSimulation(ctx context.Context, planID string, expectedRevision int, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, changeplanmodel.BusinessSystemChangePlan, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, badRequest("backend.change_plan.plan_id_required")
	}
	draft, found, err := s.repository.GetDraft(ctx, principal.WorkspaceID, planID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, internalError("get domain change plan draft", err)
	}
	if !found {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, notFound("backend.change_plan.draft_not_found")
	}
	if expectedRevision <= 0 || draft.Revision != expectedRevision || (draft.Status != "draft" && draft.Status != "in_review" && draft.Status != "approved") {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", planID)
	}
	var plan changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &plan); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, badRequest("backend.change_plan.draft_invalid")
	}
	if strings.TrimSpace(plan.PlanID) != planID {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, conflict("backend.change_plan.draft_identity_mismatch")
	}
	return draft, plan, nil
}

func (s *ChangePlanApplicationService) simulateAcceptanceScenarios(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, mutations []metadatamodel.MetadataDefinitionMutation, principal principalmodel.Principal) (changeplanmodel.BusinessAcceptanceScenarioSimulation, error) {
	result := changeplanmodel.BusinessAcceptanceScenarioSimulation{PlanID: plan.PlanID, DraftRevision: plan.DraftRevision, SideEffectFree: true, Passed: true, Results: []changeplanmodel.BusinessAcceptanceScenarioResult{}}
	if len(plan.AcceptanceScenarios) == 0 {
		return result, nil
	}
	if s.scenarios == nil {
		return result, internalError("simulate domain change plan acceptance scenarios", nil)
	}
	actions := map[string]metadatamodel.MetadataDefinitionMutation{}
	for _, mutation := range mutations {
		if mutation.ResourceType == "action" && (mutation.Operation == "create" || mutation.Operation == "update") {
			actions[strings.TrimSpace(mutation.ResourceKey)] = mutation
		}
	}
	seen := map[string]struct{}{}
	for index, scenario := range plan.AcceptanceScenarios {
		scenario.Key = strings.TrimSpace(scenario.Key)
		scenario.Kind = strings.TrimSpace(scenario.Kind)
		scenario.ResourceKey = strings.TrimSpace(scenario.ResourceKey)
		field := "acceptance_scenarios[" + itoa(index) + "]"
		if scenario.Key == "" {
			return result, badRequest("backend.change_plan.scenario_key_required", "field", field+".key")
		}
		if _, duplicate := seen[scenario.Key]; duplicate {
			return result, badRequest("backend.change_plan.scenario_key_duplicate", "scenario_key", scenario.Key)
		}
		seen[scenario.Key] = struct{}{}
		if scenario.Kind != changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition {
			return result, badRequest("backend.change_plan.scenario_kind_unsupported", "scenario_key", scenario.Key, "kind", scenario.Kind)
		}
		mutation, exists := actions[scenario.ResourceKey]
		if !exists {
			return result, badRequest("backend.change_plan.scenario_resource_not_in_candidate", "scenario_key", scenario.Key, "resource_type", "action", "resource_key", scenario.ResourceKey)
		}
		runtimeResult, err := s.scenarios.SimulateActionCandidate(ctx, scenario.ResourceKey, mutation.Request, scenario.Input, scenario.Record, principal)
		if err != nil {
			return result, err
		}
		expectedCodes := sortedUniqueStrings(scenario.Expected.ErrorCodes)
		actualCodes := sortedUniqueStrings(runtimeResult.ErrorCodes)
		passed := runtimeResult.SideEffectFree && !runtimeResult.ExecutionPerformed && runtimeResult.Valid == scenario.Expected.Valid && equalStrings(expectedCodes, actualCodes)
		if !passed {
			result.Passed = false
		}
		result.Results = append(result.Results, changeplanmodel.BusinessAcceptanceScenarioResult{
			Key: scenario.Key, Kind: scenario.Kind, ResourceKey: scenario.ResourceKey, Passed: passed,
			ExpectedValid: scenario.Expected.Valid, ActualValid: runtimeResult.Valid,
			ExpectedErrorCodes: expectedCodes, ActualErrorCodes: actualCodes,
			Simulation: append(json.RawMessage(nil), runtimeResult.Payload...),
		})
	}
	return result, nil
}

func sortedUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
