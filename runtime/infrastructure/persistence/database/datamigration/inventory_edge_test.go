package datamigration

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInventoryRejectsMissingCancelledAndUnsupportedInputs(t *testing.T) {
	if _, err := Inspect(t.Context(), nil, EngineSQLite, ""); err == nil || !strings.Contains(err.Error(), "requires a database") {
		t.Fatalf("nil database error = %v", err)
	}
	db := openSQLiteInventoryFixture(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Inspect(cancelled, db, EngineSQLite, ""); err != context.Canceled {
		t.Fatalf("cancelled inventory error = %v", err)
	}
	if _, err := Inspect(t.Context(), db, Engine("oracle"), ""); err == nil || !strings.Contains(err.Error(), "unsupported inventory engine") {
		t.Fatalf("unsupported engine error = %v", err)
	}
}

func TestSQLiteInventoryEmptyAndUnsafeCatalogObjects(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE safe_records (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	inventory, err := Inspect(t.Context(), db, EngineSQLite, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Tables) != 1 || len(inventory.Sequences) != 0 || inventory.Tables[0].WorkspaceScoped {
		t.Fatalf("empty SQLite inventory = %#v", inventory)
	}

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE "unsafe-name" (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), db, EngineSQLite, ""); err == nil || !strings.Contains(err.Error(), "unsafe SQLite table identifier") {
		t.Fatalf("unsafe table error = %v", err)
	}
}

func TestSQLiteInventoryRejectsUnsafeIndex(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE records (id TEXT); CREATE INDEX "unsafe-index" ON records(id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), db, EngineSQLite, ""); err == nil || !strings.Contains(err.Error(), "unsafe SQLite index identifier") {
		t.Fatalf("unsafe index error = %v", err)
	}
}

func TestInventoryClosedSQLiteDatabaseFails(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), db, EngineSQLite, ""); err == nil || !strings.Contains(err.Error(), "inventory SQLite tables") {
		t.Fatalf("closed database error = %v", err)
	}
}

func TestInventoryPrimitiveHelpers(t *testing.T) {
	if got := primaryKeyPosition([]ColumnInventory{{Name: "first", PrimaryKey: 2}, {Name: "second", PrimaryKey: 1}}, "first"); got != 2 {
		t.Fatalf("primary key position = %d", got)
	}
	if got := primaryKeyPosition(nil, "missing"); got != 0 {
		t.Fatalf("missing primary key position = %d", got)
	}
	if quote(`a"b`) != `"a""b"` || mysqlQuote("a`b") != "`a``b`" {
		t.Fatalf("identifier quoting mismatch: %q %q", quote(`a"b`), mysqlQuote("a`b"))
	}
	if _, err := ParseEngine("unknown"); err == nil {
		t.Fatal("unknown engine was accepted")
	}

	tests := []struct {
		value any
		want  bool
		ok    bool
	}{
		{value: true, want: true, ok: true},
		{value: false, ok: true},
		{value: int64(1), want: true, ok: true},
		{value: int64(0), ok: true},
		{value: int64(2), want: true},
		{value: " true ", want: true, ok: true},
		{value: "false", ok: true},
		{value: "invalid"},
		{value: 1},
	}
	for _, tt := range tests {
		if got, ok := parseSQLiteBoolean(tt.value); got != tt.want || ok != tt.ok {
			t.Errorf("parseSQLiteBoolean(%v) = (%v, %v), want (%v, %v)", tt.value, got, ok, tt.want, tt.ok)
		}
	}
}

func TestSQLiteInventoryPreservesDefaultsAndCompositePrimaryKeyOrder(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE composite_records (first INTEGER, second TEXT DEFAULT 'fallback', PRIMARY KEY (second, first))`); err != nil {
		t.Fatal(err)
	}
	table, err := inspectSQLiteTable(t.Context(), db, "composite_records")
	if err != nil {
		t.Fatal(err)
	}
	if len(table.PrimaryKey) != 2 || table.PrimaryKey[0] != "second" || table.PrimaryKey[1] != "first" {
		t.Fatalf("composite primary key = %#v", table.PrimaryKey)
	}
	if len(table.Columns) != 2 || !strings.Contains(table.Columns[1].Default, "fallback") {
		t.Fatalf("column defaults = %#v", table.Columns)
	}
}
