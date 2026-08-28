package schema

import (
	"strings"
	"testing"
)

func TestMetadataSchemaDoesNotEmitNotificationOwnedDDL(t *testing.T) {
	state := &schemaSQLState{}
	database := openSchemaScriptedDB(state)
	t.Cleanup(func() { _ = database.Close() })

	if err := EnsureMetadataSchema(t.Context(), scriptedSchemaStore{db: database, driver: "mysql"}); err != nil {
		t.Fatal(err)
	}

	for _, query := range state.execQueries {
		if strings.Contains(query, "notification_") {
			t.Fatalf("Runtime metadata schema emitted Notification-owned DDL: %s", query)
		}
	}
}
