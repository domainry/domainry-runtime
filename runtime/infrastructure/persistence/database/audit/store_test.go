package audit

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type contextAuditContract interface {
	InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error
	ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error)
	ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error)
}

var _ contextAuditContract = AuditStore{}

func TestAuditStoreContractAndCancellation(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAuditStore(store)
	if escaped := escapeSQLLike(`a~b%c_d\e`); escaped != `a~~b~%c~_d\e` {
		t.Fatalf("portable LIKE escaping=%q", escaped)
	}
	expression, args, err := ormbuilder.PreparePredicate(store.SQLRenderer, auditClassMarkerPredicate(auditEventClassExpression(), nil), 0)
	if err != nil || expression != "1 = 0" || len(args) != 0 {
		t.Fatalf("empty class expression=%q args=%v err=%v", expression, args, err)
	}
	event := auditmodel.AuditEvent{ID: "audit-context-1", WorkspaceID: "default", Event: "record.updated", ObjectKey: "customer", RecordID: "customer-1", ActorID: "admin", RoleKey: "admin", Summary: "Updated", Metadata: map[string]any{"request_id": "req-audit-1"}, Before: map[string]any{"status": "new", "amount": "0.10"}, After: map[string]any{"status": "active", "amount": "0.30"}, CreatedAt: "2026-07-12T00:00:00Z"}
	if err := repository.InsertAuditEvent(t.Context(), "default", event); err != nil {
		t.Fatalf("insert audit: %v", err)
	}
	if err := repository.InsertAuditEvent(t.Context(), "default", event); err != nil {
		t.Fatalf("exact idempotent audit replay: %v", err)
	}
	conflict := event
	conflict.Summary = "Different facts"
	if err := repository.InsertAuditEvent(t.Context(), "default", conflict); err == nil {
		t.Fatal("same audit identity with different facts was accepted")
	}
	events, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{RequestID: "req-audit-1", Limit: 10})
	if err != nil || len(events) != 1 || events[0].ID != event.ID || events[0].Before["amount"] != "0.10" || events[0].After["amount"] != "0.30" {
		t.Fatalf("list audit events=%#v err=%v", events, err)
	}
	options, err := repository.ListAuditOptions(t.Context(), "default", auditmodel.AuditOptionQuery{Field: "actor_id", ObjectKey: "customer", Limit: 10})
	if err != nil || len(options) != 1 || options[0].Value != "admin" || options[0].Count != 1 {
		t.Fatalf("list audit options=%#v err=%v", options, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListAuditEvents(cancelled, "default", auditmodel.AuditEventQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if err := repository.InsertAuditEvent(cancelled, "default", auditmodel.AuditEvent{ID: "never", WorkspaceID: "default"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled insert error=%v", err)
	}
}

func TestAuditStoreUsesActiveActionExecutionTransaction(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-action.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	connection, err := store.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), "ROLLBACK")

	ctx, cancel := context.WithTimeout(
		database.WithActionExecutionTransaction(t.Context(), connection),
		time.Second,
	)
	defer cancel()
	event := auditmodel.AuditEvent{
		ID: "audit-action-transaction", WorkspaceID: "default", Event: "record.update_denied",
		ObjectKey: "customer", RecordID: "customer-1", ActorID: "worker", RoleKey: "system",
		CreatedAt: "2026-07-29T00:00:00Z",
	}
	repository := NewAuditStore(store)
	if err := repository.InsertAuditEvent(ctx, "default", event); err != nil {
		t.Fatalf("insert audit through active Action transaction: %v", err)
	}
	events, err := repository.ListAuditEvents(ctx, "default", auditmodel.AuditEventQuery{RecordID: event.RecordID})
	if err != nil || len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("list audit through active Action transaction=%#v err=%v", events, err)
	}
}

func TestAuditStoreWorkspaceIsolationContract(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-workspace.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAuditStore(store)
	for _, event := range []auditmodel.AuditEvent{
		{ID: "audit-a", WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "customer", ActorID: "actor-a", CreatedAt: "2026-07-19T00:00:00Z"},
		{ID: "audit-b", WorkspaceID: "workspace-b", Event: "record.updated", ObjectKey: "customer", ActorID: "actor-b", CreatedAt: "2026-07-19T00:00:01Z"},
	} {
		if err := repository.InsertAuditEvent(t.Context(), event.WorkspaceID, event); err != nil {
			t.Fatalf("insert %s: %v", event.WorkspaceID, err)
		}
	}
	for _, test := range []struct{ workspaceID, eventID, actorID string }{{"workspace-a", "audit-a", "actor-a"}, {"workspace-b", "audit-b", "actor-b"}} {
		events, err := repository.ListAuditEvents(t.Context(), test.workspaceID, auditmodel.AuditEventQuery{ObjectKey: "customer", Limit: 10})
		if err != nil || len(events) != 1 || events[0].ID != test.eventID {
			t.Fatalf("workspace=%s events=%#v err=%v", test.workspaceID, events, err)
		}
		options, err := repository.ListAuditOptions(t.Context(), test.workspaceID, auditmodel.AuditOptionQuery{Field: "actor_id", ObjectKey: "customer", Limit: 10})
		if err != nil || len(options) != 1 || options[0].Value != test.actorID || options[0].Count != 1 {
			t.Fatalf("workspace=%s options=%#v err=%v", test.workspaceID, options, err)
		}
	}
	if _, err := repository.ListAuditEvents(t.Context(), "", auditmodel.AuditEventQuery{}); err == nil {
		t.Fatal("expected empty workspace to be rejected")
	}
	if err := repository.InsertAuditEvent(t.Context(), "workspace-a", auditmodel.AuditEvent{ID: "mismatch", WorkspaceID: "workspace-b"}); err == nil {
		t.Fatal("expected mismatched event workspace to be rejected")
	}
}

