package integration

import (
	"strconv"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationEventStoreLifecycleEdges(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationEventStore(store)
	event, duplicate, err := repository.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{Provider: "crm", EventType: "customer.updated", ExternalID: "event-1", Payload: map[string]any{"id": "one"}})
	if err != nil || duplicate || event.Status != "received" {
		t.Fatalf("event=%+v duplicate=%v err=%v", event, duplicate, err)
	}
	for _, limit := range []int{0, 201, 1} {
		values, err := repository.ListEvents(t.Context(), "default", " crm ", " received ", limit)
		if err != nil || len(values) != 1 {
			t.Fatalf("events(limit=%d)=%+v err=%v", limit, values, err)
		}
	}
	if _, found, err := repository.GetEvent(t.Context(), "default", "missing"); err != nil || found {
		t.Fatalf("missing event found=%v err=%v", found, err)
	}
	if _, err := repository.UpdateEventStatus(t.Context(), "default", " ", "processed", ""); err == nil {
		t.Fatal("empty event status id accepted")
	}
	if _, err := repository.UpdateEventStatus(t.Context(), "default", "missing", "processed", ""); err == nil {
		t.Fatal("missing event status update succeeded")
	}
	event, err = repository.UpdateEventStatus(t.Context(), "default", event.ID, "processed", "")
	if err != nil || event.Status != "processed" {
		t.Fatalf("updated event=%+v err=%v", event, err)
	}
	if _, err := repository.ScheduleEventRetry(t.Context(), "default", " ", 1, "failed"); err == nil {
		t.Fatal("empty retry event id accepted")
	}
	if _, err := repository.ScheduleEventRetry(t.Context(), "default", "missing", 1, "failed"); err == nil {
		t.Fatal("missing retry event succeeded")
	}
	for _, delay := range []int{-1, 90000} {
		event, err = repository.ScheduleEventRetry(t.Context(), "default", event.ID, delay, "failed")
		if err != nil || event.Status != "failed" || event.AttemptCount == 0 {
			t.Fatalf("retry(delay=%d)=%+v err=%v", delay, event, err)
		}
	}
	for _, values := range [][2]string{{"", "nonce"}, {"connector", ""}} {
		if _, err := repository.RecordWebhookNonce(t.Context(), "default", values[0], values[1], "now", "later"); err == nil {
			t.Fatalf("invalid nonce accepted: %q", values)
		}
	}
}

