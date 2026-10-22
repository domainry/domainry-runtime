package integrationtest

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/entityid"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func TestAuditCursorSchemaAndPaginationOnRealMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
			t.Fatal("RUNTIME_MYSQL_TEST_DSN is required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	cfg := realDialectMySQLConfig(t, dsn, fmt.Sprintf("runtime_audit_cursor_%d", time.Now().UnixNano()))
	store, err := persistence.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	wantIndexes := map[string][]string{
		"idx_audit_event_actor_cursor": {"workspace_id", "actor_id", "created_at", "id"},
		// InnoDB appends the remaining primary-key column (id) to this wide
		// secondary index, so the physical definition omits only that duplicate.
		"idx_audit_event_record_cursor": {"workspace_id", "object_key", "record_id", "created_at"},
	}
	rows, err := store.DB().QueryContext(t.Context(), `SELECT INDEX_NAME, COLUMN_NAME, SEQ_IN_INDEX, SUB_PART FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '_audit_events' AND INDEX_NAME IN ('idx_audit_event_actor_cursor', 'idx_audit_event_record_cursor') ORDER BY INDEX_NAME, SEQ_IN_INDEX`)
	if err != nil {
		t.Fatal(err)
	}
	gotIndexes := map[string][]string{}
	for rows.Next() {
		var index, column string
		var sequence int
		var prefixLength sql.NullInt64
		if err := rows.Scan(&index, &column, &sequence, &prefixLength); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if prefixLength.Valid {
			_ = rows.Close()
			t.Fatalf("index %s column %s unexpectedly uses prefix length %d", index, column, prefixLength.Int64)
		}
		gotIndexes[index] = append(gotIndexes[index], column)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotIndexes, wantIndexes) {
		t.Fatalf("audit cursor indexes=%v want=%v", gotIndexes, wantIndexes)
	}

	columnRows, err := store.DB().QueryContext(t.Context(), `SELECT COLUMN_NAME, CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '_audit_events' AND COLUMN_NAME IN ('audit_event_id', 'record_id', 'actor_id')`)
	if err != nil {
		t.Fatal(err)
	}
	type charsetState struct{ charset, collation string }
	charsets := map[string]charsetState{}
	for columnRows.Next() {
		var name string
		var state charsetState
		if err := columnRows.Scan(&name, &state.charset, &state.collation); err != nil {
			_ = columnRows.Close()
			t.Fatal(err)
		}
		charsets[name] = state
	}
	if err := columnRows.Err(); err != nil {
		_ = columnRows.Close()
		t.Fatal(err)
	}
	if err := columnRows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"audit_event_id"} {
		if state := charsets[column]; state.charset != "ascii" || state.collation != "ascii_bin" {
			t.Fatalf("cursor column %s charset=%+v", column, state)
		}
	}
	for _, column := range []string{"record_id", "actor_id"} {
		if state := charsets[column]; state.charset != "utf8mb4" {
			t.Fatalf("filter column %s did not retain utf8mb4: %+v", column, state)
		}
	}
	var createdAtType string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '_audit_events' AND COLUMN_NAME = 'created_at'`).Scan(&createdAtType); err != nil || createdAtType != "bigint" {
		t.Fatalf("created_at type=%q err=%v", createdAtType, err)
	}
	var idType string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '_audit_events' AND COLUMN_NAME = 'id'`).Scan(&idType); err != nil || idType != "bigint" {
		t.Fatalf("id type=%q err=%v", idType, err)
	}

	repository := auditpersistence.NewAuditStoreFromRuntimeStore(store)
	stamp := "2026-08-22T12:00:00Z"
	auditA := auditCursorTestID(t, "a")
	auditB := auditCursorTestID(t, "b")
	auditC := auditCursorTestID(t, "c")
	for _, event := range []auditmodel.AuditEvent{
		{AuditEventID: auditA, WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一", ActorID: "用户甲", CreatedAt: stamp},
		{AuditEventID: auditB, WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一", ActorID: "用户甲", CreatedAt: stamp},
		{AuditEventID: auditC, WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一", ActorID: "用户甲", CreatedAt: stamp},
		{AuditEventID: auditCursorTestID(t, "prefix"), WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一附", ActorID: "用户甲附", CreatedAt: stamp},
	} {
		if err := repository.InsertAuditEvent(t.Context(), "workspace-a", event); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repository.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{ObjectKey: "订单", RecordID: "记录一", Limit: 2})
	if err != nil || len(first) != 2 || first[0].AuditEventID != auditC || first[1].AuditEventID != auditB {
		t.Fatalf("first exact record page=%+v err=%v", first, err)
	}
	second, err := repository.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{ObjectKey: "订单", RecordID: "记录一", Cursor: auditmodel.EncodeAuditEventCursor(first[1]), Limit: 2})
	if err != nil || len(second) != 1 || second[0].AuditEventID != auditA {
		t.Fatalf("second exact record page=%+v err=%v", second, err)
	}
	actors, err := repository.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{ActorID: "用户甲", Limit: 10})
	if err != nil || len(actors) != 3 {
		t.Fatalf("exact actor events=%+v err=%v", actors, err)
	}
}

func TestAuditBusinessClassAndRequestIDFiltersOnRealMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
			t.Fatal("RUNTIME_MYSQL_TEST_DSN is required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	cfg := realDialectMySQLConfig(t, dsn, fmt.Sprintf("runtime_audit_filters_%d", time.Now().UnixNano()))
	store, err := persistence.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertAuditBusinessClassAndRequestIDFilters(t, store)
}

func TestAuditBusinessClassAndRequestIDFiltersOnRealPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_POSTGRES_TEST_DSN"))
	if dsn == "" {
		if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
			t.Fatal("RUNTIME_POSTGRES_TEST_DSN is required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("RUNTIME_POSTGRES_TEST_DSN is not configured")
	}
	cfg := realDialectPostgresConfig(t, dsn, fmt.Sprintf("runtime_audit_filters_%d", time.Now().UnixNano()))
	store, err := persistence.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertAuditBusinessClassAndRequestIDFilters(t, store)
}

func assertAuditBusinessClassAndRequestIDFilters(t *testing.T, store *persistence.RuntimeStore) {
	t.Helper()
	repository := auditpersistence.NewAuditStoreFromRuntimeStore(store)
	requestID := `req-%_\literal~`
	matchID := auditCursorTestID(t, "business-match")
	for _, event := range []auditmodel.AuditEvent{
		{AuditEventID: matchID, WorkspaceID: "workspace-a", Event: "ticket.assign", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": requestID}, CreatedAt: "2026-08-22T12:00:04Z"},
		{AuditEventID: auditCursorTestID(t, "business-wildcard-decoy"), WorkspaceID: "workspace-a", Event: "ticket.assign", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": `req-ABX\literal~`}, CreatedAt: "2026-08-22T12:00:03Z"},
		{AuditEventID: auditCursorTestID(t, "business-backslash-decoy"), WorkspaceID: "workspace-a", Event: "ticket.assign", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": `req-%_\\literal~`}, CreatedAt: "2026-08-22T12:00:02Z"},
		{AuditEventID: auditCursorTestID(t, "governance"), WorkspaceID: "workspace-a", Event: "identity_role_updated", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": requestID}, CreatedAt: "2026-08-22T12:00:01Z"},
		{AuditEventID: auditCursorTestID(t, "operations"), WorkspaceID: "workspace-a", Event: "scheduler_run_completed", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": requestID}, CreatedAt: "2026-08-22T12:00:00Z"},
	} {
		if err := repository.InsertAuditEvent(t.Context(), "workspace-a", event); err != nil {
			t.Fatalf("insert %s: %v", event.AuditEventID, err)
		}
	}

	events, err := repository.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{
		ObjectKey: "ticket", RecordID: "ticket-1", RequestID: requestID,
		Class: auditmodel.AuditEventClassBusiness, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list filtered business audit events: %v", err)
	}
	if len(events) != 1 || events[0].AuditEventID != matchID {
		t.Fatalf("filtered business events=%+v", events)
	}
}

func auditCursorTestID(t *testing.T, label string) string {
	t.Helper()
	id, err := entityid.Derive("audevt", "runtime-audit-cursor-test", label)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
