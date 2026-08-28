package operations

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestDatabaseRetirementApplicationDoesNotAcceptSQLAndBlocksEarlyExecution(t *testing.T) {
	now := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{}}
	executor := &databaseRetirementExecutorFake{}
	service := NewDatabaseRetirementApplicationService(repository, executor, func() time.Time { return now }, func() string { return "fixed" })
	principal := databaseRetirementPrincipal()
	retirement, err := service.Discover(t.Context(), operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old_table"}, "record", principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(t.Context(), retirement.ID, principal); err == nil {
		t.Fatal("destructive execution before quarantine accepted")
	}
	if executor.executed {
		t.Fatal("executor called before policy gate")
	}
}

func TestDatabaseRetirementApplicationMarksFailedExecutionBlocked(t *testing.T) {
	now := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	quarantineEnded := now.Add(-time.Minute)
	verified, drill := now.Add(-time.Hour), now.Add(-2*time.Hour)
	retirement := operationsmodel.DatabaseRetirement{
		ID: "retire-1", Object: operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old_table"}, State: operationsmodel.DatabaseRetirementQuarantined, UpdatedAt: now.Add(-time.Hour),
		Evidence: operationsmodel.DatabaseRetirementEvidence{Owner: "record", Replacement: "new_table", ExpectedSchemaVersion: "2", ExpectedDataVersion: "2", BackfillComplete: true, BackfillCheckpoint: "checkpoint", ReadsSwitchVersion: "2.1", WritesDisableVersion: "2.2", WriteProtection: "trigger", Observation: operationsmodel.DatabaseAccessObservation{WindowStarted: now.Add(-48 * time.Hour), WindowEnds: now.Add(-24 * time.Hour), SourceCounts: map[string]uint64{"runtime": 0}}, Comparison: operationsmodel.DatabaseDataComparison{SourceRows: 1, ReplacementRows: 1, SourceKeys: 1, ReplacementKeys: 1, SourceHash: "same", ReplacementHash: "same", BusinessChecks: []string{"count"}, ComparedAt: now.Add(-25 * time.Hour)}, Disposition: "migrate", BackupID: "backup", BackupChecksum: "checksum", BackupVerifiedAt: &verified, RestoreDrillAt: &drill, MaintenanceEvidence: "maintenance", DrainEvidence: "drain", ChangePlanID: "change", ApprovalID: "approval", QuarantineUntil: &quarantineEnded, Rollback: "restore", AuditEventID: "audit"},
	}
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{retirement.ID: retirement}}
	executor := &databaseRetirementExecutorFake{fail: true}
	service := NewDatabaseRetirementApplicationService(repository, executor, func() time.Time { return now }, nil)
	if _, err := service.Execute(t.Context(), retirement.ID, databaseRetirementPrincipal()); err == nil {
		t.Fatal("failed destructive execution reported success")
	}
	if repository.items[retirement.ID].State != operationsmodel.DatabaseRetirementBlocked {
		t.Fatalf("failed execution state=%s", repository.items[retirement.ID].State)
	}
}

func TestDatabaseRetirementRetriesAreIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	retirement := operationsmodel.DatabaseRetirement{ID: "retire-idempotent", Object: operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "retired_table"}, State: operationsmodel.DatabaseRetirementDropped, UpdatedAt: now, Evidence: operationsmodel.DatabaseRetirementEvidence{Owner: "record", AuditEventID: "audit-success"}}
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{retirement.ID: retirement}}
	executor := &databaseRetirementExecutorFake{}
	service := NewDatabaseRetirementApplicationService(repository, executor, func() time.Time { return now }, nil)
	result, err := service.Execute(t.Context(), retirement.ID, databaseRetirementPrincipal())
	if err != nil || result.ExecutedStatements != 0 || result.AuditEventID != "audit-success" || executor.executed {
		t.Fatalf("idempotent execute result=%+v executed=%t err=%v", result, executor.executed, err)
	}
	advanced, err := service.Advance(t.Context(), retirement.ID, retirement.State, retirement.Evidence, databaseRetirementPrincipal())
	if err != nil || advanced.State != retirement.State {
		t.Fatalf("idempotent advance=%+v err=%v", advanced, err)
	}
}

type databaseRetirementRepositoryFake struct {
	items             map[string]operationsmodel.DatabaseRetirement
	registerErr       error
	getErr            error
	listErr           error
	transitionErr     error
	transitionChanged *bool
}