func TestIntegrationDeliveryStoreLifecycleEdges(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	invocation, err := repository.InsertInvocation(t.Context(), "default", integrationmodel.IntegrationInvocation{ID: "invocation-explicit", ConnectorKey: "webhook", Operation: "send", Status: "prepared", RecordID: "record", WorkflowExecutionID: "execution"})
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 501, 1} {
		values, err := repository.ListInvocations(t.Context(), "default", " webhook ", " record ", " execution ", " prepared ", limit)
		if err != nil || len(values) != 1 {
			t.Fatalf("invocations(limit=%d)=%+v err=%v", limit, values, err)
		}
	}
	if _, err := repository.UpdateInvocationStatus(t.Context(), "default", " ", "done", 1, "", ""); err == nil {
		t.Fatal("empty invocation id accepted")
	}
	if _, err := repository.UpdateInvocationStatus(t.Context(), "default", "missing", "done", 1, "", ""); err == nil {
		t.Fatal("missing invocation update succeeded")
	}
	updated, err := repository.UpdateInvocationStatus(t.Context(), "default", invocation.ID, "prepared", 2, "", "")
	if err != nil || updated.DurationMS != 2 {
		t.Fatalf("updated invocation=%+v err=%v", updated, err)
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	if _, err := repository.CompleteInvocation(t.Context(), "default", invocation.ID, "succeeded", 1, "response", "", cyclic); err == nil {
		t.Fatal("cyclic invocation outcome encoded")
	}
	invocation, err = repository.CompleteInvocation(t.Context(), "default", invocation.ID, "succeeded", 1, "response", "", map[string]any{"ok": true})
	if err != nil || invocation.Status != "succeeded" {
		t.Fatalf("completed invocation=%+v err=%v", invocation, err)
	}
	if _, err := repository.CompleteInvocation(t.Context(), "default", invocation.ID, "succeeded", 1, "response", "", nil); err == nil {
		t.Fatal("non-prepared invocation completed twice")
	}

	message, err := repository.InsertOutbox(t.Context(), "default", integrationmodel.IntegrationOutboxMessage{ConnectorKey: "webhook", ConnectionKey: "primary", Operation: "send", RequestRef: "request", Payload: map[string]any{"id": "one"}})
	if err != nil {
		t.Fatal(err)
	}
	if found, ok, err := repository.GetOutbox(t.Context(), "default", message.ID); err != nil || !ok || found.ID != message.ID {
		t.Fatalf("outbox=%+v ok=%v err=%v", found, ok, err)
	}
	if _, ok, err := repository.GetOutbox(t.Context(), "default", "missing"); err != nil || ok {
		t.Fatalf("missing outbox ok=%v err=%v", ok, err)
	}
	if _, _, err := repository.GetOutbox(t.Context(), "", "missing"); err == nil {
		t.Fatal("missing outbox workspace accepted")
	}
	for _, limit := range []int{0, 501, 1} {
		values, err := repository.ListOutbox(t.Context(), "default", " webhook ", " queued ", limit)
		if err != nil || len(values) != 1 {
			t.Fatalf("outbox list(limit=%d)=%+v err=%v", limit, values, err)
		}
	}
	if _, err := repository.UpdateOutboxStatus(t.Context(), "default", " ", "sent", "", ""); err == nil {
		t.Fatal("empty outbox id accepted")
	}
	if _, err := repository.UpdateOutboxStatus(t.Context(), "default", "missing", "sent", "", ""); err == nil {
		t.Fatal("missing outbox update succeeded")
	}
	message, err = repository.UpdateOutboxStatus(t.Context(), "default", message.ID, "sent", "response", "")
	if err != nil || message.Status != "sent" {
		t.Fatalf("updated outbox=%+v err=%v", message, err)
	}
	if _, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "default", "", "response", "delivered", ""); err != nil || found {
		t.Fatalf("blank response lookup found=%v err=%v", found, err)
	}
	if _, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "default", "primary", "", "delivered", ""); err != nil || found {
		t.Fatalf("blank response ref found=%v err=%v", found, err)
	}
	if _, err := repository.InsertOutbox(t.Context(), "default", integrationmodel.IntegrationOutboxMessage{ConnectorKey: "webhook", Operation: "send", DedupKey: "fingerprint", RequestFingerprint: "explicit-fingerprint"}); err != nil {
		t.Fatalf("explicit outbox fingerprint: %v", err)
	}
	if _, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "default", "primary", "missing", "delivered", ""); err != nil || found {
		t.Fatalf("missing response lookup found=%v err=%v", found, err)
	}
	if _, err := repository.ScheduleOutboxRetry(t.Context(), "default", " ", 1, "failed"); err == nil {
		t.Fatal("empty outbox retry id accepted")
	}
	if _, err := repository.ScheduleOutboxRetry(t.Context(), "default", "missing", 1, "failed"); err == nil {
		t.Fatal("missing outbox retry succeeded")
	}
	message, err = repository.ScheduleOutboxRetry(t.Context(), "default", message.ID, -1, "failed")
	if err != nil || message.Status != "queued" || message.AttemptCount == 0 {
		t.Fatalf("retried outbox=%+v err=%v", message, err)
	}
	message, err = repository.ScheduleOutboxRetry(t.Context(), "default", message.ID, 0, "")
	if err != nil || message.Error != "failed" {
		t.Fatalf("operator retry must preserve provider rejection evidence: message=%+v err=%v", message, err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "reconciliation")
	for _, limit := range []int{0, 501} {
		if values, err := repository.ListPreparedInvocationsForReconciliation(t.Context(), scope, limit, "2026-07-20T00:00:00Z"); err != nil || len(values) != 0 {
			t.Fatalf("reconciliation(limit=%d)=%+v err=%v", limit, values, err)
		}
	}
	if _, err := repository.ListPreparedInvocationsForReconciliation(t.Context(), scope, 1, " "); err == nil {
		t.Fatal("blank reconciliation cutoff accepted")
	}
	for _, values := range [][2]string{{"", "now"}, {"invocation", ""}} {
		if _, _, err := repository.MarkInvocationReconciliationRequired(t.Context(), "default", values[0], values[1]); err == nil {
			t.Fatalf("invalid reconciliation identity accepted: %q", values)
		}
	}
	invalidOutbox := map[string]any{"unsupported": func() {}}
	if _, err := repository.InsertOutbox(t.Context(), "default", integrationmodel.IntegrationOutboxMessage{ConnectorKey: "webhook", Operation: "send", DedupKey: "invalid", Payload: invalidOutbox}); err == nil {
		t.Fatal("unsupported outbox payload accepted")
	}
}

