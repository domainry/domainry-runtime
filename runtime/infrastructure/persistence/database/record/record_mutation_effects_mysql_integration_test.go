package record

import (
	"os"
	"strings"
	"testing"

	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestInsertPublicationHandoffPersistsExplicitIntentOnMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "mysql", DatabaseDSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	message := publicationmodel.Message{
		MessageID: "mysql-explicit-intent", WorkspaceID: "mysql-explicit-intent-workspace",
		ConnectorKey: "test", Operation: "publish", DedupKey: "mysql-explicit-intent",
		Payload: map[string]any{"record_id": "record-1"},
	}
	if err := NewRecordStore(store).insertPublicationHandoffTx(t.Context(), tx, message); err != nil {
		t.Fatal(err)
	}
	var intentJSON string
	if err := tx.QueryRowContext(t.Context(), "SELECT intent_json FROM "+store.Identifier("_publication_outbox")+" WHERE workspace_id = "+store.Placeholder(1)+" AND id = "+store.Placeholder(2), message.WorkspaceID, message.MessageID).Scan(&intentJSON); err != nil {
		t.Fatal(err)
	}
	if intentJSON != "{}" {
		t.Fatalf("intent_json=%q, want explicit empty object", intentJSON)
	}
}
