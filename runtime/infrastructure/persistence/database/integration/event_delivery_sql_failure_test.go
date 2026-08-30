package integration

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func scriptedIntegrationStores(t *testing.T, state *integrationSQLState) (IntegrationEventStore, IntegrationDeliveryStore, IntegrationWorkerStore) {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	db := openIntegrationScriptedDB(state)
	t.Cleanup(func() {
		_ = db.Close()
		_ = store.Close()
	})
	events := NewIntegrationEventStore(store)
	delivery := NewIntegrationDeliveryStore(store)
	worker := NewIntegrationWorkerStore(store)
	events.db, delivery.db, worker.db = db, db, db
	return events, delivery, worker
}

func TestIntegrationEventDeliveryWorkerListSQLFailures(t *testing.T) {
	wantErr := errors.New("list SQL failure")
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "integration worker")
	calls := []func(IntegrationEventStore, IntegrationDeliveryStore, IntegrationWorkerStore) error{
		func(e IntegrationEventStore, _ IntegrationDeliveryStore, _ IntegrationWorkerStore) error {
			_, err := e.ListEvents(t.Context(), "default", "", "", 1)
			return err
		},
		func(_ IntegrationEventStore, d IntegrationDeliveryStore, _ IntegrationWorkerStore) error {
			_, err := d.ListInvocations(t.Context(), "default", "", "", "", "", 1)
			return err
		},
		func(_ IntegrationEventStore, d IntegrationDeliveryStore, _ IntegrationWorkerStore) error {
			_, err := d.ListPreparedInvocationsForReconciliation(t.Context(), scope, 1, "2026-07-20T00:00:00Z")
			return err
		},
		func(_ IntegrationEventStore, d IntegrationDeliveryStore, _ IntegrationWorkerStore) error {
			_, err := d.ListOutbox(t.Context(), "default", "", "", 1)
			return err
		},
		func(_ IntegrationEventStore, _ IntegrationDeliveryStore, w IntegrationWorkerStore) error {
			_, err := w.ListDueEvents(t.Context(), scope, 1, "2026-07-20T00:00:00Z")
			return err
		},
		func(_ IntegrationEventStore, _ IntegrationDeliveryStore, w IntegrationWorkerStore) error {
			_, err := w.ListDueOutbox(t.Context(), scope, 1, "2026-07-20T00:00:00Z")
			return err
		},
	}
	for index, call := range calls {
		for _, step := range []integrationSQLQueryStep{{err: wantErr}, {columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}, {columns: []string{"bad"}, nextErr: wantErr}} {
			querySteps := []integrationSQLQueryStep{step}
			if index >= 4 && step.err == nil {
				querySteps = []integrationSQLQueryStep{{columns: []string{"scope_key"}, rows: [][]driver.Value{{"default"}}}, step}
			}
			events, delivery, worker := scriptedIntegrationStores(t, &integrationSQLState{querySteps: querySteps})
			if err := call(events, delivery, worker); err == nil {
				t.Fatalf("list call %d ignored %+v", index, step)
			}
		}
	}
}

func TestRuntimeGlobalWorkerDiscoveryKeepsQueueReadsWorkspaceScoped(t *testing.T) {
	state := &integrationSQLState{querySteps: []integrationSQLQueryStep{
		{columns: []string{"scope_key"}, rows: [][]driver.Value{{"workspace-a"}}},
		{columns: []string{"id"}},
	}}
	_, _, worker := scriptedIntegrationStores(t, state)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "workspace isolation contract")
	if _, err := worker.ListDueEvents(t.Context(), scope, 1, "2026-07-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if len(state.queryWorkspaces) != 2 || state.queryWorkspaces[0] != "" || state.queryWorkspaces[1] != "workspace-a" {
		t.Fatalf("expected global discovery followed by tenant-scoped queue read, got %#v", state.queryWorkspaces)
	}
	if !strings.Contains(state.queryStatements[0], "_worker_queue_scopes") || !strings.Contains(state.queryStatements[1], "_integration_events") {
		t.Fatalf("unexpected discovery/read statements: %#v", state.queryStatements)
	}
}