func TestIntegrationWorkerLeaseTimeEdges(t *testing.T) {
	for _, now := range []string{"2026-07-20T00:00:00Z", "invalid"} {
		if integrationWorkerLeaseCutoff(now) == "" || integrationWorkerLeaseExpiry(now) == "" {
			t.Fatalf("empty lease time for %q", now)
		}
	}
}

func TestIntegrationWorkersDiscoverRegisteredWorkspaceScopes(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	events := NewIntegrationEventStore(store)
	delivery := NewIntegrationDeliveryStore(store)
	worker := NewIntegrationWorkerStore(store)
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		if _, _, err := events.UpsertEvent(t.Context(), workspaceID, integrationmodel.IntegrationEvent{Provider: "crm", EventType: "customer.updated", ExternalID: "event-" + workspaceID}); err != nil {
			t.Fatalf("insert event for %s: %v", workspaceID, err)
		}
		if _, err := delivery.InsertOutbox(t.Context(), workspaceID, integrationmodel.IntegrationOutboxMessage{ConnectorKey: "__automation__", Operation: "execute", DedupKey: "outbox-" + workspaceID}); err != nil {
			t.Fatalf("insert outbox for %s: %v", workspaceID, err)
		}
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "worker scope discovery")
	dueEvents, err := worker.ListDueEvents(t.Context(), scope, 10, time.Now().UTC().Add(time.Minute).Format(time.RFC3339))
	if err != nil || len(dueEvents) != 2 {
		t.Fatalf("due events=%+v err=%v", dueEvents, err)
	}
	dueOutbox, err := worker.ListDueOutbox(t.Context(), scope, 10, time.Now().UTC().Add(time.Minute).Format(time.RFC3339))
	if err != nil || len(dueOutbox) != 2 {
		t.Fatalf("due outbox=%+v err=%v", dueOutbox, err)
	}
	for _, values := range [][]string{{dueEvents[0].WorkspaceID, dueEvents[1].WorkspaceID}, {dueOutbox[0].WorkspaceID, dueOutbox[1].WorkspaceID}} {
		if values[0] == values[1] || (values[0] != "workspace-a" && values[0] != "workspace-b") || (values[1] != "workspace-a" && values[1] != "workspace-b") {
			t.Fatalf("worker scope isolation lost: %v", values)
		}
	}
}

