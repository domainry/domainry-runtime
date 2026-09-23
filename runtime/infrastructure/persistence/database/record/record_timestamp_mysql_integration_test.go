package record

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func TestRecordSystemTimestampValuePersistsInRealMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	const table = "runtime_record_timestamp_contract"
	if _, err := database.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	defer database.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table)
	if _, err := database.ExecContext(t.Context(), "CREATE TABLE "+table+" (id VARCHAR(64) PRIMARY KEY, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 9, 24, 1, 2, 3, 0, time.UTC)
	value := stamp.Format(time.RFC3339Nano)
	if _, err := database.ExecContext(t.Context(), "INSERT INTO "+table+" (id, created_at, updated_at) VALUES (?, ?, ?)", "record-1", recordTimestampDBValue(testEngineProfile("mysql"), value), recordTimestampDBValue(testEngineProfile("mysql"), value)); err != nil {
		t.Fatal(err)
	}
	var createdAt, updatedAt time.Time
	if err := database.QueryRowContext(t.Context(), "SELECT created_at, updated_at FROM "+table+" WHERE id = ?", "record-1").Scan(&createdAt, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if !createdAt.UTC().Equal(stamp) || !updatedAt.UTC().Equal(stamp) {
		t.Fatalf("timestamps created=%s updated=%s want=%s", createdAt.UTC(), updatedAt.UTC(), stamp)
	}
	updated := stamp.Add(time.Minute)
	if _, err := database.ExecContext(t.Context(), "UPDATE "+table+" SET updated_at = ? WHERE id = ? AND updated_at = ?", recordTimestampDBValue(testEngineProfile("mysql"), updated.Format(time.RFC3339Nano)), "record-1", recordTimestampDBValue(testEngineProfile("mysql"), value)); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(t.Context(), "SELECT updated_at FROM "+table+" WHERE id = ?", "record-1").Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if !updatedAt.UTC().Equal(updated) {
		t.Fatalf("updated timestamp=%s want=%s", updatedAt.UTC(), updated)
	}
}
