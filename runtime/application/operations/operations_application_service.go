package operations

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/requestcontext"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// OperationsIdempotencyReceiptControl retains the existing owner-specific
// recovery API while it migrates onto the unified operation ledger.
type OperationsIdempotencyReceiptControl interface {
	IdempotencyReceipts(context.Context, principalmodel.Principal, string, int) ([]idempotency.ReceiptSummary, error)
	RetryIdempotencyReceipt(context.Context, principalmodel.Principal, string, string) error
	ResetIdempotencyReceipt(context.Context, principalmodel.Principal, string, string) error
}

type OperationsSubmitRequest struct {
	Kind              string `json:"kind"`
	ParentOperationID string `json:"parent_operation_id,omitempty"`
	ResourceType      string `json:"resource_type"`
	ResourceID        string `json:"resource_id,omitempty"`
	Reason            string `json:"reason"`
	Reference         string `json:"reference,omitempty"`
	Payload           any    `json:"payload,omitempty"`
}

type OperationsApplicationService struct {
	repository       operationsrepository.OperationsRepository
	legacy           OperationsIdempotencyReceiptControl
	definitions      map[string]operationsmodel.OperationsDefinition
	now              func() time.Time
	newID            func() string
	deadLetterMu     sync.RWMutex
	deadLetterOwners map[string]OperationsDeadLetterOwner
	diagnostics      operationsrepository.OperationsDiagnosticsRepository
	instanceID       string
	breakGlass       operationsrepository.OperationsBreakGlassRepository
	breakGlassAlerts OperationsBreakGlassAlertSink
	resultArtifacts  operationsrepository.OperationsResultArtifactRepository
}

func NewOperationsApplicationService(repository operationsrepository.OperationsRepository, legacy OperationsIdempotencyReceiptControl, now func() time.Time, newID func() string, selectedDefinitions ...[]operationsmodel.OperationsDefinition) *OperationsApplicationService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if newID == nil {
		newID = requestcontext.NewRequestID
	}
	definitions := operationsprojection.OperationsDefinitions()
	if len(selectedDefinitions) != 0 {
		definitions = append([]operationsmodel.OperationsDefinition(nil), selectedDefinitions[0]...)
	}
	definitionsByKind := make(map[string]operationsmodel.OperationsDefinition, len(definitions))
	for _, definition := range definitions {
		definitionsByKind[strings.TrimSpace(definition.Kind)] = definition
	}
	return &OperationsApplicationService{repository: repository, legacy: legacy, definitions: definitionsByKind, now: now, newID: newID, deadLetterOwners: map[string]OperationsDeadLetterOwner{}}
}

func (s *OperationsApplicationService) Submit(ctx context.Context, request OperationsSubmitRequest, key string, principal principalmodel.Principal) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	definition, found := s.definition(request.Kind)
	if !found {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.kind_not_registered", nil, nil)
	}
	if strings.TrimSpace(request.ResourceType) != definition.ResourceType {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.definition_mismatch", nil, nil)
	}
	if definition.ExecutionScope != operationsmodel.OperationsExecutionWorkspace {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.system_entrypoint_required", nil, nil)
	}
	if err := operationsAuthorize(principal, definition.ActionKey); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	return s.submit(ctx, request, definition.Owner, definition.ActionKey, key, principal.UserID, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID, ResourceType: request.ResourceType, ResourceID: request.ResourceID})
}

// SubmitSystem registers a Runtime-global operation after authenticating the
// human operator through their workspace principal. Execution still requires
// an explicit SystemScope through Start/Finish.
func (s *OperationsApplicationService) SubmitSystem(ctx context.Context, request OperationsSubmitRequest, key, systemPurpose string, principal principalmodel.Principal) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	definition, found := s.definition(request.Kind)
	if !found {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.kind_not_registered", nil, nil)
	}
	if strings.TrimSpace(request.ResourceType) != definition.ResourceType {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.definition_mismatch", nil, nil)
	}
	if definition.ExecutionScope != operationsmodel.OperationsExecutionSystem || strings.TrimSpace(systemPurpose) == "" {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.system_scope_required", nil, nil)
	}
	if err := operationsAuthorize(principal, definition.ActionKey); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	return s.submit(ctx, request, definition.Owner, definition.ActionKey, key, principal.UserID, operationsmodel.OperationsScope{SystemPurpose: strings.TrimSpace(systemPurpose), ResourceType: request.ResourceType, ResourceID: request.ResourceID})
}