func TestIntegrationEventMutationSQLFailures(t *testing.T) {
	wantErr := errors.New("event SQL failure")
	updateCalls := []func(IntegrationEventStore) error{
		func(r IntegrationEventStore) error {
			_, err := r.UpdateEventStatus(t.Context(), "default", "event", "done", "")
			return err
		},
		func(r IntegrationEventStore) error {
			_, err := r.ScheduleEventRetry(t.Context(), "default", "event", 1, "failed")
			return err
		},
	}
	for index, call := range updateCalls {
		for _, exec := range []integrationSQLExecStep{{err: wantErr}, {rowsErr: wantErr}} {
			events, _, _ := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{exec}})
			if err := call(events); err == nil {
				t.Fatalf("event mutation %d ignored %+v", index, exec)
			}
		}
		events, _, _ := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{err: wantErr}}})
		if err := call(events); err == nil {
			t.Fatalf("event mutation %d reload failure ignored", index)
		}
	}

	events, _, _ := scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{err: wantErr}}})
	if _, _, err := events.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{Provider: "crm", ExternalID: "event"}); err == nil {
		t.Fatal("event lookup failure ignored")
	}
	events, _, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}, execSteps: []integrationSQLExecStep{{err: wantErr}}})
	if _, _, err := events.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{Provider: "crm", ExternalID: "event"}); err == nil {
		t.Fatal("event insert failure ignored")
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	events, _, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}})
	if _, _, err := events.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{Provider: "crm", ExternalID: "event", Payload: cyclic}); err == nil {
		t.Fatal("cyclic event payload encoded")
	}

	for index, test := range []struct {
		state     *integrationSQLState
		duplicate bool
		wantError bool
	}{
		{state: &integrationSQLState{execSteps: []integrationSQLExecStep{{err: wantErr}}}, wantError: true},
		{state: &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{err: wantErr}}}, wantError: true},
		{state: &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}, {err: errors.New("unique constraint")}}, querySteps: []integrationSQLQueryStep{{}}}, duplicate: true},
		{state: &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}, {err: errors.New("duplicate key")}}, querySteps: []integrationSQLQueryStep{{}}}, duplicate: true},
		{state: &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}, {err: wantErr}}, querySteps: []integrationSQLQueryStep{{}}}, wantError: true},
	} {
		events, _, _ = scriptedIntegrationStores(t, test.state)
		duplicate, err := events.RecordWebhookNonce(t.Context(), "default", "connector", "nonce", "now", "later")
		if (err != nil) != test.wantError || duplicate != test.duplicate {
			t.Fatalf("nonce case %d duplicate=%v err=%v", index, duplicate, err)
		}
	}

}

