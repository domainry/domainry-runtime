package schema

import (
	"database/sql/driver"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const mysqlInnoDBMaxIndexBytes = 3072

var mysqlVarcharLengthPattern = regexp.MustCompile(`(?i)VARCHAR\(([0-9]+)\)`)

func TestAuditEventMySQLCursorIndexesStayWithinInnoDBKeyLimit(t *testing.T) {
	utf8Definitions := auditEventColumnDefinitions("VARCHAR(191)", "VARCHAR(191)")
	if got := mysqlCompositeIndexBytes(t, utf8Definitions, auditEventRecordCursorColumns()); got != 3820 || got <= mysqlInnoDBMaxIndexBytes {
		t.Fatalf("regression fixture no longer reproduces the original 3820-byte oversized key: bytes=%d", got)
	}

	definitions := auditEventColumnDefinitions("VARCHAR(191)", mysqlAuditCursorColumnType)
	for _, test := range []struct {
		name      string
		columns   []string
		want      []string
		wantBytes int
	}{
		{name: "actor", columns: auditEventActorCursorColumns(), want: []string{"workspace_id", "actor_id", "created_at", "id"}, wantBytes: 1910},
		{name: "record", columns: auditEventRecordCursorColumns(), want: []string{"workspace_id", "object_key", "record_id", "created_at", "id"}, wantBytes: 2674},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !reflect.DeepEqual(test.columns, test.want) {
				t.Fatalf("cursor index columns=%v want=%v", test.columns, test.want)
			}
			if got := mysqlCompositeIndexBytes(t, definitions, test.columns); got != test.wantBytes || got > mysqlInnoDBMaxIndexBytes {
				t.Fatalf("cursor index bytes=%d want=%d limit=%d: columns=%v", got, test.wantBytes, mysqlInnoDBMaxIndexBytes, test.columns)
			}
		})
	}
}

func TestMySQLAuditCursorColumnNormalizationRepairsPartialBootstrap(t *testing.T) {
	state := &schemaSQLState{querySteps: []schemaSQLQueryStep{{
		columns: []string{"COLUMN_NAME", "COLUMN_TYPE", "CHARACTER_SET_NAME", "COLLATION_NAME"},
		rows: [][]driver.Value{
			{"id", "varchar(191)", "utf8mb4", "utf8mb4_unicode_ci"},
			{"created_at", "varchar(191)", "utf8mb4", "utf8mb4_unicode_ci"},
		},
	}}}
	database := openSchemaScriptedDB(state)
	t.Cleanup(func() { _ = database.Close() })
	store := scriptedSchemaStore{db: database, driver: "mysql"}

	if err := ensureMySQLAuditCursorColumns(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if len(state.execQueries) != 1 {
		t.Fatalf("normalization statements=%v", state.execQueries)
	}
	statement := state.execQueries[0]
	for _, fragment := range []string{
		`ALTER TABLE "_audit_events"`,
		`MODIFY COLUMN "id" ` + mysqlAuditCursorColumnType + ` NOT NULL`,
		`MODIFY COLUMN "created_at" ` + mysqlAuditCursorColumnType + ` NOT NULL`,
	} {
		if !strings.Contains(statement, fragment) {
			t.Fatalf("normalization DDL missing %q: %s", fragment, statement)
		}
	}
}

func mysqlCompositeIndexBytes(t *testing.T, definitions, columns []string) int {
	t.Helper()
	byColumn := map[string]string{}
	for _, definition := range definitions {
		parts := strings.SplitN(definition, " ", 2)
		if len(parts) == 2 {
			byColumn[parts[0]] = parts[1]
		}
	}
	total := 0
	for _, column := range columns {
		definition, exists := byColumn[column]
		if !exists {
			t.Fatalf("index column %s has no definition", column)
		}
		match := mysqlVarcharLengthPattern.FindStringSubmatch(definition)
		if len(match) != 2 {
			t.Fatalf("index column %s is not bounded VARCHAR: %s", column, definition)
		}
		length, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatal(err)
		}
		bytesPerCharacter := 4
		if strings.Contains(strings.ToLower(definition), "character set ascii") {
			bytesPerCharacter = 1
		}
		total += length * bytesPerCharacter
	}
	return total
}
