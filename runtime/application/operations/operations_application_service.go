package operations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

// OperationsIdempotencyReceiptControl retains the existing owner-specific
// recovery surface while it migrates onto the unified operation ledger.
type OperationsIdempotencyReceiptControl interface {
	IdempotencyReceipts(context.Context, principalmodel.Principal, string, int) ([]idempotency.ReceiptSummary, error)
	RetryIdempotencyReceipt(context.Context, principalmodel.Principal, string, string) error
	ResetIdempotencyReceipt(context.Context, principalmodel.Principal, string, string) error
}

type OperationsSubmitRequest struct {
	Kind         string `json:"kind"`
	Permission   string `json:"permission"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id,omitempty"`
	Reason       string `json:"reason"`
	Reference    string `json:"reference,omitempty"`
	Payload      any    `json:"payload,omitempty"`
}

type OperationsApplicationService struct {
	repository                operationsrepository.OperationsRepository
	legacy                    OperationsIdempotencyReceiptControl
	now                       func() time.Time
	newID                     func() string
	deadLetterMu              sync.RWMutex
	deadLetterOwners          map[string]OperationsDeadLetterOwner
	diagnostics               operationsrepository.OperationsDiagnosticsRepository
	instanceID                string
	breakGlass                operationsrepository.OperationsBreakGlassRepository
	breakGlassAlerts          OperationsBreakGlassAlertSink
	directAuthoringProjection OperationsDirectAuthoringProjection
}

func NewOperationsApplicationService(repository operationsrepository.OperationsRepository, legacy OperationsIdempotencyReceiptControl, now func() time.Time, newID func() string) *OperationsApplicationService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if newID == nil {
		newID = requestcontext.NewRequestID
	}
	return &OperationsApplicationService{repository: repository, legacy: legacy, now: now, newID: newID, deadLetterOwners: map[string]OperationsDeadLetterOwner{}}
}

func (s *OperationsApplicationService) Submit(ctx context.Context, request OperationsSubmitRequest, key string, principal principalmodel.Principal) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	definition, found := operationsprojection.OperationsDefinition(strings.TrimSpace(request.Kind))
	if !found {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.kind_not_registered", nil, nil)
	}
	if strings.TrimSpace(request.ResourceType) != definition.ResourceType || !operationsPermissionDeclared(definition.Permissions, request.Permission) {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.definition_mismatch", nil, nil)
	}
	if definition.ExecutionScope != operationsmodel.OperationsExecutionWorkspace {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.system_entrypoint_required", nil, nil)
	}
	if err := operationsAuthorize(principal, request.Permission); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	return s.submit(ctx, request, key, principal.UserID, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID, ResourceType: request.ResourceType, ResourceID: request.ResourceID})
}

// SubmitSystem registers a Runtime-global operation after authenticating the
// human operator through their workspace principal. Execution still requires
// an explicit SystemScope through Start/Finish.
func (s *OperationsApplicationService) SubmitSystem(ctx context.Context, request OperationsSubmitRequest, key, systemPurpose string, principal principalmodel.Principal) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	definition, found := operationsprojection.OperationsDefinition(strings.TrimSpace(request.Kind))
	if !found {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.kind_not_registered", nil, nil)
	}
	if strings.TrimSpace(request.ResourceType) != definition.ResourceType || !operationsPermissionDeclared(definition.Permissions, request.Permission) {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.definition_mismatch", nil, nil)
	}
	if definition.ExecutionScope != operationsmodel.OperationsExecutionSystem || strings.TrimSpace(systemPurpose) == "" {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.system_scope_required", nil, nil)
	}
	if err := operationsAuthorize(principal, request.Permission); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	return s.submit(ctx, request, key, principal.UserID, operationsmodel.OperationsScope{SystemPurpose: strings.TrimSpace(systemPurpose), ResourceType: request.ResourceType, ResourceID: request.ResourceID})
}

func (s *OperationsApplicationService) submit(ctx context.Context, request OperationsSubmitRequest, key, requestedBy string, scope operationsmodel.OperationsScope) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	if s == nil || s.repository == nil {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindInternal, "backend.operations.repository_unavailable", nil, nil)
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: strings.TrimSpace(request.Kind), ResourceType: strings.TrimSpace(request.ResourceType), TargetID: strings.TrimSpace(request.ResourceID), Payload: request.Payload,
		Preconditions: map[string]any{"permission": strings.TrimSpace(request.Permission)},
	})
	if err != nil {
		return operationsmodel.OperationsReceipt{}, "", apperror.New(apperror.KindBadRequest, "backend.operations.payload_invalid", err, nil)
	}
	now := s.now().UTC()
	command := operationsmodel.OperationsCommand{
		ID: "operation_" + strings.TrimSpace(s.newID()), Kind: request.Kind, Permission: request.Permission,
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
	return operationsprojection.OperationsDefinitions()
}

func (s *OperationsApplicationService) Receipt(ctx context.Context, id string, principal principalmodel.Principal) (operationsmodel.OperationsReceipt, error) {
	if err := operationsAuthorize(principal, "operations.read"); err != nil {
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
	if err := operationsAuthorize(principal, "operations.read"); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return s.repository.ListOperationsReceipts(ctx, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID}, status, limit)
}

func (s *OperationsApplicationService) SearchReceipts(ctx context.Context, filter operationsmodel.OperationsReceiptFilter, principal principalmodel.Principal) (operationsmodel.OperationsReceiptPage, error) {
	if err := operationsAuthorize(principal, "operations.read"); err != nil {
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
	filter.Kind = strings.TrimSpace(filter.Kind)
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
	now := s.now().UTC()
	receipt.Command.FinishedAt, receipt.Command.UpdatedAt = &now, now
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
	if principal.HasExactPermission("workspace.admin") {
		return nil
	}
	return apperror.New(apperror.KindForbidden, "auth.permission_denied", nil, nil)
}

func operationsPermissionDeclared(permissions []string, permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "workspace.admin" {
		return true
	}
	for _, candidate := range permissions {
		if strings.TrimSpace(candidate) == permission {
			return true
		}
	}
	return false
}