func TestIntegrationEventAcceptanceFailureStages(t *testing.T) {
	wantErr := errors.New("acceptance failure")
	event := integrationmodel.IntegrationEvent{Provider: "crm", EventType: "customer.updated", ExternalID: "external"}
	intent := integrationmodel.IntegrationEventMappingIntent{MappingKey: "mapping"}
	eventColumns := []string{"id", "workspace_id", "provider", "event_type", "external_id", "status", "payload_json", "error", "attempt_count", "next_retry_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "received_at", "updated_at"}
	eventValues := []driver.Value{"event", "default", "crm", "customer.updated", "external", "received", "{}", "", int64(0), "", "", "", "", int64(0), "now", "now"}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	events, _, _ := scriptedIntegrationStores(t, &integrationSQLState{})
	if _, _, err := events.AcceptEvent(cancelled, "default", event, intent); !errors.Is(err, context.Canceled) {
		t.Fatalf("acceptance cancellation=%v", err)
	}

	for index, state := range []*integrationSQLState{
		{beginErr: wantErr},
		{querySteps: []integrationSQLQueryStep{{err: wantErr}}},
		{querySteps: []integrationSQLQueryStep{{}}, execSteps: []integrationSQLExecStep{{err: wantErr}}},
		{querySteps: []integrationSQLQueryStep{{}}, execSteps: []integrationSQLExecStep{{rows: 1}, {err: wantErr}}},
		{querySteps: []integrationSQLQueryStep{{}}, execSteps: []integrationSQLExecStep{{rows: 1}, {rows: 1}}, commitErr: wantErr},
	} {
		events, _, _ = scriptedIntegrationStores(t, state)
		if _, _, err := events.AcceptEvent(t.Context(), "default", event, intent); err == nil {
			t.Fatalf("acceptance failure stage %d succeeded", index)
		}
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	events, _, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}})
	event.Payload = cyclic
	if _, _, err := events.AcceptEvent(t.Context(), "default", event, intent); err == nil {
		t.Fatal("cyclic event acceptance payload encoded")
	}
	event.Payload = nil
	intent.Payload = cyclic
	events, _, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}, execSteps: []integrationSQLExecStep{{rows: 1}}})
	if _, _, err := events.AcceptEvent(t.Context(), "default", event, intent); err == nil {
		t.Fatal("cyclic mapping intent encoded")
	}
	intent.Payload = nil
	event.Status = "received"

	for index, test := range []struct {
		state     *integrationSQLState
		duplicate bool
		wantError bool
	}{
		{state: &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: eventColumns, rows: [][]driver.Value{eventValues}}}, execSteps: []integrationSQLExecStep{{err: errors.New("unique constraint")}}}, duplicate: true},
		{state: &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: eventColumns, rows: [][]driver.Value{eventValues}}}, execSteps: []integrationSQLExecStep{{err: wantErr}}}, wantError: true},
		{state: &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: eventColumns, rows: [][]driver.Value{eventValues}}}, execSteps: []integrationSQLExecStep{{rows: 1}}, commitErr: wantErr}, wantError: true},
		{state: &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: eventColumns, rows: [][]driver.Value{eventValues}}}, execSteps: []integrationSQLExecStep{{rows: 1}}}, duplicate: true},
	} {
		events, _, _ = scriptedIntegrationStores(t, test.state)
		_, duplicate, err := events.AcceptEvent(t.Context(), "default", event, intent)
		if duplicate != test.duplicate || (err != nil) != test.wantError {
			t.Fatalf("duplicate acceptance %d duplicate=%v err=%v", index, duplicate, err)
		}
	}

	event.Payload = map[string]any{"unsupported": func() {}}
	events, _, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{columns: eventColumns, rows: [][]driver.Value{eventValues}}}})
	if _, _, err := events.AcceptEvent(t.Context(), "default", event, intent); err == nil {
		t.Fatal("unsupported duplicate event content fingerprinted")
	}
	event.Payload = nil
	conflictingValues := append([]driver.Value(nil), eventValues...)
	conflictingValues[3] = "customer.created"
	for _, state := range []*integrationSQLState{
		{querySteps: []integrationSQLQueryStep{{columns: eventColumns, rows: [][]driver.Value{conflictingValues}}}, execSteps: []integrationSQLExecStep{{err: wantErr}}},
		{querySteps: []integrationSQLQueryStep{{columns: eventColumns, rows: [][]driver.Value{conflictingValues}}}, execSteps: []integrationSQLExecStep{{rows: 1}}, commitErr: wantErr},
	} {
		events, _, _ = scriptedIntegrationStores(t, state)
		if _, _, err := events.AcceptEvent(t.Context(), "default", event, intent); err == nil {
			t.Fatal("conflicting event acceptance failure ignored")
		}
	}
}