func TestAuditStoreFiltersSurfaceClassBeforeLimit(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-class.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAuditStore(store)
	events := []auditmodel.AuditEvent{
		{ID: "governance-older", WorkspaceID: "default", Event: "identity_role_updated", ObjectKey: "role", ActorID: "admin", CreatedAt: "2026-07-19T00:00:00Z"},
		{ID: "business-one", WorkspaceID: "default", Event: "order.updated", ObjectKey: "fulfillment_order", ActorID: "user", CreatedAt: "2026-07-19T00:00:01Z"},
		{ID: "business-two", WorkspaceID: "default", Event: "order.updated", ObjectKey: "fulfillment_order", ActorID: "user", CreatedAt: "2026-07-19T00:00:02Z"},
		{ID: "business-three", WorkspaceID: "default", Event: "order.updated", ObjectKey: "fulfillment_order", ActorID: "user", CreatedAt: "2026-07-19T00:00:03Z"},
		{ID: "operations-newer", WorkspaceID: "default", Event: "identity_recovery_retry", ObjectKey: "role", ActorID: "operator", CreatedAt: "2026-07-19T00:00:04Z"},
	}
	for _, event := range events {
		if err := repository.InsertAuditEvent(t.Context(), event.WorkspaceID, event); err != nil {
			t.Fatalf("insert %s: %v", event.ID, err)
		}
	}

	governance, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{
		Class: auditmodel.AuditEventClassGovernance,
		Limit: 1,
	})
	if err != nil || len(governance) != 1 || governance[0].ID != "governance-older" {
		t.Fatalf("governance=%#v err=%v", governance, err)
	}
	business, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{
		Class: auditmodel.AuditEventClassBusiness,
		Limit: 2,
	})
	if err != nil || len(business) != 2 || business[0].ID != "business-three" || business[1].ID != "business-two" {
		t.Fatalf("business=%#v err=%v", business, err)
	}
	operations, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{
		Class: auditmodel.AuditEventClassOperations,
		Limit: 1,
	})
	if err != nil || len(operations) != 1 || operations[0].ID != "operations-newer" {
		t.Fatalf("operations=%#v err=%v", operations, err)
	}
}

func TestAuditStoreCursorTraversesEqualTimestampsWithoutGapsOrDuplicates(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-cursor.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAuditStore(store)
	for _, event := range []auditmodel.AuditEvent{
		{ID: "audit-a", WorkspaceID: "default", Event: "order.updated", ActorID: "user-a", CreatedAt: "2026-08-18T11:59:59Z"},
		{ID: "audit-b", WorkspaceID: "default", Event: "order.updated", ActorID: "user-a", CreatedAt: "2026-08-18T12:00:00Z"},
		{ID: "audit-c", WorkspaceID: "default", Event: "order.updated", ActorID: "user-a", CreatedAt: "2026-08-18T12:00:00Z"},
		{ID: "audit-d", WorkspaceID: "default", Event: "order.updated", ActorID: "user-a", CreatedAt: "2026-08-18T12:00:00Z"},
	} {
		if err := repository.InsertAuditEvent(t.Context(), "default", event); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{ActorID: "user-a", Limit: 2})
	if err != nil || len(first) != 2 || first[0].ID != "audit-d" || first[1].ID != "audit-c" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{ActorID: "user-a", Limit: 2, Cursor: auditmodel.EncodeAuditEventCursor(first[1])})
	if err != nil || len(second) != 2 || second[0].ID != "audit-b" || second[1].ID != "audit-a" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if _, err := repository.ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{Cursor: "invalid"}); err == nil {
		t.Fatal("invalid cursor was accepted")
	}
}

func TestEnsureRuntimeSchemaBackfillsAuditWorkspaceOnLegacyTable(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-legacy.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE _audit_events (
		id TEXT PRIMARY KEY, event TEXT NOT NULL, object_key TEXT, record_id TEXT,
		actor_id TEXT, role_key TEXT, summary TEXT, metadata_json TEXT NOT NULL,
		before_json TEXT NOT NULL, after_json TEXT NOT NULL, created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _audit_events
		(id, event, object_key, record_id, actor_id, role_key, summary, metadata_json, before_json, after_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-audit", "legacy.event", "", "", "system", "", "legacy", `{}`, `{}`, `{}`, "2026-07-19T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var migratedWorkspace string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT workspace_id FROM _audit_events WHERE id = ?`, "legacy-audit").Scan(&migratedWorkspace); err != nil {
		t.Fatalf("read migrated workspace: %v", err)
	}
	if migratedWorkspace != principalmodel.InstallationWorkspaceID {
		t.Fatalf("migrated workspace=%q", migratedWorkspace)
	}
	events, err := NewAuditStore(store).ListAuditEvents(t.Context(), principalmodel.InstallationWorkspaceID, auditmodel.AuditEventQuery{Limit: 10})
	if err != nil || len(events) != 1 || events[0].ID != "legacy-audit" || events[0].WorkspaceID != principalmodel.InstallationWorkspaceID {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}
