package operations

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type OperationsBreakGlassAlertSink interface {
	BreakGlassAlert(context.Context, string, operationsmodel.OperationsBreakGlassGrant, principalmodel.Principal) error
}

type OperationsBreakGlassEnableCommand struct {
	DurationSeconds int      `json:"duration_seconds"`
	ApproverIDs     []string `json:"approver_ids"`
	Reason          string   `json:"reason"`
	IncidentRef     string   `json:"incident_ref"`
	AlertTarget     string   `json:"alert_target"`
}

type OperationsBreakGlassDisableCommand struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
	IncidentRef      string `json:"incident_ref"`
}

type OperationsBreakGlassResult struct {
	Grant   operationsmodel.OperationsBreakGlassGrant `json:"grant"`
	Receipt operationsmodel.OperationsReceipt         `json:"receipt"`
}

func (s *OperationsApplicationService) RegisterBreakGlass(repository operationsrepository.OperationsBreakGlassRepository, alerts OperationsBreakGlassAlertSink) error {
	if s == nil || repository == nil || alerts == nil {
		return apperror.New(apperror.KindBadRequest, "backend.operations.break_glass_registration_invalid", nil, nil)
	}
	s.breakGlass, s.breakGlassAlerts = repository, alerts
	return nil
}

func (s *OperationsApplicationService) EnableBreakGlass(ctx context.Context, command OperationsBreakGlassEnableCommand, key string, principal principalmodel.Principal) (OperationsBreakGlassResult, error) {
	if s == nil || s.breakGlass == nil || s.breakGlassAlerts == nil {
		return OperationsBreakGlassResult{}, apperror.New(apperror.KindInternal, "backend.operations.break_glass_unavailable", nil, nil)
	}
	command.Reason, command.IncidentRef, command.AlertTarget = strings.TrimSpace(command.Reason), strings.TrimSpace(command.IncidentRef), strings.TrimSpace(command.AlertTarget)
	if command.DurationSeconds <= 0 || time.Duration(command.DurationSeconds)*time.Second > operationspolicy.MaximumBreakGlassDuration || command.AlertTarget == "" || command.IncidentRef == "" {
		return OperationsBreakGlassResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.break_glass_command_invalid", nil, nil)
	}
	receipt, decision, err := s.SubmitSystem(ctx, OperationsSubmitRequest{Kind: "break_glass.enable", Permission: "workspace.admin", ResourceType: "runtime", ResourceID: principal.WorkspaceID, Reason: command.Reason, Reference: command.IncidentRef, Payload: command}, key, operationsmodel.OperationsSystemPurposeRuntimeControl, principal)
	if err != nil {
		return OperationsBreakGlassResult{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		var grant operationsmodel.OperationsBreakGlassGrant
		if json.Unmarshal(receipt.Result, &grant) != nil {
			return OperationsBreakGlassResult{}, apperror.New(apperror.KindInternal, "backend.operations.break_glass_receipt_invalid", nil, nil)
		}
		return OperationsBreakGlassResult{Grant: grant, Receipt: receipt}, nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "enable time-limited break-glass access")
	receipt, err = s.Start(ctx, receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		return OperationsBreakGlassResult{}, err
	}
	now := s.now().UTC()
	grant := operationsmodel.OperationsBreakGlassGrant{ID: "break_glass_" + strings.TrimSpace(s.newID()), WorkspaceID: principal.WorkspaceID, State: operationsmodel.OperationsBreakGlassActive, ActorID: principal.UserID, ApproverIDs: append([]string(nil), command.ApproverIDs...), Reason: command.Reason, IncidentRef: command.IncidentRef, AlertTarget: command.AlertTarget, AuditEventID: "break_glass_alert_" + receipt.Command.ID, ExpiresAt: now.Add(time.Duration(command.DurationSeconds) * time.Second), Revision: 1, CreatedAt: now, UpdatedAt: now}
	if validationErr := operationspolicy.OperationsValidateBreakGlass(operationsmodel.BreakGlassGrant{ExpiresAt: grant.ExpiresAt, ApproverIDs: grant.ApproverIDs, AuditEventID: grant.AuditEventID}, grant.ActorID, now); validationErr != nil {
		return s.failBreakGlass(ctx, receipt, scope, "backend.operations.break_glass_approval_invalid", validationErr)
	}
	created, createErr := s.breakGlass.CreateOperationsBreakGlass(ctx, grant)
	if createErr != nil || !created {
		return s.failBreakGlass(ctx, receipt, scope, "backend.operations.break_glass_active_conflict", createErr)
	}
	if alertErr := s.breakGlassAlerts.BreakGlassAlert(ctx, "runtime_break_glass_enabled", grant, principal); alertErr != nil {
		revoked := now
		grant.State, grant.Revision, grant.UpdatedAt, grant.RevokedAt, grant.RevokedBy, grant.RevocationNote = operationsmodel.OperationsBreakGlassRevoked, 2, now, &revoked, principal.UserID, "automatic revocation because alert delivery failed"
		_, _ = s.breakGlass.RevokeOperationsBreakGlass(ctx, grant, 1)
		return s.failBreakGlass(ctx, receipt, scope, "backend.operations.break_glass_alert_failed", alertErr)
	}
	return s.finishBreakGlass(ctx, receipt, scope, grant, "break-glass is active only until expiry; monitor the alert target and revoke as soon as incident work ends")
}

func (s *OperationsApplicationService) DisableBreakGlass(ctx context.Context, grantID string, command OperationsBreakGlassDisableCommand, key string, principal principalmodel.Principal) (OperationsBreakGlassResult, error) {
	if s == nil || s.breakGlass == nil || s.breakGlassAlerts == nil {
		return OperationsBreakGlassResult{}, apperror.New(apperror.KindInternal, "backend.operations.break_glass_unavailable", nil, nil)
	}
	grantID, command.Reason, command.IncidentRef = strings.TrimSpace(grantID), strings.TrimSpace(command.Reason), strings.TrimSpace(command.IncidentRef)
	if grantID == "" || command.ExpectedRevision <= 0 || command.Reason == "" || command.IncidentRef == "" {
		return OperationsBreakGlassResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.break_glass_command_invalid", nil, nil)
	}
	receipt, decision, err := s.SubmitSystem(ctx, OperationsSubmitRequest{Kind: "break_glass.disable", Permission: "workspace.admin", ResourceType: "runtime", ResourceID: grantID, Reason: command.Reason, Reference: command.IncidentRef, Payload: command}, key, operationsmodel.OperationsSystemPurposeRuntimeControl, principal)
	if err != nil {
		return OperationsBreakGlassResult{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		var grant operationsmodel.OperationsBreakGlassGrant
		if json.Unmarshal(receipt.Result, &grant) != nil {
			return OperationsBreakGlassResult{}, apperror.New(apperror.KindInternal, "backend.operations.break_glass_receipt_invalid", nil, nil)
		}
		return OperationsBreakGlassResult{Grant: grant, Receipt: receipt}, nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "revoke break-glass access")
	receipt, err = s.Start(ctx, receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		return OperationsBreakGlassResult{}, err
	}
	grant, found, readErr := s.breakGlass.GetOperationsBreakGlass(ctx, grantID)
	if readErr != nil || !found || grant.WorkspaceID != principal.WorkspaceID {
		return s.failBreakGlass(ctx, receipt, scope, "backend.operations.break_glass_not_found", readErr)
	}
	if grant.State == operationsmodel.OperationsBreakGlassRevoked {
		return s.finishBreakGlass(ctx, receipt, scope, grant, "break-glass is already revoked; verify downstream access has ended")
	}
	now := s.now().UTC()
	grant.State, grant.Revision, grant.UpdatedAt, grant.RevokedAt, grant.RevokedBy, grant.RevocationNote = operationsmodel.OperationsBreakGlassRevoked, command.ExpectedRevision+1, now, &now, principal.UserID, command.Reason
	changed, revokeErr := s.breakGlass.RevokeOperationsBreakGlass(ctx, grant, command.ExpectedRevision)
	if revokeErr != nil || !changed {
		return s.failBreakGlass(ctx, receipt, scope, "backend.operations.break_glass_revision_conflict", revokeErr)
	}
	if alertErr := s.breakGlassAlerts.BreakGlassAlert(ctx, "runtime_break_glass_disabled", grant, principal); alertErr != nil {
		return s.failBreakGlass(ctx, receipt, scope, "backend.operations.break_glass_alert_failed", alertErr)
	}
	return s.finishBreakGlass(ctx, receipt, scope, grant, "verify privileged access is denied and retain incident evidence")
}

func (s *OperationsApplicationService) ListBreakGlass(ctx context.Context, limit int, principal principalmodel.Principal) ([]operationsmodel.OperationsBreakGlassGrant, error) {
	if err := operationsAuthorize(principal, "workspace.admin"); err != nil {
		return nil, err
	}
	grants, err := s.breakGlass.ListOperationsBreakGlass(ctx, principal.WorkspaceID, limit)
	if err != nil {
		return nil, err
	}
	for index := range grants {
		if grants[index].State == operationsmodel.OperationsBreakGlassActive && !grants[index].ExpiresAt.After(s.now().UTC()) {
			grants[index].State = operationsmodel.OperationsBreakGlassExpired
		}
	}
	return grants, nil
}
func (s *OperationsApplicationService) finishBreakGlass(ctx context.Context, receipt operationsmodel.OperationsReceipt, scope principalmodel.SystemScope, grant operationsmodel.OperationsBreakGlassGrant, next string) (OperationsBreakGlassResult, error) {
	encoded, _ := json.Marshal(grant)
	receipt.Command.Status, receipt.Result, receipt.NextAction = operationsmodel.OperationsStatusSucceeded, encoded, next
	receipt.RelatedIDs, receipt.Correlation, receipt.Evidence = []string{grant.ID, grant.IncidentRef, grant.AuditEventID}, grant.IncidentRef, []string{grant.AuditEventID, grant.AlertTarget}
	finished, err := s.Finish(ctx, receipt, scope)
	return OperationsBreakGlassResult{Grant: grant, Receipt: finished}, err
}
func (s *OperationsApplicationService) failBreakGlass(ctx context.Context, receipt operationsmodel.OperationsReceipt, scope principalmodel.SystemScope, code string, cause error) (OperationsBreakGlassResult, error) {
	receipt.Command.Status, receipt.FailureClass, receipt.ErrorCode, receipt.NextAction = operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureManualIntervention, code, "follow the break-glass runbook and verify no active grant remains"
	_, _ = s.Finish(ctx, receipt, scope)
	return OperationsBreakGlassResult{}, apperror.New(apperror.KindConflict, code, cause, nil)
}