func TestIntegrationEventInsertRaceAndMissingReload(t *testing.T) {
	eventColumns := []string{"id", "workspace_id", "provider", "event_type", "external_id", "status", "payload_json", "error", "attempt_count", "next_retry_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "received_at", "updated_at"}
	eventValues := []driver.Value{"event", "default", "crm", "customer.updated", "external", "received", "{}", "", int64(0), "", "", "", "", int64(0), "now", "now"}
	events, _, _ := scriptedIntegrationStores(t, &integrationSQLState{
		querySteps: []integrationSQLQueryStep{{}, {columns: eventColumns, rows: [][]driver.Value{eventValues}}},
		execSteps:  []integrationSQLExecStep{{err: errors.New("unique constraint")}},
	})
	value, duplicate, err := events.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{Provider: "crm", ExternalID: "external"})
	if err != nil || !duplicate || value.ID != "event" {
		t.Fatalf("insert race event=%+v duplicate=%v err=%v", value, duplicate, err)
	}
	events, _, _ = scriptedIntegrationStores(t, &integrationSQLState{
		querySteps: []integrationSQLQueryStep{{}, {err: errors.New("reload failed")}},
		execSteps:  []integrationSQLExecStep{{err: errors.New("insert failed")}},
	})
	if _, _, err := events.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{Provider: "crm", ExternalID: "external"}); err == nil {
		t.Fatal("event insert/reload failure ignored")
	}
	for index, call := range []func(IntegrationEventStore) error{
		func(r IntegrationEventStore) error {
			_, err := r.UpdateEventStatus(t.Context(), "default", "event", "done", "")
			return err
		},
		func(r IntegrationEventStore) error {
			_, err := r.ScheduleEventRetry(t.Context(), "default", "event", 1, "failed")
			return err
		},
	} {
		events, _, _ = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{}}})
		if err := call(events); err == nil {
			t.Fatalf("event missing reload %d succeeded", index)
		}
	}
}

func TestIntegrationDeliveryMutationSQLFailures(t *testing.T) {
	wantErr := errors.New("delivery SQL failure")
	calls := []func(IntegrationDeliveryStore) error{
		func(r IntegrationDeliveryStore) error {
			_, err := r.UpdateInvocationStatus(t.Context(), "default", "invocation", "done", 1, "", "")
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, err := r.CompleteInvocation(t.Context(), "default", "invocation", "done", 1, "", "", nil)
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, _, err := r.MarkInvocationReconciliationRequired(t.Context(), "default", "invocation", "now")
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, err := r.UpdateOutboxStatus(t.Context(), "default", "outbox", "sent", "", "")
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, err := r.ScheduleOutboxRetry(t.Context(), "default", "outbox", 1, "failed")
			return err
		},
	}
	for index, call := range calls {
		for _, exec := range []integrationSQLExecStep{{err: wantErr}, {rowsErr: wantErr}} {
			_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{exec}})
			if err := call(delivery); err == nil {
				t.Fatalf("delivery mutation %d ignored %+v", index, exec)
			}
		}
		_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{err: wantErr}}})
		if err := call(delivery); err == nil {
			t.Fatalf("delivery mutation %d reload failure ignored", index)
		}
	}

	_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{err: wantErr}}})
	if _, err := delivery.InsertInvocation(t.Context(), "default", integrationmodel.IntegrationInvocation{ConnectorKey: "connector"}); err == nil {
		t.Fatal("invocation insert failure ignored")
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{})
	if _, err := delivery.InsertInvocation(t.Context(), "default", integrationmodel.IntegrationInvocation{ConnectorKey: "connector", Metadata: cyclic}); err == nil {
		t.Fatal("cyclic invocation metadata encoded")
	}
	_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{err: wantErr}}, querySteps: []integrationSQLQueryStep{{err: wantErr}}})
	if _, err := delivery.InsertOutbox(t.Context(), "default", integrationmodel.IntegrationOutboxMessage{ConnectorKey: "connector", Operation: "send", DedupKey: "key"}); err == nil {
		t.Fatal("outbox insert failure ignored")
	}
	_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{err: wantErr}}})
	if _, _, err := delivery.GetOutbox(t.Context(), "default", "outbox"); err == nil {
		t.Fatal("outbox read failure ignored")
	}
	_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{err: wantErr}}})
	if _, _, err := delivery.UpdateOutboxStatusByResponseRef(t.Context(), "default", "connection", "response", "sent", ""); err == nil {
		t.Fatal("response-ref read failure ignored")
	}
}

