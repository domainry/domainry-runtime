package operations

import (
	"errors"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func operationsDatabaseRetirementFixture(now time.Time) operationsmodel.DatabaseRetirement {
	verified, drill := now.Add(-time.Hour), now.Add(-2*time.Hour)
	return operationsmodel.DatabaseRetirement{
		ID: "retire-1", Object: operationsmodel.DatabaseObjectIdentity{Engine: "postgres", Database: "runtime", Schema: "public", Kind: "table", Name: "old_table"}, State: operationsmodel.DatabaseRetirementDiscovered, UpdatedAt: now,
		Evidence: operationsmodel.DatabaseRetirementEvidence{
			Owner: "record", Replacement: "new_table", ExpectedSchemaVersion: "2", ExpectedDataVersion: "2", BackfillCheckpoint: "checkpoint", BackfillComplete: true,
			ReadsSwitchVersion: "2.1", WritesDisableVersion: "2.2", WriteProtection: "trigger", Observation: operationsmodel.DatabaseAccessObservation{WindowStarted: now.Add(-48 * time.Hour), WindowEnds: now.Add(-24 * time.Hour), SourceCounts: map[string]uint64{"runtime": 0}},
			Comparison:  operationsmodel.DatabaseDataComparison{SourceRows: 1, ReplacementRows: 1, SourceKeys: 1, ReplacementKeys: 1, SourceHash: "same", ReplacementHash: "same", BusinessChecks: []string{"count"}, ComparedAt: now.Add(-25 * time.Hour)},
			Disposition: "migrate", BackupID: "backup", BackupChecksum: "checksum", BackupVerifiedAt: &verified, RestoreDrillAt: &drill, MaintenanceEvidence: "maintenance", DrainEvidence: "drain", ChangePlanID: "change", ApprovalID: "approval", Rollback: "restore", AuditEventID: "audit",
		},
	}
}

func TestDatabaseRetirementDiscoverStatusAndPreviewFailureBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 19, 16, 17, 18, 0, time.UTC)
	principal := databaseRetirementPrincipal()
	var nilService *DatabaseRetirementApplicationService
	if _, err := nilService.Discover(t.Context(), operationsmodel.DatabaseObjectIdentity{}, "owner", principal); apperror.CodeOf(err) != "backend.operations.database_retirement_repository_unavailable" {
		t.Fatalf("nil service error = %v", err)
	}
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{}}
	executor := &databaseRetirementExecutorFake{}
	service := NewDatabaseRetirementApplicationService(repository, executor, func() time.Time { return now }, func() string { return "fixed" })
	if _, err := service.Discover(t.Context(), operationsmodel.DatabaseObjectIdentity{Engine: "sqlite"}, "", principal); apperror.KindOf(err) != apperror.KindBadRequest {
		t.Fatalf("discovery validation error = %v", err)
	}
	repositoryFailure := errors.New("database retirement repository failed")
	repository.registerErr = repositoryFailure
	object := operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old_table"}
	if _, err := service.Discover(t.Context(), object, "record", principal); apperror.CodeOf(err) != "backend.operations.database_retirement_register_failed" || !errors.Is(err, repositoryFailure) {
		t.Fatalf("register error = %v", err)
	}
	repository.registerErr = nil
	repository.items["database_retirement_fixed"] = operationsmodel.DatabaseRetirement{ID: "database_retirement_fixed"}
	if _, err := service.Discover(t.Context(), object, "record", principal); apperror.CodeOf(err) != "backend.operations.database_retirement_exists" {
		t.Fatalf("duplicate error = %v", err)
	}
	delete(repository.items, "database_retirement_fixed")
	discovered, err := service.Discover(t.Context(), object, " record ", principal)
	if err != nil || discovered.ID != "database_retirement_fixed" || discovered.Evidence.Owner != "record" {
		t.Fatalf("discovered=%#v err=%v", discovered, err)
	}

	repository.getErr = repositoryFailure
	if _, err := service.Status(t.Context(), discovered.ID, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_read_failed" {
		t.Fatalf("status read error = %v", err)
	}
	repository.getErr = nil
	if _, err := service.Status(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("status not-found error = %v", err)
	}
	repository.listErr = repositoryFailure
	if _, err := service.List(t.Context(), "", 10, principal); !errors.Is(err, repositoryFailure) {
		t.Fatalf("list error = %v", err)
	}
	repository.listErr = nil

	service.executor = nil
	if _, err := service.Preview(t.Context(), discovered.ID, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_executor_unavailable" {
		t.Fatalf("missing executor error = %v", err)
	}
	service.executor = executor
	executor.previewErr = errors.New("preview failed")
	if _, err := service.Preview(t.Context(), discovered.ID, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_preview_blocked" {
		t.Fatalf("preview error = %v", err)
	}
	executor.previewErr = nil
	executor.invalidPlan = true
	if _, err := service.Preview(t.Context(), discovered.ID, principal); apperror.KindOf(err) != apperror.KindConflict {
		t.Fatalf("invalid plan error = %v", err)
	}
	executor.invalidPlan = false
	complete := discovered
	complete.Evidence = operationsDatabaseRetirementFixture(now).Evidence
	repository.items[discovered.ID] = complete
	plan, err := service.Preview(t.Context(), discovered.ID, principal)
	if err != nil || plan.RetirementID != discovered.ID {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
}