func (r *databaseRetirementRepositoryFake) RegisterDatabaseRetirement(_ context.Context, value operationsmodel.DatabaseRetirement) (bool, error) {
	if r.registerErr != nil {
		return false, r.registerErr
	}
	if _, exists := r.items[value.ID]; exists {
		return false, nil
	}
	r.items[value.ID] = value
	return true, nil
}
func (r *databaseRetirementRepositoryFake) GetDatabaseRetirement(_ context.Context, id string) (operationsmodel.DatabaseRetirement, bool, error) {
	if r.getErr != nil {
		return operationsmodel.DatabaseRetirement{}, false, r.getErr
	}
	value, found := r.items[id]
	return value, found, nil
}
func (r *databaseRetirementRepositoryFake) ListDatabaseRetirements(_ context.Context, state operationsmodel.DatabaseRetirementState, _ int) ([]operationsmodel.DatabaseRetirement, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	result := []operationsmodel.DatabaseRetirement{}
	for _, value := range r.items {
		if state == "" || value.State == state {
			result = append(result, value)
		}
	}
	return result, nil
}
func (r *databaseRetirementRepositoryFake) TransitionDatabaseRetirement(_ context.Context, value operationsmodel.DatabaseRetirement, expected operationsmodel.DatabaseRetirementState) (bool, error) {
	if r.transitionErr != nil {
		return false, r.transitionErr
	}
	if r.transitionChanged != nil {
		return *r.transitionChanged, nil
	}
	current, found := r.items[value.ID]
	if !found || current.State != expected {
		return false, nil
	}
	r.items[value.ID] = value
	return true, nil
}
func (r *databaseRetirementRepositoryFake) RecordDatabaseRetirementAccess(context.Context, string, string, string, time.Time) error {
	return nil
}

type databaseRetirementExecutorFake struct {
	executed      bool
	fail          bool
	applyErr      error
	applyResult   func(operationsmodel.DatabaseRetirement) operationsmodel.DatabaseRetirement
	previewErr    error
	invalidPlan   bool
	executeErr    error
	dirty         bool
	blockedReason string
}

func (e *databaseRetirementExecutorFake) ApplyDatabaseRetirementTransition(_ context.Context, _ operationsmodel.DatabaseRetirement, next operationsmodel.DatabaseRetirement) (operationsmodel.DatabaseRetirement, error) {
	if e.applyErr != nil {
		return operationsmodel.DatabaseRetirement{}, e.applyErr
	}
	if e.applyResult != nil {
		return e.applyResult(next), nil
	}
	return next, nil
}

func (e *databaseRetirementExecutorFake) PreviewDatabaseRetirement(_ context.Context, retirement operationsmodel.DatabaseRetirement) (operationsmodel.DatabaseDropPlan, error) {
	if e.previewErr != nil {
		return operationsmodel.DatabaseDropPlan{}, e.previewErr
	}
	if e.invalidPlan {
		return operationsmodel.DatabaseDropPlan{RetirementID: retirement.ID}, nil
	}
	return operationsmodel.DatabaseDropPlan{RetirementID: retirement.ID, Object: retirement.Object, Statements: []string{"DROP TABLE old_table"}, EstimatedLock: time.Second, LockTimeout: 5 * time.Second, Rollback: "restore", ApprovalID: retirement.Evidence.ApprovalID}, nil
}
func (e *databaseRetirementExecutorFake) ExecuteDatabaseRetirement(context.Context, operationsmodel.DatabaseRetirement, operationsmodel.DatabaseDropPlan) (operationscontract.DatabaseRetirementExecutionResult, error) {
	e.executed = true
	if e.fail {
		return operationscontract.DatabaseRetirementExecutionResult{Dirty: true, BlockedReason: "injected failure", AuditEventID: "audit-failed"}, context.DeadlineExceeded
	}
	if e.executeErr != nil || e.dirty {
		return operationscontract.DatabaseRetirementExecutionResult{Dirty: e.dirty, BlockedReason: e.blockedReason, AuditEventID: "audit-failed"}, e.executeErr
	}
	return operationscontract.DatabaseRetirementExecutionResult{ExecutedStatements: 1, AuditEventID: "audit-success"}, nil
}

func databaseRetirementPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "operations"}}, accessfixture.Bundle{Key: "database-operator", Permissions: []string{databaseRetirementPermission}})
}
