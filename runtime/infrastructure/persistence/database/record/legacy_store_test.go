package record

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRecordCompatibilityAPIsRejectCancelledContext(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/records.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "record_cancel", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE record_cancel (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListRecords(ctx, "workspace-primary", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRecords error = %v", err)
	}
	if _, err := repository.UniqueExists(ctx, "workspace-primary", object.Key, "name", "", "never"); !errors.Is(err, context.Canceled) {
		t.Fatalf("UniqueExists error = %v", err)
	}
}
