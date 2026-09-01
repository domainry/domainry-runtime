package operations

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	ActionDiscoverDatabaseRetirement = "runtime.operations.discover_database_retirement"
	ActionListDatabaseRetirements    = "runtime.operations.list_database_retirements"
	ActionGetDatabaseRetirement      = "runtime.operations.get_database_retirement"
	ActionPreviewDatabaseRetirement  = "runtime.operations.preview_database_retirement"
	ActionAdvanceDatabaseRetirement  = "runtime.operations.advance_database_retirement"
	ActionExecuteDatabaseRetirement  = "runtime.operations.execute_database_retirement"
)

type DatabaseRetirementApplicationService struct {
	repository operationsrepository.DatabaseRetirementRepository
	executor   operationscontract.DatabaseRetirementExecutor
	now        func() time.Time
	newID      func() string
}

func NewDatabaseRetirementApplicationService(repository operationsrepository.DatabaseRetirementRepository, executor operationscontract.DatabaseRetirementExecutor, now func() time.Time, newID func() string) *DatabaseRetirementApplicationService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if newID == nil {
		newID = requestcontext.NewRequestID
	}
	return &DatabaseRetirementApplicationService{repository: repository, executor: executor, now: now, newID: newID}
}

func (s *DatabaseRetirementApplicationService) Discover(ctx context.Context, object operationsmodel.DatabaseObjectIdentity, owner string, principal principalmodel.Principal) (operationsmodel.DatabaseRetirement, error) {
	if err := authorizeDatabaseRetirement(principal, ActionDiscoverDatabaseRetirement); err != nil {
		return operationsmodel.DatabaseRetirement{}, err
	}
	if s == nil || s.repository == nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_repository_unavailable", nil, nil)
	}
	now := s.now().UTC()
	retirement := operationsmodel.DatabaseRetirement{ID: "database_retirement_" + strings.TrimSpace(s.newID()), Object: object, State: operationsmodel.DatabaseRetirementDiscovered, Evidence: operationsmodel.DatabaseRetirementEvidence{Owner: strings.TrimSpace(owner), AuditEventID: "discovery:" + strings.TrimSpace(s.newID())}, UpdatedAt: now}
	if err := validateDatabaseRetirementDiscovery(retirement); err != nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindBadRequest, err.Error(), err, nil)
	}
	created, err := s.repository.RegisterDatabaseRetirement(ctx, retirement)
	if err != nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_register_failed", err, nil)
	}
	if !created {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindConflict, "backend.operations.database_retirement_exists", nil, nil)
	}
	return retirement, nil
}

func (s *DatabaseRetirementApplicationService) Status(ctx context.Context, id string, principal principalmodel.Principal) (operationsmodel.DatabaseRetirement, error) {
	if err := authorizeDatabaseRetirement(principal, ActionGetDatabaseRetirement); err != nil {
		return operationsmodel.DatabaseRetirement{}, err
	}
	return s.status(ctx, id)
}

func (s *DatabaseRetirementApplicationService) status(ctx context.Context, id string) (operationsmodel.DatabaseRetirement, error) {
	retirement, found, err := s.repository.GetDatabaseRetirement(ctx, strings.TrimSpace(id))
	if err != nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_read_failed", err, nil)
	}
	if !found {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindNotFound, "backend.operations.database_retirement_not_found", nil, nil)
	}
	return retirement, nil
}

