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
		"idx_audit_event_actor_cursor":  {"workspace_id", "actor_id", "created_at", "id"},
		"idx_audit_event_record_cursor": {"workspace_id", "object_key", "record_id", "created_at", "id"},
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

	columnRows, err := store.DB().QueryContext(t.Context(), `SELECT COLUMN_NAME, CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '_audit_events' AND COLUMN_NAME IN ('id', 'created_at', 'record_id', 'actor_id')`)
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
	for _, column := range []string{"id", "created_at"} {
		if state := charsets[column]; state.charset != "ascii" || state.collation != "ascii_bin" {
			t.Fatalf("cursor column %s charset=%+v", column, state)
		}
	}
	for _, column := range []string{"record_id", "actor_id"} {
		if state := charsets[column]; state.charset != "utf8mb4" {
			t.Fatalf("filter column %s did not retain utf8mb4: %+v", column, state)
		}
	}

	repository := auditpersistence.NewRepositoryFromStore(store)
	stamp := "2026-08-22T12:00:00Z"
	for _, event := range []auditmodel.AuditEvent{
		{ID: "audit-a", WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一", ActorID: "用户甲", CreatedAt: stamp},
		{ID: "audit-b", WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一", ActorID: "用户甲", CreatedAt: stamp},
		{ID: "audit-c", WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一", ActorID: "用户甲", CreatedAt: stamp},
		{ID: "audit-prefix", WorkspaceID: "workspace-a", Event: "record.updated", ObjectKey: "订单", RecordID: "记录一附", ActorID: "用户甲附", CreatedAt: stamp},
	} {
		if err := repository.InsertAuditEvent(t.Context(), "workspace-a", event); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repository.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{ObjectKey: "订单", RecordID: "记录一", Limit: 2})
	if err != nil || len(first) != 2 || first[0].ID != "audit-c" || first[1].ID != "audit-b" {
		t.Fatalf("first exact record page=%+v err=%v", first, err)
	}
	second, err := repository.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{ObjectKey: "订单", RecordID: "记录一", Cursor: auditmodel.EncodeAuditEventCursor(first[1]), Limit: 2})
	if err != nil || len(second) != 1 || second[0].ID != "audit-a" {
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
	repository := auditpersistence.NewRepositoryFromStore(store)
	requestID := `req-%_\literal~`
	for _, event := range []auditmodel.AuditEvent{
		{ID: "audit-business-match", WorkspaceID: "workspace-a", Event: "ticket.assign", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": requestID}, CreatedAt: "2026-08-22T12:00:04Z"},
		{ID: "audit-business-wildcard-decoy", WorkspaceID: "workspace-a", Event: "ticket.assign", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": `req-ABX\literal~`}, CreatedAt: "2026-08-22T12:00:03Z"},
		{ID: "audit-business-backslash-decoy", WorkspaceID: "workspace-a", Event: "ticket.assign", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": `req-%_\\literal~`}, CreatedAt: "2026-08-22T12:00:02Z"},
		{ID: "audit-governance", WorkspaceID: "workspace-a", Event: "identity_role_updated", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": requestID}, CreatedAt: "2026-08-22T12:00:01Z"},
		{ID: "audit-operations", WorkspaceID: "workspace-a", Event: "scheduler_run_completed", ObjectKey: "ticket", RecordID: "ticket-1", ActorID: "manager", Metadata: map[string]any{"request_id": requestID}, CreatedAt: "2026-08-22T12:00:00Z"},
	} {
		if err := repository.InsertAuditEvent(t.Context(), "workspace-a", event); err != nil {
			t.Fatalf("insert %s: %v", event.ID, err)
		}
	}

	events, err := repository.ListAuditEvents(t.Context(), "workspace-a", auditmodel.AuditEventQuery{
		ObjectKey: "ticket", RecordID: "ticket-1", RequestID: requestID,
		Class: auditmodel.AuditEventClassBusiness, Limit: 100,
	})
	if err != nil {
		t.Fatalf("list filtered business audit events: %v", err)
	}
	if len(events) != 1 || events[0].ID != "audit-business-match" {
		t.Fatalf("filtered business events=%+v", events)
	}
}

func TestAuditCursorPartialBootstrapRepairsOnRealMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
			t.Fatal("RUNTIME_MYSQL_TEST_DSN is required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	cfg := realDialectMySQLConfig(t, dsn, fmt.Sprintf("runtime_audit_retry_%d", time.Now().UnixNano()))
	store, err := persistence.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE _audit_events (
		id VARCHAR(191) PRIMARY KEY,
		workspace_id VARCHAR(191) NOT NULL,
		event VARCHAR(191) NOT NULL,
		object_key VARCHAR(191),
		record_id VARCHAR(191),
		actor_id VARCHAR(191),
		role_key VARCHAR(191),
		summary TEXT,
		metadata_json TEXT NOT NULL,
		before_json TEXT NOT NULL,
		after_json TEXT NOT NULL,
		created_at VARCHAR(191) NOT NULL,
		INDEX idx_audit_event_actor_cursor (workspace_id, actor_id, created_at, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	var recordIndexColumns, prefixedColumns int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*), COUNT(SUB_PART) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '_audit_events' AND INDEX_NAME = 'idx_audit_event_record_cursor'`).Scan(&recordIndexColumns, &prefixedColumns); err != nil {
		t.Fatal(err)
	}
	if recordIndexColumns != 5 || prefixedColumns != 0 {
		t.Fatalf("repaired record cursor columns=%d prefixed=%d", recordIndexColumns, prefixedColumns)
	}
	for _, column := range []string{"id", "created_at"} {
		var charset, collation string
		if err := store.DB().QueryRowContext(t.Context(), `SELECT CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = '_audit_events' AND COLUMN_NAME = ?`, column).Scan(&charset, &collation); err != nil {
			t.Fatal(err)
		}
		if charset != "ascii" || collation != "ascii_bin" {
			t.Fatalf("repaired cursor column %s charset=%s collation=%s", column, charset, collation)
		}
	}
}
