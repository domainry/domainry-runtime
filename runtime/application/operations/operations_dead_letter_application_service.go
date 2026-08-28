package operations

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	OperationsDeadLetterResolve = "resolve"
	OperationsDeadLetterRetry   = "retry"
	OperationsDeadLetterAck     = "ack"
)

type OperationsDeadLetterItem struct {
	Owner          string         `json:"owner"`
	ID             string         `json:"id"`
	ResourceType   string         `json:"resource_type"`
	Status         string         `json:"status"`
	FailureCode    string         `json:"failure_code,omitempty"`
	CorrelationID  string         `json:"correlation_id,omitempty"`
	BusinessKey    string         `json:"business_key,omitempty"`
	EvidenceRef    string         `json:"evidence_ref,omitempty"`
	AllowedActions []string       `json:"allowed_actions"`
	Details        map[string]any `json:"details,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
}

type OperationsDeadLetterActionRequest struct {
	Reason    string `json:"reason"`
	Reference string `json:"reference,omitempty"`
}

type OperationsDeadLetterActionResult struct {
	Item    OperationsDeadLetterItem          `json:"item"`
	Receipt operationsmodel.OperationsReceipt `json:"receipt"`
}

// OperationsDeadLetterOwner is implemented by Bootstrap adapters around the
// real owner Application Services. Operations never mutates owner tables.
type OperationsDeadLetterOwner interface {
	Inspect(context.Context, string, principalmodel.Principal) (OperationsDeadLetterItem, error)
	Act(context.Context, string, string, string, string, principalmodel.Principal) (OperationsDeadLetterItem, error)
}

func (s *OperationsApplicationService) RegisterDeadLetterOwner(owner string, adapter OperationsDeadLetterOwner) error {
	owner = strings.TrimSpace(owner)
	if s == nil || owner == "" || adapter == nil {
		return apperror.New(apperror.KindBadRequest, "backend.operations.dead_letter_owner_invalid", nil, nil)
	}
	s.deadLetterMu.Lock()
	defer s.deadLetterMu.Unlock()
	if _, exists := s.deadLetterOwners[owner]; exists {
		return apperror.New(apperror.KindConflict, "backend.operations.dead_letter_owner_duplicate", nil, nil)
	}
	s.deadLetterOwners[owner] = adapter
	return nil
}

func (s *OperationsApplicationService) InspectDeadLetter(ctx context.Context, owner, id string, principal principalmodel.Principal) (OperationsDeadLetterItem, error) {
	if err := operationsAuthorize(principal, "runtime.dead_letter.read"); err != nil {
		return OperationsDeadLetterItem{}, err
	}
	adapter, err := s.deadLetterOwner(owner)
	if err != nil {
		return OperationsDeadLetterItem{}, err
	}
	return adapter.Inspect(ctx, strings.TrimSpace(id), principal)
}

func (s *OperationsApplicationService) ActOnDeadLetter(ctx context.Context, owner, id, action string, request OperationsDeadLetterActionRequest, key string, principal principalmodel.Principal) (OperationsDeadLetterActionResult, error) {
	action, owner, id = strings.TrimSpace(action), strings.TrimSpace(owner), strings.TrimSpace(id)
	if action != OperationsDeadLetterResolve && action != OperationsDeadLetterRetry && action != OperationsDeadLetterAck {
		return OperationsDeadLetterActionResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.dead_letter_action_invalid", nil, nil)
	}
	adapter, err := s.deadLetterOwner(owner)
	if err != nil {
		return OperationsDeadLetterActionResult{}, err
	}
	receipt, decision, err := s.Submit(ctx, OperationsSubmitRequest{
		Kind: "dead_letter." + action, Permission: "workspace.admin", ResourceType: "dead_letter", ResourceID: owner + ":" + id,
		Reason: strings.TrimSpace(request.Reason), Reference: strings.TrimSpace(request.Reference), Payload: map[string]any{"owner": owner, "dead_letter_id": id},
	}, key, principal)
	if err != nil {
		return OperationsDeadLetterActionResult{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		var item OperationsDeadLetterItem
		if json.Unmarshal(receipt.Result, &item) != nil {
			return OperationsDeadLetterActionResult{}, apperror.New(apperror.KindInternal, "backend.operations.dead_letter_receipt_invalid", nil, nil)
		}
		return OperationsDeadLetterActionResult{Item: item, Receipt: receipt}, nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "execute owner-controlled dead-letter transition")
	receipt, err = s.Start(ctx, receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		return OperationsDeadLetterActionResult{}, err
	}
	item, ownerErr := adapter.Act(ctx, id, action, strings.TrimSpace(request.Reason), key, principal)
	if ownerErr != nil {
		receipt.Command.Status, receipt.FailureClass, receipt.ErrorCode = operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureManualIntervention, "backend.operations.dead_letter_owner_rejected"
		receipt.NextAction = "inspect the owner state and readiness before retrying with a new idempotency key"
		_, _ = s.Finish(ctx, receipt, scope)
		return OperationsDeadLetterActionResult{}, ownerErr
	}
	result, _ := json.Marshal(item)
	receipt.Command.Status, receipt.Result = operationsmodel.OperationsStatusSucceeded, result
	receipt.RelatedIDs = []string{owner, id, item.CorrelationID, item.BusinessKey, item.EvidenceRef}
	receipt.NextAction = "inspect owner evidence and confirm the requested transition completed"
	receipt, err = s.Finish(ctx, receipt, scope)
	if err != nil {
		return OperationsDeadLetterActionResult{}, err
	}
	return OperationsDeadLetterActionResult{Item: item, Receipt: receipt}, nil
}

func (s *OperationsApplicationService) deadLetterOwner(owner string) (OperationsDeadLetterOwner, error) {
	if s == nil {
		return nil, apperror.New(apperror.KindInternal, "backend.operations.unavailable", nil, nil)
	}
	s.deadLetterMu.RLock()
	defer s.deadLetterMu.RUnlock()
	adapter := s.deadLetterOwners[strings.TrimSpace(owner)]
	if adapter == nil {
		return nil, apperror.New(apperror.KindBadRequest, "backend.operations.dead_letter_owner_not_registered", nil, nil)
	}
	return adapter, nil
}
