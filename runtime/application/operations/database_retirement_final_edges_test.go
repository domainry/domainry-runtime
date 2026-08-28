package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestDatabaseRetirementConstructorAuthorizationAndDiscoveryConditions(t *testing.T) {
	defaultClock := NewDatabaseRetirementApplicationService(nil, nil, nil, nil)
	if defaultClock.now().IsZero() {
		t.Fatal("default clock returned zero time")
	}
	service := NewDatabaseRetirementApplicationService(&databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{}}, nil, time.Now, func() string { return "id" })
	if _, err := NewDatabaseRetirementApplicationService(nil, nil, time.Now, nil).Discover(t.Context(), operationsmodel.DatabaseObjectIdentity{}, "owner", databaseRetirementPrincipal()); apperror.CodeOf(err) != "backend.operations.database_retirement_repository_unavailable" {
		t.Fatalf("nil repository error = %v", err)
	}
	for name, principal := range map[string]principalmodel.Principal{
		"unknown":       principalmodel.Principal{},
		"blank user":    principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "  "}},
		"no permission": principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "viewer"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Discover(t.Context(), operationsmodel.DatabaseObjectIdentity{}, "owner", principal); apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("authorization error = %v", err)
			}
		})
	}

	valid := operationsmodel.DatabaseRetirement{ID: "id", Object: operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old"}, Evidence: operationsmodel.DatabaseRetirementEvidence{Owner: "record"}}
	invalid := []operationsmodel.DatabaseRetirement{
		{Object: valid.Object, Evidence: valid.Evidence},
		{ID: valid.ID, Object: operationsmodel.DatabaseObjectIdentity{Database: "runtime", Kind: "table", Name: "old"}, Evidence: valid.Evidence},
		{ID: valid.ID, Object: operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Kind: "table", Name: "old"}, Evidence: valid.Evidence},
		{ID: valid.ID, Object: operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Name: "old"}, Evidence: valid.Evidence},
		{ID: valid.ID, Object: operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table"}, Evidence: valid.Evidence},
		{ID: valid.ID, Object: valid.Object},
	}
	for index, retirement := range invalid {
		if err := validateDatabaseRetirementDiscovery(retirement); err == nil {
			t.Fatalf("invalid discovery %d accepted", index)
		}
	}
	if err := validateDatabaseRetirementDiscovery(valid); err != nil {
		t.Fatalf("valid discovery rejected: %v", err)
	}
}

func TestDatabaseRetirementOperationalStatusConditionCombinations(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	fresh := now.Add(-time.Hour)
	stale := now.Add(-2 * operationspolicy.MaximumRetirementEvidenceAge)
	items := map[string]operationsmodel.DatabaseRetirement{
		"quiet": {ID: "quiet", State: operationsmodel.DatabaseRetirementDiscovered, Evidence: operationsmodel.DatabaseRetirementEvidence{}},
		"write": {ID: "write", State: operationsmodel.DatabaseRetirementDiscovered, Evidence: operationsmodel.DatabaseRetirementEvidence{Observation: operationsmodel.DatabaseAccessObservation{WriteCount: 1}, BackupVerifiedAt: &fresh, RestoreDrillAt: &fresh}},
		"fresh": {ID: "fresh", State: operationsmodel.DatabaseRetirementDiscovered, Evidence: operationsmodel.DatabaseRetirementEvidence{BackupVerifiedAt: &fresh, RestoreDrillAt: &fresh}},
		"stale": {ID: "stale", State: operationsmodel.DatabaseRetirementDiscovered, Evidence: operationsmodel.DatabaseRetirementEvidence{BackupVerifiedAt: &stale, RestoreDrillAt: &stale}},
	}
	service := NewDatabaseRetirementApplicationService(&databaseRetirementRepositoryFake{items: items}, nil, func() time.Time { return now }, nil)
	for id, wantAlerts := range map[string]int{"quiet": 0, "write": 1, "fresh": 0, "stale": 2} {
		status, err := service.OperationalStatus(t.Context(), id, databaseRetirementPrincipal())
		if err != nil || len(status.Alerts) != wantAlerts {
			t.Fatalf("%s alerts=%v err=%v", id, status.Alerts, err)
		}
	}
}

func TestDatabaseRetirementNestedFailuresAndPostEffectValidation(t *testing.T) {
	now := time.Date(2026, 7, 20, 2, 3, 4, 0, time.UTC)
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{}}
	executor := &databaseRetirementExecutorFake{}
	service := NewDatabaseRetirementApplicationService(repository, executor, func() time.Time { return now }, nil)
	principal := databaseRetirementPrincipal()
	if _, err := service.Preview(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("preview status error = %v", err)
	}
	if _, err := service.Advance(t.Context(), "missing", operationsmodel.DatabaseRetirementReplacementReady, operationsmodel.DatabaseRetirementEvidence{}, principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("advance status error = %v", err)
	}
	if _, err := service.Execute(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("execute status error = %v", err)
	}

	current := operationsDatabaseRetirementFixture(now)
	repository.items[current.ID] = current
	executor.applyResult = func(next operationsmodel.DatabaseRetirement) operationsmodel.DatabaseRetirement {
		next.Evidence.Owner = ""
		return next
	}
	if _, err := service.Advance(t.Context(), current.ID, operationsmodel.DatabaseRetirementReplacementReady, current.Evidence, principal); apperror.KindOf(err) != apperror.KindConflict {
		t.Fatalf("post-effect validation error = %v", err)
	}

	quarantineEnded := now.Add(-time.Minute)
	current.State = operationsmodel.DatabaseRetirementQuarantined
	current.Evidence.QuarantineUntil = &quarantineEnded
	repository.items[current.ID] = current
	executor.applyResult = nil
	quarantineActive := now.Add(time.Minute)
	current.Evidence.QuarantineUntil = &quarantineActive
	repository.items[current.ID] = current
	if _, err := service.Execute(t.Context(), current.ID, principal); apperror.KindOf(err) != apperror.KindConflict {
		t.Fatalf("active quarantine validation error = %v", err)
	}
	current.Evidence.QuarantineUntil = &quarantineEnded
	repository.items[current.ID] = current
	executor.previewErr = errors.New("preview unavailable")
	if _, err := service.Execute(t.Context(), current.ID, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_preview_blocked" {
		t.Fatalf("execute preview error = %v", err)
	}
	executor.previewErr = nil
	executor.dirty = true
	executor.blockedReason = ""
	if _, err := service.Execute(t.Context(), current.ID, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_execute_failed" {
		t.Fatalf("dirty execution error = %v", err)
	}
	if got := repository.items[current.ID].BlockedReason; got != "destructive execution failed" {
		t.Fatalf("fallback blocked reason = %q", got)
	}

	// An execution error without a dirty result must still force the retirement into a blocked state.
	repository.items[current.ID] = current
	executor.dirty = false
	executor.executeErr = context.DeadlineExceeded
	if _, err := service.Execute(t.Context(), current.ID, principal); apperror.CodeOf(err) != "backend.operations.database_retirement_execute_failed" {
		t.Fatalf("execution error = %v", err)
	}
}
