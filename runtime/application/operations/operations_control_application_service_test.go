package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type operationsControlRepositoryProbe struct {
	controls   map[string]operationsmodel.OperationsControl
	getErr     error
	listErr    error
	putErr     error
	putChanged *bool
}

type operationsLeaseRepositoryProbe struct {
	snapshot operationsmodel.OperationsLeaseSnapshot
	err      error
}

func (p operationsLeaseRepositoryProbe) OperationsLeaseSnapshot(context.Context, string, time.Time) (operationsmodel.OperationsLeaseSnapshot, error) {
	return p.snapshot, p.err
}

func (p operationsLeaseRepositoryProbe) ForceReleaseOperationsLease(context.Context, operationsmodel.OperationsLeaseReleaseRequest) (operationsmodel.OperationsLeaseReleaseResult, bool, error) {
	return operationsmodel.OperationsLeaseReleaseResult{}, false, nil
}

func (p *operationsControlRepositoryProbe) GetOperationsControl(_ context.Context, purpose string, kind operationsmodel.OperationsControlKind, owner string) (operationsmodel.OperationsControl, bool, error) {
	if p.getErr != nil {
		return operationsmodel.OperationsControl{}, false, p.getErr
	}
	control, found := p.controls[purpose+":"+string(kind)+":"+owner]
	return control, found, nil
}

func (p *operationsControlRepositoryProbe) ListOperationsControls(_ context.Context, purpose string, kind operationsmodel.OperationsControlKind, _ int) ([]operationsmodel.OperationsControl, error) {
	if p.listErr != nil {
		return nil, p.listErr
	}
	result := []operationsmodel.OperationsControl{}
	for _, control := range p.controls {
		if control.SystemPurpose == purpose && (kind == "" || control.Kind == kind) {
			result = append(result, control)
		}
	}
	return result, nil
}

func (p *operationsControlRepositoryProbe) PutOperationsControl(_ context.Context, control operationsmodel.OperationsControl, expectedRevision int64) (bool, error) {
	if p.putErr != nil {
		return false, p.putErr
	}
	if p.putChanged != nil {
		return *p.putChanged, nil
	}
	key := control.SystemPurpose + ":" + string(control.Kind) + ":" + control.Owner
	existing, found := p.controls[key]
	if (!found && expectedRevision != 0) || (found && existing.Revision != expectedRevision) {
		return false, nil
	}
	p.controls[key] = control
	return true, nil
}

var errOperationsControlProbe = errors.New("control probe failed")

func TestOperationsControlPersistsSystemStateAndReceipt(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations := NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string { return "control" })
	controls := &operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}}
	service := NewOperationsControlApplicationService(controls, operations, nil, func() time.Time { return now })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"runtime.operations.enable_maintenance"}})

	result, err := service.Set(t.Context(), OperationsControlRequest{Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", Active: true, Reason: "restore drill", Reference: "CHG-42"}, "maintenance-1", principal)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Control.Active() || result.Control.Revision != 1 || result.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded || result.Receipt.Command.Scope.SystemPurpose != operationsmodel.OperationsSystemPurposeRuntimeControl {
		t.Fatalf("result=%#v", result)
	}
	if result.Receipt.Correlation == "" || len(result.Receipt.RelatedIDs) == 0 || len(result.Receipt.Evidence) == 0 || result.Receipt.NextAction == "" {
		t.Fatalf("terminal receipt missing recovery evidence: %#v", result.Receipt)
	}
	replayed, err := service.Set(t.Context(), OperationsControlRequest{Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", Active: true, Reason: "restore drill", Reference: "CHG-42"}, "maintenance-1", principal)
	if err != nil || replayed.Control.Revision != 1 || replayed.Receipt.Command.ID != result.Receipt.Command.ID {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
}

func TestOperationsControlRejectsStaleRevision(t *testing.T) {
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations := NewOperationsApplicationService(ledger, nil, nil, func() string { return "stale" })
	controls := &operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{
		operationsmodel.OperationsSystemPurposeRuntimeControl + ":maintenance:runtime": {SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", State: operationsmodel.OperationsControlActive, Revision: 2},
	}}
	service := NewOperationsControlApplicationService(controls, operations, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"runtime.operations.disable_maintenance"}})
	if _, err := service.Set(t.Context(), OperationsControlRequest{Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", Active: false, Reason: "complete", ExpectedRevision: 1}, "maintenance-2", principal); err == nil {
		t.Fatal("stale revision accepted")
	}
}

func TestOperationsControlDrainWaitsAndReturnsRemainingLeaseSnapshot(t *testing.T) {
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations := NewOperationsApplicationService(ledger, nil, nil, func() string { return "drain" })
	controls := &operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}}
	leases := operationsLeaseRepositoryProbe{snapshot: operationsmodel.OperationsLeaseSnapshot{InstanceID: "instance-a", Live: 2, Owners: []operationsmodel.OperationsLeaseCount{{Owner: "workflow", Live: 2}}}}
	service := NewOperationsControlApplicationService(controls, operations, leases, nil)
	service.drainSettleDelay, service.drainWaitTimeout = time.Millisecond, 5*time.Millisecond
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"runtime.operations.drain_runtime_instance"}})
	result, err := service.Set(t.Context(), OperationsControlRequest{Kind: operationsmodel.OperationsControlInstanceDrain, Owner: "instance-a", Active: true, Reason: "rolling deploy"}, "drain-1", principal)
	if err != nil {
		t.Fatal(err)
	}
	if result.DrainSnapshot == nil || result.DrainSnapshot.Live != 2 || result.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded || result.Receipt.NextAction == "" {
		t.Fatalf("result=%#v", result)
	}
}
