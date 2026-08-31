package integration

import (
	"context"
	"errors"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"sync"
	"testing"

	"github.com/domainry/domainry-foundation/mutation"
)

type contextIntegrationDeliveryContract interface {
	ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error)
	InsertInvocation(context.Context, string, integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error)
	UpdateInvocationStatus(context.Context, string, string, string, int64, string, string) (integrationmodel.IntegrationInvocation, error)
	ListOutbox(context.Context, string, string, string, int) ([]integrationmodel.IntegrationOutboxMessage, error)
	InsertOutbox(context.Context, string, integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error)
	UpdateOutboxStatus(context.Context, string, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, error)
	UpdateOutboxStatusByResponseRef(context.Context, string, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error)
	ScheduleOutboxRetry(context.Context, string, string, int, string) (integrationmodel.IntegrationOutboxMessage, error)
}

type invocationReconciliationContract interface {
	ListPreparedInvocationsForReconciliation(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationInvocation, error)
	MarkInvocationReconciliationRequired(context.Context, string, string, string) (integrationmodel.IntegrationInvocation, bool, error)
}

type acknowledgementReconciliationContract interface {
	ListOverdueOutboxAcknowledgements(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationOutboxMessage, error)
	MarkOutboxAcknowledgementReconciliationRequired(context.Context, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error)
}

func TestIntegrationOutboxEnqueueDeduplicatesAndRejectsFingerprintConflict(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-primary", ConnectorKey: "webhook", ConnectionKey: "primary", Operation: "notify", RequestRef: "order:one", DedupKey: "order:one", Payload: map[string]any{"order_id": "one"}}
	start := make(chan struct{})
	ids, errorsFound := make(chan string, 100), make(chan error, 100)
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			saved, err := repository.InsertOutbox(t.Context(), message.WorkspaceID, message)
			if err != nil {
				errorsFound <- err
				return
			}
			ids <- saved.ID
		}()
	}
	close(start)
	group.Wait()
	close(ids)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent enqueue: %v", err)
	}
	identity := ""
	for id := range ids {
		if identity == "" {
			identity = id
		} else if id != identity {
			t.Fatalf("dedup returned different ids %q and %q", identity, id)
		}
	}
	message.Payload = map[string]any{"order_id": "different"}
	if _, err := repository.InsertOutbox(t.Context(), message.WorkspaceID, message); !mutation.IsMutationConflict(err, mutation.MutationConflictIdempotency) {
		t.Fatalf("fingerprint conflict=%v", err)
	}
}

func TestIntegrationDeliveryStoreAppliesMonotonicDeliveryReceipts(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	message, err := repository.InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-primary", ConnectorKey: "whatsapp", ConnectionKey: "whatsapp-primary", Operation: "send_message", Status: "sent", ResponseRef: "whatsapp:wamid.1", CreatedBy: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	delivered, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "whatsapp-primary", "whatsapp:wamid.1", "delivered", "")
	if err != nil || !found || delivered.ID != message.ID || delivered.Status != "delivered" {
		t.Fatalf("delivered=%#v found=%v err=%v", delivered, found, err)
	}
	read, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "whatsapp-primary", "whatsapp:wamid.1", "read", "")
	if err != nil || !found || read.Status != "read" {
		t.Fatalf("read=%#v found=%v err=%v", read, found, err)
	}
	regressed, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "whatsapp-primary", "whatsapp:wamid.1", "delivered", "")
	if err != nil || !found || regressed.Status != "read" {
		t.Fatalf("regressed=%#v found=%v err=%v", regressed, found, err)
	}
	duplicate, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "whatsapp-primary", "whatsapp:wamid.1", "read", "")
	if err != nil || !found || duplicate.Status != "read" {
		t.Fatalf("duplicate=%#v found=%v err=%v", duplicate, found, err)
	}
	lateFailure, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "whatsapp-primary", "whatsapp:wamid.1", "failed", "late")
	if err != nil || !found || lateFailure.Status != "read" {
		t.Fatalf("late failure=%#v found=%v err=%v", lateFailure, found, err)
	}
	if _, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "whatsapp-primary", "whatsapp:missing", "read", ""); err != nil || found {
		t.Fatalf("missing receipt found=%v err=%v", found, err)
	}
}