func TestIntegrationWorkerRetryAndInputEdges(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	events := NewIntegrationEventStore(store)
	delivery := NewIntegrationDeliveryStore(store)
	worker := NewIntegrationWorkerStore(store)
	now := "2026-07-20T00:00:00Z"

	for _, delay := range []int{-1, 90000} {
		event, _, err := events.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{Provider: "crm", ExternalID: "retry-event-" + strconv.Itoa(delay)})
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := worker.ClaimEvent(t.Context(), "default", event.ID, "worker", now)
		if err != nil || !ok {
			t.Fatalf("claim event=%+v ok=%v err=%v", claimed, ok, err)
		}
		retried, err := worker.ScheduleEventRetry(t.Context(), "default", event.ID, claimed.LeaseOwner, claimed.FencingToken, delay, "failed", now)
		if err != nil || retried.Status != "failed" || retried.AttemptCount != 1 {
			t.Fatalf("retry event(delay=%d)=%+v err=%v", delay, retried, err)
		}
	}
	if _, _, err := worker.ClaimEvent(t.Context(), "default", "", "worker", now); err == nil {
		t.Fatal("empty event claim id accepted")
	}
	if _, _, err := worker.ClaimEvent(t.Context(), "default", "event", "", now); err == nil {
		t.Fatal("empty event claim owner accepted")
	}
	if _, err := worker.UpdateEventStatus(t.Context(), "default", "", "worker", 1, "done", "", now); err == nil {
		t.Fatal("empty event update id accepted")
	}
	if _, err := worker.UpdateEventStatus(t.Context(), "default", "event", "worker", 1, "done", "", ""); err == nil {
		t.Fatal("empty event update time accepted")
	}
	if _, err := worker.ScheduleEventRetry(t.Context(), "default", "event", "worker", 1, 1, "failed", "invalid"); err == nil {
		t.Fatal("invalid event retry time accepted")
	}
	if _, err := worker.ScheduleEventRetry(t.Context(), "default", "", "worker", 1, 1, "failed", now); err == nil {
		t.Fatal("empty event retry id accepted")
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "worker edge limits")
	if _, err := worker.ListDueEvents(t.Context(), scope, 201, now); err != nil {
		t.Fatalf("due events high limit: %v", err)
	}
	if _, err := worker.ListDueOutbox(t.Context(), scope, 0, now); err != nil {
		t.Fatalf("due outbox default limit: %v", err)
	}

	for _, delay := range []int{-1, 1} {
		message, err := delivery.InsertOutbox(t.Context(), "default", integrationmodel.IntegrationOutboxMessage{ConnectorKey: "webhook", Operation: "send", DedupKey: "retry-outbox-" + strconv.Itoa(delay)})
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := worker.ClaimOutbox(t.Context(), "default", message.ID, "worker", now)
		if err != nil || !ok {
			t.Fatalf("claim outbox=%+v ok=%v err=%v", claimed, ok, err)
		}
		retried, err := worker.ScheduleOutboxRetry(t.Context(), "default", message.ID, claimed.LeaseOwner, claimed.FencingToken, delay, "failed", now)
		if err != nil || retried.Status != "queued" || retried.AttemptCount != 1 {
			t.Fatalf("retry outbox(delay=%d)=%+v err=%v", delay, retried, err)
		}
	}
	if _, _, err := worker.ClaimOutbox(t.Context(), "default", "", "worker", now); err == nil {
		t.Fatal("empty outbox claim id accepted")
	}
	if _, _, err := worker.ClaimOutbox(t.Context(), "default", "outbox", "", now); err == nil {
		t.Fatal("empty outbox claim owner accepted")
	}
	if _, err := worker.UpdateOutboxStatus(t.Context(), "default", "", "worker", 1, "sent", "", "", "", now); err == nil {
		t.Fatal("empty outbox update id accepted")
	}
	if _, err := worker.UpdateOutboxStatus(t.Context(), "default", "outbox", "worker", 1, "sent", "", "", "", ""); err == nil {
		t.Fatal("empty outbox update time accepted")
	}
	if _, err := worker.ScheduleOutboxRetry(t.Context(), "default", "outbox", "worker", 1, 1, "failed", "invalid"); err == nil {
		t.Fatal("invalid outbox retry time accepted")
	}
	if _, err := worker.ScheduleOutboxRetry(t.Context(), "default", "", "worker", 1, 1, "failed", now); err == nil {
		t.Fatal("empty outbox retry id accepted")
	}
}
