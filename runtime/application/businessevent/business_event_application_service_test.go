package businessevent

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	apperror "github.com/domainry/domainry-foundation/apperror"
	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	businesseventmodel "github.com/domainry/domainry-runtime/runtime/domain/businessevent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	businesseventmemory "github.com/domainry/domainry-runtime/runtime/infrastructure/broadcast/memory"
)

func TestBusinessEventServiceEnforcesIdentityWorkspaceCapacityAndCleanup(t *testing.T) {
	service := NewBusinessEventApplicationService(businesseventmemory.NewBusinessEventBackplane(4, 2), Limits{GlobalConnections: 2, WorkspaceConnections: 1, PrincipalConnections: 1})
	if _, err := service.Open(t.Context(), principalmodel.Principal{}, ""); apperror.CodeOf(err) != "backend.event_stream.workspace_required" {
		t.Fatalf("empty principal error=%v", err)
	}
	if _, err := service.Open(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}, ""); apperror.CodeOf(err) != "backend.event_stream.identity_required" {
		t.Fatalf("unknown principal error=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	first, err := service.Open(t.Context(), principal, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Open(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-b"}}, ""); apperror.CodeOf(err) != "backend.event_stream.capacity_exceeded" {
		t.Fatalf("capacity error=%v", err)
	}
	first.Close()
	second, err := service.Open(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-b"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	stats := service.Snapshot(t.Context())
	if stats.ActiveConnections != 0 || stats.OpenedTotal != 2 || stats.ClosedTotal != 2 || stats.RejectedTotal != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestBusinessEventServiceCountsBackplaneFailuresWithoutAuditEvidence(t *testing.T) {
	backplaneErr := errors.New("backplane unavailable")
	service := NewBusinessEventApplicationService(failingBusinessEventBackplane{err: backplaneErr}, Limits{})
	if _, err := service.Publish(t.Context(), "workspace-a", "customer", "mutation"); apperror.CodeOf(err) != "backend.event_stream.unavailable" {
		t.Fatalf("publish error=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	if _, err := service.Open(t.Context(), principal, ""); apperror.CodeOf(err) != "backend.event_stream.unavailable" {
		t.Fatalf("open error=%v", err)
	}
	stats := service.Snapshot(t.Context())
	if stats.ActiveConnections != 0 || stats.OpenedTotal != 0 || stats.ClosedTotal != 0 || stats.OpenFailedTotal != 1 || stats.PublishedTotal != 0 || stats.PublishFailedTotal != 1 {
		t.Fatalf("unexpected failure stats: %+v", stats)
	}
}

func TestBusinessEventServicePublishesSafeWorkspaceSignal(t *testing.T) {
	service := NewBusinessEventApplicationService(businesseventmemory.NewBusinessEventBackplane(4, 2), Limits{})
	event, err := service.Publish(t.Context(), "workspace-a", "customer", "mutation")
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "1" || event.WorkspaceID != "workspace-a" || event.ObjectKey != "customer" || event.Reason != "mutation" || event.OccurredAt.IsZero() {
		t.Fatalf("unexpected event: %+v", event)
	}
	if _, err := service.Publish(t.Context(), "", "customer", "mutation"); apperror.CodeOf(err) != "backend.event_stream.workspace_required" {
		t.Fatalf("missing workspace error=%v", err)
	}
}

func TestInjectedBackplaneBroadcastsAcrossServiceInstances(t *testing.T) {
	backplane := businesseventmemory.NewBusinessEventBackplane(8, 4)
	instanceA := NewBusinessEventApplicationService(backplane, Limits{})
	instanceB := NewBusinessEventApplicationService(backplane, Limits{})
	subscription, err := instanceB.Open(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-b"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	published, err := instanceA.Publish(t.Context(), "workspace-a", "customer", "mutation")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-subscription.Events:
		if received.ID != published.ID || received.WorkspaceID != "workspace-a" || received.ObjectKey != "customer" {
			t.Fatalf("cross-instance event mismatch: published=%+v received=%+v", published, received)
		}
	case <-t.Context().Done():
		t.Fatal("cross-instance event was not delivered")
	}
}

type failingBusinessEventBackplane struct{ err error }

func (f failingBusinessEventBackplane) Mode(context.Context) string {
	return businesseventcontract.BackplaneModeShared
}

func (f failingBusinessEventBackplane) Publish(context.Context, businesseventmodel.BusinessEvent) (businesseventmodel.BusinessEvent, error) {
	return businesseventmodel.BusinessEvent{}, f.err
}

func (f failingBusinessEventBackplane) Open(context.Context, string, string) (businesseventcontract.Subscription, error) {
	return businesseventcontract.Subscription{}, f.err
}
