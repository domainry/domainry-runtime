package report

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func TestDateBucketUsesUnixMillisecondsAndCanonicalOffset(t *testing.T) {
	got, err := (Profile{}).DateBucket("`event`.`occurred_at`", "day", "Asia/Shanghai", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"TIMESTAMPADD(SECOND, (`event`.`occurred_at` DIV 1000), '1970-01-01 00:00:00')",
		"CONVERT_TZ(",
		"'+00:00', '+08:00'",
		"'%Y-%m-%dT00:00:00'",
		"'+08:00'",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("date bucket missing %q: %s", fragment, got)
		}
	}
	if strings.Contains(got, "Asia/Shanghai") {
		t.Fatalf("fixed-offset zone should not require MySQL time-zone tables: %s", got)
	}
}

func TestDateBucketKeepsVariableOffsetIANAZone(t *testing.T) {
	got, err := (Profile{}).DateBucket("`event`.`occurred_at`", "hour", "America/New_York", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"America/New_York", "TIMESTAMPDIFF(SECOND", "MOD(ABS(", "%Y-%m-%dT%H:00:00"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("variable-offset date bucket missing %q: %s", fragment, got)
		}
	}
}

func TestDateBucketDateAndCalendarGrains(t *testing.T) {
	week, err := (Profile{}).DateBucket("`period`.`business_date`", "week", "Asia/Shanghai", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(week, "CAST(`period`.`business_date` AS DATETIME)") || !strings.Contains(week, "DATE_SUB(DATE(") || !strings.Contains(week, "WEEKDAY(") || strings.Contains(week, "DIV 1000") {
		t.Fatalf("invalid date-backed week bucket: %s", week)
	}
	quarter, err := (Profile{}).DateBucket("`period`.`occurred_at`", "quarter", "UTC", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(quarter, "QUARTER(") || !strings.HasSuffix(quarter, ", 'Z')") {
		t.Fatalf("invalid UTC quarter bucket: %s", quarter)
	}
}

func TestDateBucketRejectsInvalidPolicy(t *testing.T) {
	if _, err := (Profile{}).DateBucket("value", "minute", "UTC", false); err == nil {
		t.Fatal("unsupported grain was accepted")
	}
	if _, err := (Profile{}).DateBucket("value", "day", "Not/AZone", false); err == nil {
		t.Fatal("invalid time zone was accepted")
	}
}

func TestDateBucketExecutesInRealMySQLWithoutNamedTimeZoneTables(t *testing.T) {
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
	const table = "runtime_report_date_bucket_contract"
	if _, err := database.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	defer database.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table)
	if _, err := database.ExecContext(t.Context(), "CREATE TABLE "+table+" (id VARCHAR(64) PRIMARY KEY, occurred_at BIGINT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 10, 20, 23, 15, 42, 321000000, time.UTC)
	if _, err := database.ExecContext(t.Context(), "INSERT INTO "+table+" (id, occurred_at) VALUES (?, ?)", "event-1", instant.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	expression, err := (Profile{}).DateBucket("occurred_at", "day", "Asia/Shanghai", false)
	if err != nil {
		t.Fatal(err)
	}
	var bucket string
	if err := database.QueryRowContext(t.Context(), "SELECT "+expression+" FROM "+table+" WHERE id = ?", "event-1").Scan(&bucket); err != nil {
		t.Fatal(err)
	}
	if bucket != "2026-10-21T00:00:00+08:00" {
		t.Fatalf("bucket=%q", bucket)
	}
}
