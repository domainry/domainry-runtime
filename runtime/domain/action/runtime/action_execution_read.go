package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *ActionExecutionRuntime) ReadReceipt(ctx context.Context, objectKey, recordID, actionKey, key string, input idempotency.FingerprintInput, p principalmodel.Principal) (actionmodel.ActionBusinessExecution, bool, error) {
	if s == nil || s.repository == nil {
		return actionmodel.ActionBusinessExecution{}, false, apperror.New(apperror.KindUnavailable, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	reader, ok := s.repository.(actioncontract.ActionExecutionReader)
	if !ok {
		return actionmodel.ActionBusinessExecution{}, false, apperror.New(apperror.KindUnavailable, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	if !p.Known || strings.TrimSpace(p.UserID) == "" || strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(key) == "" {
		return actionmodel.ActionBusinessExecution{}, false, apperror.New(apperror.KindForbidden, "backend.action.receipt_owner_mismatch", nil, nil)
	}
	fingerprint, err := idempotency.Fingerprint(input)
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, false, err
	}
	scope := actionmodel.ActionBusinessExecution{WorkspaceID: p.WorkspaceID, ObjectKey: objectKey, RecordID: recordID, ActionKey: actionKey, IdempotencyKey: key}
	value, found, err := reader.FindExecution(ctx, scope)
	if err != nil || !found {
		return value, found, err
	}
	if value.WorkspaceID != scope.WorkspaceID || value.ObjectKey != objectKey || value.RecordID != recordID || value.ActionKey != actionKey || value.IdempotencyKey != key || value.ActorID != p.UserID {
		return actionmodel.ActionBusinessExecution{}, false, apperror.New(apperror.KindForbidden, "backend.action.receipt_owner_mismatch", nil, nil)
	}
	if value.RequestFingerprint != fingerprint {
		return actionmodel.ActionBusinessExecution{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil, nil)
	}
	if value.ExpiresAt != "" {
		expiry, err := time.Parse(time.RFC3339Nano, value.ExpiresAt)
		if err != nil || !expiry.After(time.Now()) {
			return actionmodel.ActionBusinessExecution{}, false, apperror.New(apperror.KindUnavailable, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
		}
	}
	return value, true, nil
}