func TestDatabaseRetirementAdvancePersistenceAndIdempotencyEdges(t *testing.T) {
	now := time.Date(2026, 7, 19, 17, 18, 19, 0, time.UTC)
	current := operationsDatabaseRetirementFixture(now)
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{current.ID: current}}
	executor := &databaseRetirementExecutorFake{}
	service := NewDatabaseRetirementApplicationService(repository, executor, func() time.Time { return now }, nil)
	principal := databaseRetirementPrincipal()
	differentEvidence := current.Evidence
	differentEvidence.Replacement = "different"
	if _, err := service.Advance(t.Context(), current.ID, current.State, differentEvidence, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_idempotency_conflict" {
		t.Fatalf("idempotency error = %v", err)
	}
	if _, err := service.Advance(t.Context(), current.ID, operationsmodel.DatabaseRetirementBackfilled, current.Evidence, principal); apperror.KindOf(err) != apperror.KindConflict {
		t.Fatalf("invalid transition error = %v", err)
	}
	service.executor = nil
	if _, err := service.Advance(t.Context(), current.ID, operationsmodel.DatabaseRetirementReplacementReady, current.Evidence, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_executor_unavailable" {
		t.Fatalf("missing executor error = %v", err)
	}
	service.executor = executor
	executor.applyErr = errors.New("transition effect failed")
	if _, err := service.Advance(t.Context(), current.ID, operationsmodel.DatabaseRetirementReplacementReady, current.Evidence, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_transition_effect_failed" {
		t.Fatalf("effect error = %v", err)
	}
	executor.applyErr = nil
	repository.transitionErr = errors.New("transition save failed")
	if _, err := service.Advance(t.Context(), current.ID, operationsmodel.DatabaseRetirementReplacementReady, current.Evidence, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_transition_failed" {
		t.Fatalf("save error = %v", err)
	}
	repository.transitionErr = nil
	changed := false
	repository.transitionChanged = &changed
	if _, err := service.Advance(t.Context(), current.ID, operationsmodel.DatabaseRetirementReplacementReady, current.Evidence, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_transition_conflict" {
		t.Fatalf("transition conflict = %v", err)
	}
	repository.transitionChanged = nil
	advanced, err := service.Advance(t.Context(), current.ID, operationsmodel.DatabaseRetirementReplacementReady, current.Evidence, principal)
	if err != nil || advanced.State != operationsmodel.DatabaseRetirementReplacementReady {
		t.Fatalf("advanced=%#v err=%v", advanced, err)
	}
}

func TestDatabaseRetirementExecuteCommitFailureAndSuccess(t *testing.T) {
	now := time.Date(2026, 7, 19, 18, 19, 20, 0, time.UTC)
	current := operationsDatabaseRetirementFixture(now)
	quarantineEnded := now.Add(-time.Minute)
	current.State, current.Evidence.QuarantineUntil = operationsmodel.DatabaseRetirementQuarantined, &quarantineEnded
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{current.ID: current}}
	executor := &databaseRetirementExecutorFake{}
	service := NewDatabaseRetirementApplicationService(repository, executor, func() time.Time { return now }, nil)
	principal := databaseRetirementPrincipal()
	repository.transitionErr = errors.New("drop commit failed")
	result, err := service.Execute(t.Context(), current.ID, principal)
	if result.ExecutedStatements != 1 || apperror.CodeOf(err) != "backend.operations.database_retirement_commit_failed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	repository.transitionErr = nil
	changed := false
	repository.transitionChanged = &changed
	if _, err := service.Execute(t.Context(), current.ID, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_commit_failed" {
		t.Fatalf("commit conflict error = %v", err)
	}
	repository.transitionChanged = nil
	result, err = service.Execute(t.Context(), current.ID, principal)
	if err != nil || !executor.executed || result.AuditEventID != "audit-success" || repository.items[current.ID].State != operationsmodel.DatabaseRetirementDropped {
		t.Fatalf("result=%#v stored=%#v err=%v", result, repository.items[current.ID], err)
	}
}
