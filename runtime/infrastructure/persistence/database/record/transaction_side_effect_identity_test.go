package record

import (
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"path/filepath"
	"strings"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestTransactionalSideEffectsRequireStableIdentity(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "transaction-side-effects.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)

	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.insertAuditEventTx(t.Context(), tx, auditmodel.AuditEvent{Event: "record.updated"}); err == nil || !strings.Contains(err.Error(), "deterministic id") {
		t.Fatalf("missing audit identity error=%v", err)
	}
	_ = tx.Rollback()

	tx, err = store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.insertWorkflowIntentTx(t.Context(), tx, workflowmodel.WorkflowExecution{WorkflowKey: "notify"}); err == nil || !strings.Contains(err.Error(), "deterministic id") {
		t.Fatalf("missing workflow identity error=%v", err)
	}
	_ = tx.Rollback()

	tx, err = store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.insertPublicationHandoffTx(t.Context(), tx, publicationmodel.Message{WorkspaceID: "workspace-a", ConnectorKey: "webhook", Operation: "notify"}); err == nil || !strings.Contains(err.Error(), "stable dedup key") {
		t.Fatalf("missing outbox dedup identity error=%v", err)
	}
	_ = tx.Rollback()

	message := publicationmodel.Message{WorkspaceID: "workspace-a", ConnectorKey: "webhook", ConnectionKey: "primary", Operation: "notify", DedupKey: "record:1", Payload: map[string]any{}}
	wakeups := store.WorkerWakeups().Subscribe("runtime_publication_outbox", 1)
	operationContext, afterCommit := transactioncontract.WithAfterCommitRegistry(transactioncontract.WithActiveTransaction(t.Context()))
	tx, err = store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.insertPublicationHandoffTx(operationContext, tx, message); err != nil {
		t.Fatal(err)
	}
	select {
	case locator := <-wakeups:
		t.Fatalf("outbox woke before commit: %#v", locator)
	default:
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, hook := range afterCommit.Hooks() {
		if err := hook.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	wantID := publicationhandoff.OutboxDedupID(message.WorkspaceID, message.ConnectorKey, message.ConnectionKey, message.Operation, message.DedupKey)
	var stored int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _publication_outbox WHERE id = ?`, wantID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("deterministic outbox id=%q stored=%d err=%v", wantID, stored, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _worker_queue_scopes WHERE queue_kind = ? AND scope_key = ?`, "runtime_publication_outbox", message.WorkspaceID).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("transactional outbox workspace recovery scope stored=%d err=%v", stored, err)
	}
	select {
	case locator := <-wakeups:
		if locator.WorkspaceID != message.WorkspaceID || locator.TaskID != wantID {
			t.Fatalf("outbox wake locator=%#v", locator)
		}
	default:
		t.Fatal("committed transactional outbox did not wake")
	}
}