func TestIntegrationDeliveryMissingReloadAndHelperEdges(t *testing.T) {
	calls := []func(IntegrationDeliveryStore) error{
		func(r IntegrationDeliveryStore) error {
			_, err := r.UpdateInvocationStatus(t.Context(), "default", "invocation", "done", 1, "", "")
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, err := r.CompleteInvocation(t.Context(), "default", "invocation", "done", 1, "", "", nil)
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, _, err := r.MarkInvocationReconciliationRequired(t.Context(), "default", "invocation", "now")
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, err := r.UpdateOutboxStatus(t.Context(), "default", "outbox", "sent", "", "")
			return err
		},
		func(r IntegrationDeliveryStore) error {
			_, err := r.ScheduleOutboxRetry(t.Context(), "default", "outbox", 1, "failed")
			return err
		},
	}
	for index, call := range calls {
		_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{}}})
		if err := call(delivery); err == nil {
			t.Fatalf("missing reload %d succeeded", index)
		}
	}
	_, delivery, _ := scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}}})
	if _, found, err := delivery.findOutboxByDedup(t.Context(), integrationmodel.IntegrationOutboxMessage{WorkspaceID: "default"}); err != nil || found {
		t.Fatalf("missing dedup found=%v err=%v", found, err)
	}
	if _, found, err := delivery.findInvocation(t.Context(), "default", "missing"); err != nil || found {
		t.Fatalf("missing invocation found=%v err=%v", found, err)
	}
	if _, found, err := delivery.findOutbox(t.Context(), "default", "missing"); err != nil || found {
		t.Fatalf("missing outbox found=%v err=%v", found, err)
	}
	_, delivery, _ = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{err: errors.New("insert failed")}}, querySteps: []integrationSQLQueryStep{{}}})
	if _, err := delivery.InsertOutbox(t.Context(), "default", integrationmodel.IntegrationOutboxMessage{ConnectorKey: "connector", Operation: "send", DedupKey: "missing"}); err == nil {
		t.Fatal("outbox insert with missing dedup reload succeeded")
	}
}

func TestIntegrationWorkerMutationSQLFailures(t *testing.T) {
	wantErr := errors.New("worker SQL failure")
	calls := []func(IntegrationWorkerStore) error{
		func(r IntegrationWorkerStore) error {
			_, _, err := r.ClaimEvent(t.Context(), "default", "event", "owner", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.UpdateEventStatus(t.Context(), "default", "event", "owner", 1, "done", "", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.HeartbeatEvent(t.Context(), "default", "event", "owner", 1, "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.ScheduleEventRetry(t.Context(), "default", "event", "owner", 1, 1, "failed", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, _, err := r.ClaimOutbox(t.Context(), "default", "outbox", "owner", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.UpdateOutboxStatus(t.Context(), "default", "outbox", "owner", 1, "sent", "", "", "", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.HeartbeatOutbox(t.Context(), "default", "outbox", "owner", 1, "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.ScheduleOutboxRetry(t.Context(), "default", "outbox", "owner", 1, 1, "failed", "2026-07-20T00:00:00Z")
			return err
		},
	}
	for index, call := range calls {
		for _, exec := range []integrationSQLExecStep{{err: wantErr}, {rowsErr: wantErr}} {
			_, _, worker := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{exec}})
			if err := call(worker); err == nil {
				t.Fatalf("worker mutation %d ignored %+v", index, exec)
			}
		}
		_, _, worker := scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{err: wantErr}}})
		if err := call(worker); err == nil {
			t.Fatalf("worker mutation %d reload failure ignored", index)
		}
	}
}

