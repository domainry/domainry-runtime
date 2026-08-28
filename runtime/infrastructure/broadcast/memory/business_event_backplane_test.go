package memory

import (
	"context"
	"testing"

	businesseventmodel "github.com/domainry/domainry-runtime/runtime/domain/businessevent/model"
)

func TestBusinessEventBackplaneIsolatesWorkspacesAndBoundsReplay(t *testing.T) {
	backplane := NewBusinessEventBackplane(2, 2)
	for _, objectKey := range []string{"one", "two", "three"} {
		if _, err := backplane.Publish(t.Context(), businesseventmodel.BusinessEvent{WorkspaceID: "workspace-a", Type: businesseventmodel.EventTypeRefresh, ObjectKey: objectKey}); err != nil {
			t.Fatal(err)
		}
	}
	if event, err := backplane.Publish(t.Context(), businesseventmodel.BusinessEvent{WorkspaceID: "workspace-b", Type: businesseventmodel.EventTypeRefresh}); err != nil || event.ID != "1" {
		t.Fatalf("workspace-b cursor=%q err=%v", event.ID, err)
	}

	evicted, err := backplane.Open(t.Context(), "workspace-a", "1")
	if err != nil {
		t.Fatal(err)
	}
	defer evicted.Close()
	if !evicted.NeedsResync || evicted.CurrentEvent != "3" || len(evicted.Replay) != 0 {
		t.Fatalf("unexpected evicted replay: %+v", evicted)
	}

	replayed, err := backplane.Open(t.Context(), "workspace-a", "2")
	if err != nil {
		t.Fatal(err)
	}
	defer replayed.Close()
	if replayed.NeedsResync || len(replayed.Replay) != 1 || replayed.Replay[0].ObjectKey != "three" {
		t.Fatalf("unexpected replay: %+v", replayed)
	}
}

func TestBusinessEventBackplaneEvictsSlowConsumerAndCleansCancellation(t *testing.T) {
	backplane := NewBusinessEventBackplane(4, 1)
	ctx, cancel := context.WithCancel(t.Context())
	subscription, err := backplane.Open(ctx, "workspace-a", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = backplane.Publish(t.Context(), businesseventmodel.BusinessEvent{WorkspaceID: "workspace-a", Type: businesseventmodel.EventTypeRefresh})
	_, _ = backplane.Publish(t.Context(), businesseventmodel.BusinessEvent{WorkspaceID: "workspace-a", Type: businesseventmodel.EventTypeRefresh})
	<-subscription.Events
	if _, ok := <-subscription.Events; ok {
		t.Fatal("slow subscriber channel remained open")
	}
	cancel()
	subscription.Close()
}
