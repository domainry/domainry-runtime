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

type operationsLeaseReleaseRepositoryProbe struct {
	changed bool
	err     error
	request operationsmodel.OperationsLeaseReleaseRequest
}

func (p *operationsLeaseReleaseRepositoryProbe) OperationsLeaseSnapshot(context.Context, string, time.Time) (operationsmodel.OperationsLeaseSnapshot, error) {
	return operationsmodel.OperationsLeaseSnapshot{}, nil
}

func (p *operationsLeaseReleaseRepositoryProbe) ForceReleaseOperationsLease(_ context.Context, request operationsmodel.OperationsLeaseReleaseRequest) (operationsmodel.OperationsLeaseReleaseResult, bool, error) {
	p.request = request
	if p.err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, p.err
	}
	if !p.changed {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, nil
	}
	return operationsmodel.OperationsLeaseReleaseResult{Owner: request.Owner, WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID, PreviousLeaseOwner: request.ExpectedLeaseOwner, PreviousFencingToken: request.ExpectedFencingToken, NextFencingToken: request.ExpectedFencingToken + 1, PreviousExpiresAt: request.Now.Add(time.Minute), ReleasedAt: request.Now, Eligibility: "verified_stuck"}, true, nil
}

var errOperationsLeaseProbe = errors.New("lease release failed")

func TestOperationsLeaseForceReleaseProducesFencedReceiptAndReplay(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations := NewOperationsApplicationService(ledger, nil, func() time.Time { return now }, func() string { return "lease" })
	repository := &operationsLeaseReleaseRepositoryProbe{changed: true}
	service := NewOperationsLeaseApplicationService(repository, operations, func() time.Time { return now })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	command := OperationsLeaseReleaseCommand{Owner: "workflow", ResourceID: "execution-1", ExpectedLeaseOwner: "instance-a", ExpectedFencingToken: 7, VerifiedStuck: true, VerificationEvidence: "instance terminated and heartbeat absent", Reason: "incident recovery", Reference: "INC-42"}

	result, err := service.ForceRelease(t.Context(), command, "lease-key", principal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Release.NextFencingToken != 8 || result.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded || repository.request.WorkspaceID != "workspace-a" {
		t.Fatalf("result=%#v request=%#v", result, repository.request)
	}
	replayed, err := service.ForceRelease(t.Context(), command, "lease-key", principal)
	if err != nil || replayed.Receipt.Command.ID != result.Receipt.Command.ID || replayed.Release.NextFencingToken != 8 {
		t.Fatalf("replay=%#v err=%v", replayed, err)
	}
}

func TestOperationsLeaseForceReleaseRejectsFailedPrecondition(t *testing.T) {
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations := NewOperationsApplicationService(ledger, nil, nil, func() string { return "lease-conflict" })
	service := NewOperationsLeaseApplicationService(&operationsLeaseReleaseRepositoryProbe{}, operations, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.ForceRelease(t.Context(), OperationsLeaseReleaseCommand{Owner: "workflow", ResourceID: "execution-1", ExpectedLeaseOwner: "instance-a", ExpectedFencingToken: 7, Reason: "recovery"}, "lease-key", principal); err == nil {
		t.Fatal("failed lease precondition accepted")
	}
}
