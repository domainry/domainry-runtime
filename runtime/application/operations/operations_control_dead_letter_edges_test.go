package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestOperationsControlValidationOperationMappingAndNextActions(t *testing.T) {
	var nilService *OperationsControlApplicationService
	if _, err := nilService.Set(t.Context(), OperationsControlRequest{}, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.control_unavailable" {
		t.Fatalf("unavailable err=%v", err)
	}
	service := NewOperationsControlApplicationService(&operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}}, NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, nil), nil, nil)
	for _, request := range []OperationsControlRequest{
		{Kind: "invalid", Owner: "runtime", Reason: "reason"},
		{Kind: operationsmodel.OperationsControlMaintenance, Owner: " ", Reason: "reason"},
		{Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", Reason: " "},
		{Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", Reason: "reason", ExpectedRevision: -1},
	} {
		if _, err := service.Set(t.Context(), request, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.control_invalid" {
			t.Fatalf("request=%#v err=%v", request, err)
		}
	}
	for _, test := range []struct {
		kind      operationsmodel.OperationsControlKind
		active    bool
		operation string
		resource  string
	}{
		{operationsmodel.OperationsControlMaintenance, true, "runtime.maintenance.enable", "runtime"},
		{operationsmodel.OperationsControlMaintenance, false, "runtime.maintenance.disable", "runtime"},
		{operationsmodel.OperationsControlWorkerPause, true, "worker.owner.pause", "worker_owner"},
		{operationsmodel.OperationsControlWorkerPause, false, "worker.owner.resume", "worker_owner"},
		{operationsmodel.OperationsControlInstanceDrain, true, "runtime.instance.drain", "runtime_instance"},
		{operationsmodel.OperationsControlInstanceDrain, false, "runtime.instance.undrain", "runtime_instance"},
	} {
		operation, resource := operationsControlOperation(test.kind, test.active)
		if operation != test.operation || resource != test.resource {
			t.Fatalf("kind=%s active=%t operation=%s resource=%s", test.kind, test.active, operation, resource)
		}
	}
	if action := operationsControlNextAction(operationsmodel.OperationsControl{State: operationsmodel.OperationsControlInactive}, nil); action != "verify readiness and owner processing have recovered" {
		t.Fatalf("inactive action=%q", action)
	}
}

func TestOperationsControlReplayReadAndTransitionFailures(t *testing.T) {
	principal := operationsAdminPrincipal()
	request := OperationsControlRequest{Kind: operationsmodel.OperationsControlWorkerPause, Owner: " worker ", Active: true, Reason: " pause ", Reference: " ref "}
	for _, test := range []struct {
		name       string
		failAt     int
		putErr     error
		putChanged *bool
		code       string
	}{
		{"authorization", 0, nil, nil, "backend.workspace_scope_required"},
		{"start", 1, nil, nil, "backend.operations.transition_failed"},
		{"put-error", 0, errOperationsControlProbe, nil, "backend.operations.control_revision_conflict"},
		{"put-unchanged", 0, nil, boolPointer(false), "backend.operations.control_revision_conflict"},
		{"finish", 2, nil, nil, "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, failAt: test.failAt}
			controls := &operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}, putErr: test.putErr, putChanged: test.putChanged}
			service := NewOperationsControlApplicationService(controls, NewOperationsApplicationService(ledger, nil, nil, func() string { return test.name }), nil, nil)
			testPrincipal := principal
			if test.name == "authorization" {
				testPrincipal = operationsAdminPrincipal()
				testPrincipal.WorkspaceID = ""
			}
			if _, err := service.Set(t.Context(), request, "key", testPrincipal); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}

	for _, test := range []struct {
		name   string
		getErr error
	}{
		{"missing", nil},
		{"read-error", errOperationsControlProbe},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
			controls := &operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}, getErr: test.getErr}
			service := NewOperationsControlApplicationService(controls, NewOperationsApplicationService(ledger, nil, nil, func() string { return test.name }), nil, nil)
			first, err := service.Set(t.Context(), request, "key", principal)
			if err != nil {
				t.Fatal(err)
			}
			delete(controls.controls, operationsmodel.OperationsSystemPurposeRuntimeControl+":"+string(request.Kind)+":"+"worker")
			if _, err := service.Set(t.Context(), request, "key", principal); apperror.CodeOf(err) != "backend.operations.control_read_failed" {
				t.Fatalf("first=%#v err=%v", first, err)
			}
		})
	}
}