func TestIntegrationDeliveryStoreConvergesConcurrentDuplicateAndOutOfOrderReceipts(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	message, err := repository.InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-primary", ConnectorKey: "payment", ConnectionKey: "payment-primary", Operation: "capture", Status: "sent", ResponseRef: "provider:payment-1"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	statuses := []string{"failed", "sent", "delivered", "read", "delivered"}
	for callback := 0; callback < 100; callback++ {
		status := statuses[callback%len(statuses)]
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, found, updateErr := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "payment-primary", "provider:payment-1", status, status)
			if updateErr != nil {
				errorsFound <- updateErr
				return
			}
			if !found {
				errorsFound <- errors.New("receipt target disappeared")
			}
		}()
	}
	close(start)
	group.Wait()
	close(errorsFound)
	for updateErr := range errorsFound {
		t.Fatalf("concurrent callback: %v", updateErr)
	}
	final, found, err := repository.GetOutbox(t.Context(), "workspace-primary", message.ID)
	if err != nil || !found || final.Status != "read" {
		t.Fatalf("final=%#v found=%v err=%v", final, found, err)
	}
}

func TestIntegrationDeliveryStoreQuarantinesOverdueAcknowledgementOnceAndAcceptsLateAuthority(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	message, err := repository.InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{
		WorkspaceID: "workspace-primary", ConnectorKey: "callback-connector", ConnectionKey: "primary", Operation: "submit",
		Status: "sent", ResponseRef: "external:42", AckDeadlineAt: "2026-07-20T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test acknowledgement reconciliation")
	due, err := repository.ListOverdueOutboxAcknowledgements(t.Context(), scope, 10, "2026-07-20T00:00:01Z")
	if err != nil || len(due) != 1 || due[0].ID != message.ID {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	marked, changed, err := repository.MarkOutboxAcknowledgementReconciliationRequired(t.Context(), "workspace-primary", message.ID, "2026-07-20T00:00:01Z")
	if err != nil || !changed || marked.Status != "quarantined" || marked.Error != "backend.integration.outbox.ack_timeout" || marked.AckDeadlineAt != "" {
		t.Fatalf("marked=%#v changed=%v err=%v", marked, changed, err)
	}
	if _, changed, err := repository.MarkOutboxAcknowledgementReconciliationRequired(t.Context(), "workspace-primary", message.ID, "2026-07-20T00:00:02Z"); err != nil || changed {
		t.Fatalf("duplicate mark changed=%v err=%v", changed, err)
	}
	late, found, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "workspace-primary", "primary", "external:42", "delivered", "")
	if err != nil || !found || late.Status != "delivered" || late.AckDeadlineAt != "" {
		t.Fatalf("late authoritative receipt=%#v found=%v err=%v", late, found, err)
	}
}

var _ contextIntegrationDeliveryContract = IntegrationDeliveryStore{}
var _ invocationReconciliationContract = IntegrationDeliveryStore{}
var _ acknowledgementReconciliationContract = IntegrationDeliveryStore{}

