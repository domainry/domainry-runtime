package action

import (
	"context"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *ActionApplicationService) projectExecutionResult(ctx context.Context, action definitionmodel.ActionSchema, principal principalmodel.Principal, executed ActionExecutionResult) (ActionExecutionResult, error) {
	if executed.Record != nil {
		if s.dependencies.ProjectRecord != nil {
			projected, err := s.dependencies.ProjectRecord(ctx, principal, executed.Record.ObjectKey, executed.Record.Record)
			if err != nil {
				return ActionExecutionResult{}, err
			}
			executed.Record.Record = projected
		}
		if s.dependencies.ProjectOutput != nil {
			projected, err := s.dependencies.ProjectOutput(ctx, principal, action, executed.Record.Output)
			if err != nil {
				return ActionExecutionResult{}, err
			}
			executed.Record.Output = projected
		}
	}
	if executed.Object != nil && s.dependencies.ProjectOutput != nil {
		projected, err := s.dependencies.ProjectOutput(ctx, principal, action, executed.Object.Output)
		if err != nil {
			return ActionExecutionResult{}, err
		}
		executed.Object.Output = projected
	}
	return executed, nil
}

func actionReceiptResult(result actionmodel.ActionInvocationResult) (any, error) {
	if (result.Record == nil) == (result.Object == nil) {
		return nil, apperror.New(apperror.KindInternal, "backend.action.result_invalid", nil, nil)
	}
	if result.Record != nil {
		return *result.Record, nil
	}
	return *result.Object, nil
}

func (s *ActionApplicationService) failOwnedInvocation(ctx context.Context, unitOfWork *actionUnitOfWork, result actionmodel.ActionInvocationResult, invocationErr error, audits []auditmodel.AuditEvent) (actionmodel.ActionInvocationResult, error) {
	failed, normalized := failInvocation(result, invocationErr)
	if unitOfWork == nil || mutation.IsTransactionCommitUnknown(invocationErr) {
		return failed, normalized
	}
	if err := unitOfWork.fail(ctx, failed, normalized, audits); err != nil {
		return failInvocation(result, err)
	}
	return failed, normalized
}

func buildActionSuccessAudit(ctx context.Context, action definitionmodel.ActionSchema, invocation actionmodel.ActionInvocation, result actionmodel.ActionInvocationResult) auditmodel.AuditEvent {
	event := definitionmodel.EffectiveActionAuditEvent(action)
	return auditcontract.AuditBuildEvent(ctx, event, action.ObjectKey, invocation.RecordID, invocation.Principal, "Executed action "+action.Key, nil, nil, map[string]any{"action_key": action.Key, "invocation_id": result.InvocationID, "owner_source": invocation.Source, "status": result.Status})
}

func buildActionFailureAudits(ctx context.Context, action definitionmodel.ActionSchema, invocation actionmodel.ActionInvocation, result actionmodel.ActionInvocationResult, failure error) []auditmodel.AuditEvent {
	if apperror.CodeOf(failure) != "backend.record.not_found" {
		return nil
	}
	params := apperror.ParamsOf(failure)
	objectKey := strings.TrimSpace(params["object_key"])
	recordID := strings.TrimSpace(params["record_id"])
	if objectKey == "" {
		objectKey = strings.TrimSpace(action.ObjectKey)
	}
	if recordID == "" {
		recordID = strings.TrimSpace(invocation.RecordID)
	}
	if !invocation.Principal.Known {
		return nil
	}
	if invocation.Principal.AccessBundle == nil {
		return nil
	}
	auditDenial, err := identityevaluator.AuditDenialRequired(
		*invocation.Principal.AccessBundle,
		identitysdk.ResourceType(objectKey),
		identitysdk.Action(actionpolicy.ActionName(action)),
		time.Now().UTC(),
	)
	if err != nil || !auditDenial {
		return nil
	}
	event := auditcontract.AuditBuildEvent(ctx, "data_scope_access_denied", objectKey, recordID, invocation.Principal, "Data scope access denied", nil, nil, map[string]any{
		"action":        "read_detail",
		"action_key":    action.Key,
		"decision":      "denied",
		"invocation_id": result.InvocationID,
		"owner_source":  invocation.Source,
		"status":        "failed",
	})
	return []auditmodel.AuditEvent{event}
}
