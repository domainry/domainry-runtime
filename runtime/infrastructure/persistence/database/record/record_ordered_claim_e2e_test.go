package record_test

import (
	"sync"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func TestGenericOrderedClaimUsesPrioritySequenceCreatedAtAndAtomicCAS(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	object := definitionmodel.ObjectSchema{Key: "work_item", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}, {Key: "priority", Type: "integer"}, {Key: "sequence", Type: "integer"}, {Key: "claimed_by", Type: "text"}}}
	createRelationRLSTable(t, store, object)
	repository := recordStore(store)
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	for _, item := range []recordmodel.Record{
		{ID: "low", CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "ready", "priority": 1, "sequence": 1}},
		{ID: "high-second", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "ready", "priority": 10, "sequence": 2}},
		{ID: "high-first", CreatedAt: now.Add(time.Minute).Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "ready", "priority": 10, "sequence": 1}},
	} {
		if err := repository.InsertRecord(t.Context(), "workspace-primary", object, item); err != nil {
			t.Fatal(err)
		}
	}
	request := recordservice.RecordOrderedClaimRequest{WorkspaceID: "workspace-primary", Object: object, StatusField: "status", EligibleStatuses: []string{"ready"}, ClaimedStatus: "claimed", Now: now}
	for _, expected := range []string{"high-first", "high-second", "low"} {
		claimed, ok, err := recordservice.RecordClaimFirstEligible(t.Context(), repository, request)
		if err != nil || !ok || claimed.ID != expected {
			t.Fatalf("ordered claim got=%#v ok=%v err=%v want=%s", claimed, ok, err, expected)
		}
	}

	for index := 0; index < 100; index++ {
		insertRelationRLSRecord(t, repository, object, "concurrent-"+recordClaimIndex(index), map[string]any{"status": "ready", "priority": 20, "sequence": index})
	}
	claimedIDs := make(chan string, 100)
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			candidate, ok, err := recordservice.RecordClaimFirstEligible(t.Context(), repository, recordservice.RecordOrderedClaimRequest{WorkspaceID: "workspace-primary", Object: object, StatusField: "status", EligibleStatuses: []string{"ready"}, ClaimedStatus: "claimed", ClaimPatch: map[string]any{"claimed_by": recordClaimIndex(worker)}, Now: now})
			if err != nil {
				t.Errorf("worker %d: %v", worker, err)
				return
			}
			if ok {
				claimedIDs <- candidate.ID
			}
		}(index)
	}
	wait.Wait()
	close(claimedIDs)
	seen := map[string]bool{}
	for id := range claimedIDs {
		if seen[id] {
			t.Fatalf("record claimed twice: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 100 {
		t.Fatalf("claimed %d/100 records", len(seen))
	}
}

func recordClaimIndex(value int) string {
	const digits = "0123456789"
	if value < 10 {
		return "00" + string(digits[value])
	}
	if value < 100 {
		return "0" + string(digits[value/10]) + string(digits[value%10])
	}
	return "100"
}