func TestIntegrationDeliveryStoreIdentifiesPreparedFactWithMissingExternalReceipt(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	missing, err := repository.InsertInvocation(t.Context(), "workspace-primary", integrationmodel.IntegrationInvocation{
		WorkspaceID: "workspace-primary", ConnectorKey: "payments", ConnectionKey: "primary", Operation: "capture",
		Status: "prepared", RequestRef: "order:42", RecordID: "42",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repository.InsertInvocation(t.Context(), "workspace-primary", integrationmodel.IntegrationInvocation{
		WorkspaceID: "workspace-primary", ConnectorKey: "payments", ConnectionKey: "primary", Operation: "capture",
		Status: "prepared", RequestRef: "order:43", RecordID: "43",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CompleteInvocation(t.Context(), completed.WorkspaceID, completed.ID, "succeeded", 12, "provider:43", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE _integration_invocations SET created_at = ? WHERE id = ?", "2020-01-01T00:00:00Z", missing.ID); err != nil {
		t.Fatal(err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test invocation reconciliation")
	due, err := repository.ListPreparedInvocationsForReconciliation(t.Context(), scope, 10, "2020-01-02T00:00:00Z")
	if err != nil || len(due) != 1 || due[0].ID != missing.ID || due[0].RequestRef != "order:42" {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	marked, changed, err := repository.MarkInvocationReconciliationRequired(t.Context(), missing.WorkspaceID, missing.ID, "2020-01-02T00:00:01Z")
	if err != nil || !changed || marked.Status != "reconciliation_required" || marked.ResponseRef != "" || marked.Error != "backend.integration.invocation.external_receipt_missing" {
		t.Fatalf("marked=%#v changed=%v err=%v", marked, changed, err)
	}
	if _, changed, err := repository.MarkInvocationReconciliationRequired(t.Context(), missing.WorkspaceID, missing.ID, "2020-01-02T00:00:02Z"); err != nil || changed {
		t.Fatalf("second mark changed=%v err=%v", changed, err)
	}
	due, err = repository.ListPreparedInvocationsForReconciliation(t.Context(), scope, 10, "2020-01-03T00:00:00Z")
	if err != nil || len(due) != 0 {
		t.Fatalf("due after claim=%#v err=%v", due, err)
	}
}

func TestIntegrationDeliveryStoreContractAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	message, err := repository.InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-primary", ConnectorKey: "webhook", Operation: "send", CreatedBy: "admin"})
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
	values, err := repository.ListOutbox(t.Context(), "workspace-primary", "webhook", "queued", 10)
	if err != nil || len(values) != 1 || values[0].ID != message.ID {
		t.Fatalf("list outbox=%#v err=%v", values, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListOutbox(cancelled, "workspace-primary", "", "", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if _, err := repository.InsertInvocation(cancelled, "workspace-primary", integrationmodel.IntegrationInvocation{WorkspaceID: "workspace-primary", ConnectorKey: "never"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled insert error=%v", err)
	}
}

func TestIntegrationDeliveryStoreWorkspaceIsolationContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationDeliveryStore(store)
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		if _, err := repository.InsertInvocation(t.Context(), workspaceID, integrationmodel.IntegrationInvocation{WorkspaceID: workspaceID, ConnectorKey: "shared", Operation: workspaceID}); err != nil {
			t.Fatalf("insert %s invocation: %v", workspaceID, err)
		}
		if _, err := repository.InsertOutbox(t.Context(), workspaceID, integrationmodel.IntegrationOutboxMessage{WorkspaceID: workspaceID, ConnectorKey: "shared", Operation: workspaceID, DedupKey: "shared"}); err != nil {
			t.Fatalf("insert %s outbox: %v", workspaceID, err)
		}
	}
	invocations, err := repository.ListInvocations(t.Context(), "workspace-a", "", "", "", "", 10)
	if err != nil || len(invocations) != 1 || invocations[0].Operation != "workspace-a" {
		t.Fatalf("workspace-a invocations=%#v err=%v", invocations, err)
	}
	outbox, err := repository.ListOutbox(t.Context(), "workspace-b", "", "", 10)
	if err != nil || len(outbox) != 1 || outbox[0].Operation != "workspace-b" {
		t.Fatalf("workspace-b outbox=%#v err=%v", outbox, err)
	}

	missingChecks := []func() error{
		func() error { _, err := repository.ListInvocations(t.Context(), "", "", "", "", "", 1); return err },
		func() error {
			_, err := repository.InsertInvocation(t.Context(), "", integrationmodel.IntegrationInvocation{})
			return err
		},
		func() error {
			_, err := repository.UpdateInvocationStatus(t.Context(), "", "id", "done", 0, "", "")
			return err
		},
		func() error {
			_, err := repository.CompleteInvocation(t.Context(), "", "id", "done", 0, "", "", nil)
			return err
		},
		func() error {
			_, _, err := repository.MarkInvocationReconciliationRequired(t.Context(), "", "id", "now")
			return err
		},
		func() error { _, err := repository.ListOutbox(t.Context(), "", "", "", 1); return err },
		func() error {
			_, err := repository.InsertOutbox(t.Context(), "", integrationmodel.IntegrationOutboxMessage{})
			return err
		},
		func() error {
			_, err := repository.UpdateOutboxStatus(t.Context(), "", "id", "sent", "", "")
			return err
		},
		func() error {
			_, _, err := repository.UpdateOutboxStatusByResponseRef(t.Context(), "", "connection", "response", "sent", "")
			return err
		},
		func() error { _, err := repository.ScheduleOutboxRetry(t.Context(), "", "id", 1, "failed"); return err },
		func() error {
			_, err := repository.ListPreparedInvocationsForReconciliation(t.Context(), principalmodel.SystemScope{}, 1, "now")
			return err
		},
	}
	for index, check := range missingChecks {
		if err := check(); err == nil {
			t.Fatalf("missing scope check %d unexpectedly succeeded", index)
		}
	}
	if _, err := repository.InsertInvocation(t.Context(), "workspace-a", integrationmodel.IntegrationInvocation{WorkspaceID: "workspace-b"}); err == nil {
		t.Fatal("invocation workspace mismatch unexpectedly succeeded")
	}
	if _, err := repository.InsertOutbox(t.Context(), "workspace-a", integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-b"}); err == nil {
		t.Fatal("outbox workspace mismatch unexpectedly succeeded")
	}
}