func (s *OperationsApplicationService) submit(ctx context.Context, request OperationsSubmitRequest, owner, actionKey, key, requestedBy string, scope operationsmodel.OperationsScope) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	if s == nil || s.repository == nil {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindInternal, "backend.operations.repository_unavailable", nil, nil)
	}
	parentID := strings.TrimSpace(request.ParentOperationID)
	if parentID != "" {
		if _, found, parentErr := s.repository.GetOperationsReceipt(ctx, scope, parentID); parentErr != nil {
			return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindInternal, "backend.operations.parent_read_failed", parentErr, nil)
		} else if !found {
			return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.parent_not_found", nil, nil)
		}
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: strings.TrimSpace(request.Kind), ResourceType: strings.TrimSpace(request.ResourceType), TargetID: strings.TrimSpace(request.ResourceID), Payload: request.Payload,
		Preconditions: map[string]any{"action_key": strings.TrimSpace(actionKey), "owner": strings.TrimSpace(owner), "parent_operation_id": parentID},
	})
	if err != nil {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.payload_invalid", err, nil)
	}
	now := s.now().UTC()
	command := operationsmodel.OperationsCommand{
		ID: "operation_" + strings.TrimSpace(s.newID()), Owner: owner, Kind: request.Kind, ActionKey: strings.TrimSpace(actionKey), ParentID: parentID,
		Scope:          scope,
		IdempotencyKey: key, RequestFingerprint: fingerprint, RequestedBy: requestedBy, Reason: request.Reason,
		Reference: request.Reference, Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now,
	}
	if err := operationspolicy.OperationsValidateCommand(command); err != nil {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, err.Error(), err, nil)
	}
	receipt := operationsmodel.OperationsReceipt{Command: operationspolicy.OperationsNormalizeCommand(command), StatusURL: "/operations/" + command.ID}
	persisted, decision, err := s.repository.RegisterOperationsCommand(ctx, receipt)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindInternal, "backend.operations.register_failed", err, nil)
	}
	if decision == operationsmodel.OperationsSubmissionConflict {
		return operationsmodel.OperationsReceipt{}, decision, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil, nil)
	}
	return persisted, decision, nil
}

func (s *OperationsApplicationService) Definitions() []operationsmodel.OperationsDefinition {
	if s == nil {
		return nil
	}
	definitions := make([]operationsmodel.OperationsDefinition, 0, len(s.definitions))
	for _, definition := range s.definitions {
		definition.Preconditions = append([]string(nil), definition.Preconditions...)
		definition.FailureSemantics = append([]operationsmodel.OperationsFailureClass(nil), definition.FailureSemantics...)
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Kind < definitions[j].Kind })
	return definitions
}

func (s *OperationsApplicationService) definition(kind string) (operationsmodel.OperationsDefinition, bool) {
	if s == nil {
		return operationsmodel.OperationsDefinition{}, false
	}
	definition, found := s.definitions[strings.TrimSpace(kind)]
	return definition, found
}

func (s *OperationsApplicationService) Receipt(ctx context.Context, id string, principal principalmodel.Principal) (operationsmodel.OperationsReceipt, error) {
	if err := operationsAuthorize(principal, operationscontract.ActionGetOperation); err != nil {
		return operationsmodel.OperationsReceipt{}, err
	}
	receipt, found, err := s.repository.GetOperationsReceipt(ctx, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID}, strings.TrimSpace(id))
	if err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.read_failed", err, nil)
	}
	if !found {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindNotFound, "backend.operations.not_found", nil, nil)
	}
	return receipt, nil
}

func (s *OperationsApplicationService) Receipts(ctx context.Context, status operationsmodel.OperationsStatus, limit int, principal principalmodel.Principal) ([]operationsmodel.OperationsReceipt, error) {
	if err := operationsAuthorize(principal, operationscontract.ActionListOperations); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return s.repository.ListOperationsReceipts(ctx, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID}, status, limit)
}

