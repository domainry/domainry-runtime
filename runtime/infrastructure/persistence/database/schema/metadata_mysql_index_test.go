package schema

import (
	"strings"
	"testing"
)

func TestMetadataSchemaUsesPortableMySQLNotificationIndexColumns(t *testing.T) {
	state := &schemaSQLState{}
	database := openSchemaScriptedDB(state)
	t.Cleanup(func() { _ = database.Close() })

	if err := EnsureMetadataSchema(t.Context(), scriptedSchemaStore{db: database, driver: "mysql"}); err != nil {
		t.Fatal(err)
	}

	var inboxDDL string
	for _, query := range state.execQueries {
		if strings.Contains(query, "notification_inbox_items") {
			inboxDDL = query
			break
		}
	}
	if inboxDDL == "" {
		t.Fatal("notification inbox DDL was not emitted")
	}
	if count := strings.Count(inboxDDL, "VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin"); count != 19 {
		t.Fatalf("notification inbox indexed column type count=%d DDL=%s", count, inboxDDL)
	}
}