func (s *DatabaseRetirementApplicationService) OperationalStatus(ctx context.Context, id string, principal principalmodel.Principal) (operationsmodel.DatabaseRetirementOperationalStatus, error) {
	if err := authorizeDatabaseRetirement(principal, ActionGetDatabaseRetirement); err != nil {
		return operationsmodel.DatabaseRetirementOperationalStatus{}, err
	}
	retirement, err := s.status(ctx, id)
	if err != nil {
		return operationsmodel.DatabaseRetirementOperationalStatus{}, err
	}
	now := s.now().UTC()
	remaining := int64(0)
	if retirement.Evidence.Observation.WindowEnds.After(now) {
		remaining = int64(retirement.Evidence.Observation.WindowEnds.Sub(now).Seconds())
	}
	alerts := make([]string, 0, 3)
	observation := retirement.Evidence.Observation
	if observation.ReadCount > 0 || observation.WriteCount > 0 {
		alerts = append(alerts, "database_retirement_access_non_zero")
	}
	if retirement.State == operationsmodel.DatabaseRetirementBlocked {
		alerts = append(alerts, "database_retirement_blocked")
	}
	if retirement.Evidence.BackupVerifiedAt != nil && now.Sub(*retirement.Evidence.BackupVerifiedAt) > operationspolicy.MaximumRetirementEvidenceAge {
		alerts = append(alerts, "database_retirement_backup_expired")
	}
	if retirement.Evidence.RestoreDrillAt != nil && now.Sub(*retirement.Evidence.RestoreDrillAt) > operationspolicy.MaximumRetirementEvidenceAge {
		alerts = append(alerts, "database_retirement_restore_drill_expired")
	}
	return operationsmodel.DatabaseRetirementOperationalStatus{Retirement: retirement, ObservationRemainingSeconds: remaining, Alerts: alerts}, nil
}

func (s *DatabaseRetirementApplicationService) List(ctx context.Context, state operationsmodel.DatabaseRetirementState, limit int, principal principalmodel.Principal) ([]operationsmodel.DatabaseRetirement, error) {
	if err := authorizeDatabaseRetirement(principal, ActionListDatabaseRetirements); err != nil {
		return nil, err
	}
	return s.repository.ListDatabaseRetirements(ctx, state, limit)
}

func (s *DatabaseRetirementApplicationService) Preview(ctx context.Context, id string, principal principalmodel.Principal) (operationsmodel.DatabaseDropPlan, error) {
	if err := authorizeDatabaseRetirement(principal, ActionPreviewDatabaseRetirement); err != nil {
		return operationsmodel.DatabaseDropPlan{}, err
	}
	retirement, err := s.status(ctx, id)
	if err != nil {
		return operationsmodel.DatabaseDropPlan{}, err
	}
	return s.preview(ctx, retirement)
}

func (s *DatabaseRetirementApplicationService) preview(ctx context.Context, retirement operationsmodel.DatabaseRetirement) (operationsmodel.DatabaseDropPlan, error) {
	if s.executor == nil {
		return operationsmodel.DatabaseDropPlan{}, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_executor_unavailable", nil, nil)
	}
	plan, err := s.executor.PreviewDatabaseRetirement(ctx, retirement)
	if err != nil {
		return operationsmodel.DatabaseDropPlan{}, apperror.New(apperror.KindConflict, "backend.operations.database_retirement_preview_blocked", err, nil)
	}
	if err := operationspolicy.OperationsValidateDatabaseDropPlan(plan); err != nil {
		return operationsmodel.DatabaseDropPlan{}, apperror.New(apperror.KindConflict, err.Error(), err, nil)
	}
	return plan, nil
}

func (s *DatabaseRetirementApplicationService) Advance(ctx context.Context, id string, nextState operationsmodel.DatabaseRetirementState, evidence operationsmodel.DatabaseRetirementEvidence, principal principalmodel.Principal) (operationsmodel.DatabaseRetirement, error) {
	if err := authorizeDatabaseRetirement(principal, ActionAdvanceDatabaseRetirement); err != nil {
		return operationsmodel.DatabaseRetirement{}, err
	}
	current, err := s.status(ctx, id)
	if err != nil {
		return operationsmodel.DatabaseRetirement{}, err
	}
	if current.State == nextState {
		if reflect.DeepEqual(current.Evidence, evidence) {
			return current, nil
		}
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindConflict, "backend.operations.database_retirement_idempotency_conflict", nil, nil)
	}
	next := current
	next.State, next.Evidence, next.UpdatedAt, next.BlockedReason = nextState, evidence, s.now().UTC(), ""
	if err := operationspolicy.OperationsValidateDatabaseRetirementTransition(current, next, next.UpdatedAt); err != nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindConflict, err.Error(), err, nil)
	}
	if s.executor == nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_executor_unavailable", nil, nil)
	}
	next, err = s.executor.ApplyDatabaseRetirementTransition(ctx, current, next)
	if err != nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindConflict, "backend.operations.database_retirement_transition_effect_failed", err, nil)
	}
	if err := operationspolicy.OperationsValidateDatabaseRetirementTransition(current, next, next.UpdatedAt); err != nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindConflict, err.Error(), err, nil)
	}
	changed, err := s.repository.TransitionDatabaseRetirement(ctx, next, current.State)
	if err != nil {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_transition_failed", err, nil)
	}
	if !changed {
		return operationsmodel.DatabaseRetirement{}, apperror.New(apperror.KindConflict, "backend.operations.database_retirement_transition_conflict", nil, nil)
	}
	return next, nil
}

