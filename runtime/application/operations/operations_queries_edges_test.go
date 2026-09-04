package operations

import (
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestDatabaseRetirementOperationalStatusAndListQueries(t *testing.T) {
	now := time.Date(2026, 7, 19, 13, 14, 15, 0, time.UTC)
	stale := now.Add(-2 * operationspolicy.MaximumRetirementEvidenceAge)
	retirement := operationsmodel.DatabaseRetirement{
		ID: "retirement-1", State: operationsmodel.DatabaseRetirementBlocked, BlockedReason: "access observed",
		Evidence: operationsmodel.DatabaseRetirementEvidence{
			Owner: "record", BackupVerifiedAt: &stale, RestoreDrillAt: &stale,
			Observation: operationsmodel.DatabaseAccessObservation{WindowEnds: now.Add(time.Minute), ReadCount: 1},
		},
	}
	repository := &databaseRetirementRepositoryFake{items: map[string]operationsmodel.DatabaseRetirement{retirement.ID: retirement}}
	service := NewDatabaseRetirementApplicationService(repository, nil, func() time.Time { return now }, nil)
	unauthorized := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "viewer"}}
	if _, err := service.OperationalStatus(t.Context(), retirement.ID, unauthorized); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("status authorization error = %v", err)
	}
	if _, err := service.List(t.Context(), "", 10, unauthorized); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("list authorization error = %v", err)
	}
	principal := databaseRetirementPrincipal()
	status, err := service.OperationalStatus(t.Context(), retirement.ID, principal)
	if err != nil || status.ObservationRemainingSeconds != 60 || len(status.Alerts) != 4 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	items, err := service.List(t.Context(), operationsmodel.DatabaseRetirementBlocked, 10, principal)
	if err != nil || len(items) != 1 || items[0].ID != retirement.ID {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	status, err = service.OperationalStatus(t.Context(), retirement.ID, principal)
	if err != nil || status.ObservationRemainingSeconds != 0 {
		t.Fatalf("expired observation status=%#v err=%v", status, err)
	}
}

func TestOperationsControlBreakGlassAndDeadLetterQuerySurfaces(t *testing.T) {
	admin := operationsAdminPrincipal()
	unauthorized := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: "viewer"}}
	control := operationsmodel.OperationsControl{SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime"}
	controls := &operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{"control": control}}
	controlService := NewOperationsControlApplicationService(controls, nil, nil, nil)
	if _, err := controlService.List(t.Context(), "", 10, unauthorized); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("control authorization error = %v", err)
	}
	listedControls, err := controlService.List(t.Context(), operationsmodel.OperationsControlMaintenance, 10, admin)
	if err != nil || len(listedControls) != 1 {
		t.Fatalf("controls=%#v err=%v", listedControls, err)
	}

	now := time.Date(2026, 7, 19, 14, 15, 16, 0, time.UTC)
	operations := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, func() time.Time { return now }, nil)
	breakGlass := &breakGlassRepositoryProbe{grants: map[string]operationsmodel.OperationsBreakGlassGrant{
		"expired": {ID: "expired", WorkspaceID: admin.WorkspaceID, State: operationsmodel.OperationsBreakGlassActive, ExpiresAt: now},
	}}
	if err := operations.RegisterBreakGlass(breakGlass, &breakGlassAlertProbe{}); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.ListBreakGlass(t.Context(), 10, unauthorized); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("break-glass authorization error = %v", err)
	}
	grants, err := operations.ListBreakGlass(t.Context(), 10, admin)
	if err != nil || len(grants) != 1 || grants[0].State != operationsmodel.OperationsBreakGlassExpired {
		t.Fatalf("grants=%#v err=%v", grants, err)
	}

	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"item-1": {Owner: "probe", ID: "item-1"}}}
	if err := operations.RegisterDeadLetterOwner("probe", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.InspectDeadLetter(t.Context(), "probe", "item-1", unauthorized); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("dead-letter authorization error = %v", err)
	}
	if _, err := operations.InspectDeadLetter(t.Context(), "missing", "item-1", admin); apperror.CodeOf(err) != "backend.operations.dead_letter_owner_not_registered" {
		t.Fatalf("missing owner error = %v", err)
	}
	item, err := operations.InspectDeadLetter(t.Context(), " probe ", " item-1 ", admin)
	if err != nil || item.ID != "item-1" {
		t.Fatalf("item=%#v err=%v", item, err)
	}
}