func TestIntegrationWorkerMissingAndConflictEdges(t *testing.T) {
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "integration worker")
	_, _, worker := scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}, {}}})
	if values, err := worker.ListDueEvents(t.Context(), scope, 0, ""); err != nil || len(values) != 0 {
		t.Fatalf("default due events=%+v err=%v", values, err)
	}
	if values, err := worker.ListDueOutbox(t.Context(), scope, 201, ""); err != nil || len(values) != 0 {
		t.Fatalf("default due outbox=%+v err=%v", values, err)
	}

	for index, call := range []func(IntegrationWorkerStore) (bool, error){
		func(r IntegrationWorkerStore) (bool, error) {
			_, ok, err := r.ClaimEvent(t.Context(), "default", "event", "owner", "")
			return ok, err
		},
		func(r IntegrationWorkerStore) (bool, error) {
			_, ok, err := r.ClaimOutbox(t.Context(), "default", "outbox", "owner", "")
			return ok, err
		},
	} {
		_, _, worker = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 0}}, querySteps: []integrationSQLQueryStep{{}}})
		if ok, err := call(worker); err != nil || ok {
			t.Fatalf("claim miss %d ok=%v err=%v", index, ok, err)
		}
		_, _, worker = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{}}})
		if ok, err := call(worker); err == nil || ok {
			t.Fatalf("claim vanished %d ok=%v err=%v", index, ok, err)
		}
	}

	updateCalls := []func(IntegrationWorkerStore) error{
		func(r IntegrationWorkerStore) error {
			_, err := r.UpdateEventStatus(t.Context(), "default", "event", "owner", 1, "done", "", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.ScheduleEventRetry(t.Context(), "default", "event", "owner", 1, 1, "failed", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.UpdateOutboxStatus(t.Context(), "default", "outbox", "owner", 1, "sent", "", "", "", "2026-07-20T00:00:00Z")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.ScheduleOutboxRetry(t.Context(), "default", "outbox", "owner", 1, 1, "failed", "2026-07-20T00:00:00Z")
			return err
		},
	}
	for index, call := range updateCalls {
		_, _, worker = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 0}}})
		if err := call(worker); err == nil {
			t.Fatalf("worker conflict %d succeeded", index)
		}
		_, _, worker = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 1}}, querySteps: []integrationSQLQueryStep{{}}})
		if err := call(worker); err == nil {
			t.Fatalf("worker vanished reload %d succeeded", index)
		}
	}
	for index, call := range []func(IntegrationWorkerStore) error{
		func(r IntegrationWorkerStore) error {
			_, err := r.HeartbeatEvent(t.Context(), "default", "event", "owner", 1, "")
			return err
		},
		func(r IntegrationWorkerStore) error {
			_, err := r.HeartbeatOutbox(t.Context(), "default", "outbox", "owner", 1, "")
			return err
		},
	} {
		_, _, worker = scriptedIntegrationStores(t, &integrationSQLState{execSteps: []integrationSQLExecStep{{rows: 0}}})
		if err := call(worker); err == nil {
			t.Fatalf("heartbeat conflict %d succeeded", index)
		}
	}
	_, _, worker = scriptedIntegrationStores(t, &integrationSQLState{querySteps: []integrationSQLQueryStep{{}, {}}})
	if _, found, err := worker.findEvent(t.Context(), "default", "missing"); err != nil || found {
		t.Fatalf("worker missing event found=%v err=%v", found, err)
	}
	if _, found, err := worker.findOutbox(t.Context(), "default", "missing"); err != nil || found {
		t.Fatalf("worker missing outbox found=%v err=%v", found, err)
	}
}
