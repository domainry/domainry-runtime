package action

import (
	"context"
	"encoding/json"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ActionInvocationInspection struct {
	Found     bool
	Status    string
	ErrorCode string
	Result    actionmodel.ActionInvocationResult
}

// InspectInvocation reads an owned receipt. It never consumes assurance,
// acquires a lease, rechecks post-mutation business preconditions or executes.
// Callers must also apply current record scope before exposing its result.
func (s *ActionApplicationService) InspectInvocation(ctx context.Context, in actionmodel.ActionInvocation) (ActionInvocationInspection, error) {
	return s.inspectInvocation(ctx, in, false)
}

func (s *ActionApplicationService) inspectInvocation(ctx context.Context, in actionmodel.ActionInvocation, resultRead bool) (ActionInvocationInspection, error) {
	in = ActionNormalizeInvocation(in)
	return s.inspectInvocationForProducer(ctx, in, resultRead, in.Principal)
}

func (s *ActionApplicationService) inspectInvocationForProducer(ctx context.Context, in actionmodel.ActionInvocation, resultRead bool, producer principalmodel.Principal) (ActionInvocationInspection, error) {
	var out ActionInvocationInspection
	in = ActionNormalizeInvocation(in)
	if err := actionAuthorizeQuery(in.Principal); err != nil {
		return out, err
	}
	entry, ok := s.dependencies.Catalog.Entry(in.ActionKey)
	if !ok {
		return out, apperror.New(apperror.KindNotFound, "backend.action.not_found", nil, nil)
	}
	action := entry.Definition
	if action.ObjectKey != in.ObjectKey || in.RecordID == "" && !actionpolicy.ActionIsObjectKind(action.Kind) || in.RecordID != "" && !actionpolicy.ActionIsRecordKind(action.Kind) {
		return out, apperror.New(apperror.KindBadRequest, "backend.action.object_mismatch", nil, nil)
	}
	if resultRead {
		if err := s.authorizeInvocationReceiptOwner(ctx, in, action, producer.UserID); err != nil {
			return out, err
		}
	} else {
		if err := s.dependencies.Authorization.Validate(in.Principal, action); err != nil {
			return out, err
		}
	}
	payload, err := ActionNormalizePayload(action, in.Input)
	if err != nil {
		return out, err
	}
	in.Input = payload
	execution, found, err := s.dependencies.UnitOfWork.executions.ReadReceipt(ctx, action.ObjectKey, in.RecordID, action.Key, in.IdempotencyKey, actionInvocationFingerprint(in, action.Key), producer)
	if err != nil || !found {
		return out, err
	}
	out.Found, out.Status, out.ErrorCode = true, execution.Status, execution.ErrorCode
	if execution.Status != string(idempotency.StatusSucceeded) {
		return out, nil
	}
	raw, err := json.Marshal(execution.Result)
	if err != nil {
		return out, err
	}
	if in.RecordID != "" {
		var record actionmodel.ActionResult
		if err := json.Unmarshal(raw, &record); err != nil {
			return out, err
		}
		out.Result = invocationResultFromRecord(in, action, record)
	} else {
		var object actionmodel.ActionObjectResult
		if err := json.Unmarshal(raw, &object); err != nil {
			return out, err
		}
		out.Result = invocationResultFromObject(in, action, object)
	}
	return out, nil
}

func actionInvocationFingerprint(in actionmodel.ActionInvocation, actionKey string) idempotency.FingerprintInput {
	payload := any(in.Input)
	if in.TargetOrganizationID != "" {
		payload = map[string]any{"input": in.Input, "target_organization_id": in.TargetOrganizationID}
	}
	return idempotency.FingerprintInput{UseCase: "action.invoke", ResourceType: "action", TargetID: actionKey, Payload: payload}
}