func (s *OperationsApplicationService) SearchReceipts(ctx context.Context, filter operationsmodel.OperationsReceiptFilter, principal principalmodel.Principal) (operationsmodel.OperationsReceiptPage, error) {
	if err := operationsAuthorize(principal, operationscontract.ActionListOperations); err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	filter = normalizeOperationsReceiptFilter(filter)
	if err := validateOperationsReceiptFilter(filter); err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	page, err := s.repository.SearchOperationsReceipts(ctx, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID}, filter)
	if err != nil {
		return operationsmodel.OperationsReceiptPage{}, apperror.New(apperror.KindInternal, "backend.operations.read_failed", err, nil)
	}
	if page.Items == nil {
		page.Items = []operationsmodel.OperationsReceipt{}
	}
	return page, nil
}

func normalizeOperationsReceiptFilter(filter operationsmodel.OperationsReceiptFilter) operationsmodel.OperationsReceiptFilter {
	filter.Owner = strings.TrimSpace(filter.Owner)
	filter.Kind = strings.TrimSpace(filter.Kind)
	filter.ParentID = strings.TrimSpace(filter.ParentID)
	filter.ResourceType = strings.TrimSpace(filter.ResourceType)
	filter.ResourceID = strings.TrimSpace(filter.ResourceID)
	filter.RequestedBy = strings.TrimSpace(filter.RequestedBy)
	filter.Correlation = strings.TrimSpace(filter.Correlation)
	filter.CreatedFrom = strings.TrimSpace(filter.CreatedFrom)
	filter.CreatedTo = strings.TrimSpace(filter.CreatedTo)
	if parsed, err := time.Parse(time.RFC3339, filter.CreatedFrom); err == nil {
		filter.CreatedFrom = parsed.UTC().Format(time.RFC3339Nano)
	}
	if parsed, err := time.Parse(time.RFC3339, filter.CreatedTo); err == nil {
		filter.CreatedTo = parsed.UTC().Format(time.RFC3339Nano)
	}
	filter.Search = strings.TrimSpace(filter.Search)
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 100
	}
	return filter
}

func validateOperationsReceiptFilter(filter operationsmodel.OperationsReceiptFilter) error {
	if filter.Status != "" && filter.Status != operationsmodel.OperationsStatusCreated && filter.Status != operationsmodel.OperationsStatusStarted && filter.Status != operationsmodel.OperationsStatusSucceeded && filter.Status != operationsmodel.OperationsStatusFailed {
		return apperror.New(apperror.KindBadRequest, "backend.operations.status_invalid", nil, nil)
	}
	if filter.FailureClass != "" && filter.FailureClass != operationsmodel.OperationsFailureRetryable && filter.FailureClass != operationsmodel.OperationsFailureTerminal && filter.FailureClass != operationsmodel.OperationsFailureManualIntervention {
		return apperror.New(apperror.KindBadRequest, "backend.operations.failure_class_invalid", nil, nil)
	}
	var from, to time.Time
	var err error
	if filter.CreatedFrom != "" {
		from, err = time.Parse(time.RFC3339, filter.CreatedFrom)
		if err != nil {
			return apperror.New(apperror.KindBadRequest, "backend.operations.created_from_invalid", err, nil)
		}
	}
	if filter.CreatedTo != "" {
		to, err = time.Parse(time.RFC3339, filter.CreatedTo)
		if err != nil {
			return apperror.New(apperror.KindBadRequest, "backend.operations.created_to_invalid", err, nil)
		}
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return apperror.New(apperror.KindBadRequest, "backend.operations.created_range_invalid", nil, nil)
	}
	return nil
}

func (s *OperationsApplicationService) Start(ctx context.Context, id string, operationScope operationsmodel.OperationsScope, systemScope principalmodel.SystemScope) (operationsmodel.OperationsReceipt, error) {
	if _, err := principalmodel.NewSystemCommandScope(systemScope); err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindForbidden, "backend.system_scope_required", err, nil)
	}
	return s.transition(ctx, id, operationsmodel.OperationsStatusCreated, operationsmodel.OperationsStatusStarted, operationScope)
}

