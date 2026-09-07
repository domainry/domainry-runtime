package schema

import (
	"database/sql/driver"
	"strings"
	"testing"
)

func TestApplicationSchemaDoesNotEmitNotificationOwnedDDL(t *testing.T) {
	state := &schemaSQLState{querySteps: []schemaSQLQueryStep{{
		columns: []string{"cid", "name", "type", "notnull", "default", "pk"},
		rows:    [][]driver.Value{{int64(0), "id", "TEXT", int64(1), nil, int64(1)}, {int64(1), "time_zone", "TEXT", int64(1), "'UTC'", int64(0)}},
	}}}
	database := openSchemaScriptedDB(state)
	t.Cleanup(func() { _ = database.Close() })

	if err := EnsureApplicationSchema(t.Context(), scriptedSchemaStore{db: database, driver: "mysql"}); err != nil {
		t.Fatal(err)
	}

	for _, query := range state.execQueries {
		if strings.Contains(query, "notification_") {
			t.Fatalf("Runtime metadata schema emitted Notification-owned DDL: %s", query)
		}
	}
}