func TestOperationsControlDrainSnapshotAndContextFailures(t *testing.T) {
	principal := operationsAdminPrincipal()
	request := OperationsControlRequest{Kind: operationsmodel.OperationsControlInstanceDrain, Owner: "instance-a", Active: true, Reason: "deploy"}
	service := NewOperationsControlApplicationService(&operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}}, NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "snapshot-error" }), operationsLeaseRepositoryProbe{err: errOperationsControlProbe}, nil)
	service.drainSettleDelay = time.Nanosecond
	if _, err := service.Set(t.Context(), request, "key", principal); apperror.CodeOf(err) != "backend.operations.drain_snapshot_failed" || !errors.Is(err, errOperationsControlProbe) {
		t.Fatalf("snapshot err=%v", err)
	}

	service = NewOperationsControlApplicationService(&operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}}, nil, operationsLeaseRepositoryProbe{}, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.waitForInstanceDrain(ctx, "instance-a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	service.drainSettleDelay = time.Nanosecond
	service.drainWaitTimeout = time.Second
	snapshot, err := service.waitForInstanceDrain(t.Context(), "instance-a")
	if err != nil || snapshot.Live != 0 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestOperationsDeadLetterRegistrationActionAndFailureEdges(t *testing.T) {
	var nilService *OperationsApplicationService
	if err := nilService.RegisterDeadLetterOwner("owner", &bulkDeadLetterOwnerProbe{}); apperror.CodeOf(err) != "backend.operations.dead_letter_owner_invalid" {
		t.Fatalf("nil registration err=%v", err)
	}
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "dead-edge" })
	if err := service.RegisterDeadLetterOwner(" ", &bulkDeadLetterOwnerProbe{}); apperror.CodeOf(err) != "backend.operations.dead_letter_owner_invalid" {
		t.Fatalf("empty owner err=%v", err)
	}
	if err := service.RegisterDeadLetterOwner("owner", nil); apperror.CodeOf(err) != "backend.operations.dead_letter_owner_invalid" {
		t.Fatalf("nil adapter err=%v", err)
	}
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"a": {ID: "a", AllowedActions: []string{"retry"}}}}
	if err := service.RegisterDeadLetterOwner(" owner ", owner); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterDeadLetterOwner("owner", owner); apperror.CodeOf(err) != "backend.operations.dead_letter_owner_duplicate" {
		t.Fatalf("duplicate err=%v", err)
	}
	if _, err := service.ActOnDeadLetter(t.Context(), "owner", "a", "delete", OperationsDeadLetterActionRequest{}, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.dead_letter_action_invalid" {
		t.Fatalf("action err=%v", err)
	}
	if _, err := service.ActOnDeadLetter(t.Context(), "missing", "a", "retry", OperationsDeadLetterActionRequest{}, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.dead_letter_owner_not_registered" {
		t.Fatalf("owner err=%v", err)
	}
	if _, err := nilService.deadLetterOwner("owner"); apperror.CodeOf(err) != "backend.operations.unavailable" {
		t.Fatalf("nil service err=%v", err)
	}
}

func TestOperationsDeadLetterReplayOwnerAndTransitionFailures(t *testing.T) {
	principal := operationsAdminPrincipal()
	request := OperationsDeadLetterActionRequest{Reason: "recover", Reference: "INC-1"}
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "dead-replay" })
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"a": {ID: "a", AllowedActions: []string{"retry"}}}}
	_ = service.RegisterDeadLetterOwner("owner", owner)
	if _, err := service.ActOnDeadLetter(t.Context(), "owner", "a", "retry", request, "key", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(repository, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.ActOnDeadLetter(t.Context(), "owner", "a", "retry", request, "key", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_receipt_invalid" {
		t.Fatalf("replay err=%v", err)
	}

	for _, test := range []struct {
		name   string
		failAt int
		actErr error
		code   string
	}{
		{"authorization", 0, nil, "backend.workspace_scope_required"},
		{"start", 1, nil, "backend.operations.transition_failed"},
		{"owner", 0, errors.New("owner rejected"), ""},
		{"finish", 2, nil, "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, failAt: test.failAt}
			service := NewOperationsApplicationService(ledger, nil, nil, func() string { return test.name })
			owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"a": {ID: "a"}}, actErr: map[string]error{"a": test.actErr}}
			_ = service.RegisterDeadLetterOwner("owner", owner)
			testPrincipal := principal
			if test.name == "authorization" {
				testPrincipal.WorkspaceID = ""
			}
			_, err := service.ActOnDeadLetter(t.Context(), "owner", "a", "retry", request, "key", testPrincipal)
			if test.name == "owner" {
				if !errors.Is(err, test.actErr) {
					t.Fatalf("err=%v", err)
				}
			} else if apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