func (s *DatabaseRetirementApplicationService) Execute(ctx context.Context, id string, principal principalmodel.Principal) (operationscontract.DatabaseRetirementExecutionResult, error) {
	if err := authorizeDatabaseRetirement(principal, ActionExecuteDatabaseRetirement); err != nil {
		return operationscontract.DatabaseRetirementExecutionResult{}, err
	}
	current, err := s.status(ctx, id)
	if err != nil {
		return operationscontract.DatabaseRetirementExecutionResult{}, err
	}
	if current.State == operationsmodel.DatabaseRetirementDropped {
		return operationscontract.DatabaseRetirementExecutionResult{AuditEventID: current.Evidence.AuditEventID}, nil
	}
	if current.State != operationsmodel.DatabaseRetirementQuarantined {
		return operationscontract.DatabaseRetirementExecutionResult{}, apperror.New(apperror.KindConflict, "backend.operations.database_retirement_not_quarantined", nil, nil)
	}
	now := s.now().UTC()
	next := current
	next.State, next.UpdatedAt = operationsmodel.DatabaseRetirementDropped, now
	if err := operationspolicy.OperationsValidateDatabaseRetirementTransition(current, next, now); err != nil {
		return operationscontract.DatabaseRetirementExecutionResult{}, apperror.New(apperror.KindConflict, err.Error(), err, nil)
	}
	plan, err := s.preview(ctx, current)
	if err != nil {
		return operationscontract.DatabaseRetirementExecutionResult{}, err
	}
	result, executeErr := s.executor.ExecuteDatabaseRetirement(ctx, current, plan)
	if executeErr != nil || result.Dirty {
		blocked := current
		blocked.State, blocked.UpdatedAt, blocked.BlockedReason = operationsmodel.DatabaseRetirementBlocked, now, strings.TrimSpace(result.BlockedReason)
		if blocked.BlockedReason == "" {
			blocked.BlockedReason = "destructive execution failed"
		}
		blocked.Evidence.AuditEventID = result.AuditEventID
		_, _ = s.repository.TransitionDatabaseRetirement(ctx, blocked, current.State)
		return result, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_execute_failed", executeErr, nil)
	}
	next.Evidence.AuditEventID = result.AuditEventID
	changed, err := s.repository.TransitionDatabaseRetirement(ctx, next, current.State)
	if err != nil || !changed {
		return result, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_commit_failed", err, nil)
	}
	return result, nil
}

func validateDatabaseRetirementDiscovery(retirement operationsmodel.DatabaseRetirement) error {
	if strings.TrimSpace(retirement.ID) == "" || strings.TrimSpace(retirement.Object.Engine) == "" || strings.TrimSpace(retirement.Object.Database) == "" || strings.TrimSpace(retirement.Object.Kind) == "" || strings.TrimSpace(retirement.Object.Name) == "" || strings.TrimSpace(retirement.Evidence.Owner) == "" {
		return fmt.Errorf("backend.operations.database_retirement_identity_required")
	}
	return nil
}

func authorizeDatabaseRetirement(principal principalmodel.Principal, actionKey string) error {
	if !principal.Known || strings.TrimSpace(principal.UserID) == "" || !principal.HasExactPermission(actionKey) {
		return apperror.New(apperror.KindForbidden, "auth.permission_denied", nil, nil)
	}
	return nil
}