func (s *OperationsApplicationService) Finish(ctx context.Context, receipt operationsmodel.OperationsReceipt, scope principalmodel.SystemScope) (operationsmodel.OperationsReceipt, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindForbidden, "backend.system_scope_required", err, nil)
	}
	if receipt.Command.Status != operationsmodel.OperationsStatusSucceeded && receipt.Command.Status != operationsmodel.OperationsStatusFailed {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindBadRequest, "backend.operations.terminal_status_required", nil, nil)
	}
	definition, found := s.definition(receipt.Command.Kind)
	if !found || strings.TrimSpace(receipt.Command.Owner) != definition.Owner || strings.TrimSpace(receipt.Command.ActionKey) != definition.ActionKey || strings.TrimSpace(receipt.Command.Scope.ResourceType) != definition.ResourceType {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindBadRequest, "backend.operations.definition_mismatch", nil, nil)
	}
	retention, err := operationspolicy.OperationsRetentionDuration(definition, receipt.Command.Status)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.retention_invalid", err, nil)
	}
	now := s.now().UTC()
	receipt.Command.FinishedAt, receipt.Command.UpdatedAt = &now, now
	receipt.ExpiresAt = now.Add(retention).Format(time.RFC3339Nano)
	receipt, err = s.prepareResultEvidence(ctx, receipt)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, err
	}
	if strings.TrimSpace(receipt.NextAction) == "" {
		receipt.NextAction = "inspect the operation receipt and verify the owner state before continuing"
	}
	if len(receipt.RelatedIDs) == 0 && strings.TrimSpace(receipt.Command.Scope.ResourceID) != "" {
		receipt.RelatedIDs = []string{receipt.Command.Scope.ResourceID}
	}
	if strings.TrimSpace(receipt.Correlation) == "" {
		receipt.Correlation = receipt.Command.ID
	}
	if len(receipt.Evidence) == 0 {
		receipt.Evidence = []string{receipt.StatusURL}
	}
	if err := operationspolicy.OperationsValidateReceipt(receipt); err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindBadRequest, err.Error(), err, nil)
	}
	changed, err := s.repository.UpdateOperationsReceipt(ctx, receipt, operationsmodel.OperationsStatusStarted)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.finish_failed", err, nil)
	}
	if !changed {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindConflict, "backend.operations.transition_conflict", nil, nil)
	}
	return receipt, nil
}

func (s *OperationsApplicationService) transition(ctx context.Context, id string, from, to operationsmodel.OperationsStatus, operationScope operationsmodel.OperationsScope) (operationsmodel.OperationsReceipt, error) {
	receipt, found, err := s.repository.GetOperationsReceipt(ctx, operationScope, strings.TrimSpace(id))
	if err != nil || !found {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindNotFound, "backend.operations.not_found", err, nil)
	}
	now := s.now().UTC()
	receipt.Command.Status, receipt.Command.UpdatedAt = to, now
	if to == operationsmodel.OperationsStatusStarted {
		receipt.Command.StartedAt = &now
	}
	changed, err := s.repository.UpdateOperationsReceipt(ctx, receipt, from)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.transition_failed", err, nil)
	}
	if !changed {
		return operationsmodel.OperationsReceipt{}, apperror.New(apperror.KindConflict, "backend.operations.transition_conflict", nil, nil)
	}
	return receipt, nil
}

func (s *OperationsApplicationService) LegacyReceipts(ctx context.Context, principal principalmodel.Principal, status string, limit int) ([]idempotency.ReceiptSummary, error) {
	if s.legacy == nil {
		return nil, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	return s.legacy.IdempotencyReceipts(ctx, principal, status, limit)
}

func (s *OperationsApplicationService) RetryLegacyReceipt(ctx context.Context, principal principalmodel.Principal, owner, id string) error {
	if s.legacy == nil {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	return s.legacy.RetryIdempotencyReceipt(ctx, principal, owner, id)
}

func (s *OperationsApplicationService) ResetLegacyReceipt(ctx context.Context, principal principalmodel.Principal, owner, id string) error {
	if s.legacy == nil {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	return s.legacy.ResetIdempotencyReceipt(ctx, principal, owner, id)
}

func operationsAuthorize(principal principalmodel.Principal, permission string) error {
	if _, err := principalmodel.NewWorkspaceID(principal.WorkspaceID); err != nil {
		return apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", errors.New("workspace is required"), nil)
	}
	if !principal.Known || strings.TrimSpace(principal.UserID) == "" {
		return apperror.New(apperror.KindForbidden, "auth.permission_denied", nil, nil)
	}
	if principal.HasExactPermission(strings.TrimSpace(permission)) {
		return nil
	}
	return apperror.New(apperror.KindForbidden, "auth.permission_denied", nil, nil)
}

func operationsDefinitionActionKey(kind string) string {
	definition, found := operationsprojection.OperationsDefinition(strings.TrimSpace(kind))
	if !found {
		return ""
	}
	return strings.TrimSpace(definition.ActionKey)
}
