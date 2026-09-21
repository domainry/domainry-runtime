package report

import (
	"path/filepath"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestReportSnapshotSourceVersionHashesEffectiveScopedProjection(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "report-source-version.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE ticket (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, note TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}

	object := definitionmodel.ObjectSchema{Key: "ticket", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"},
		{Key: "note", Type: "text"},
	}}
	records := recordpersistence.NewRecordStore(store)
	stamp := "2026-09-21T10:00:00Z"
	insert := func(workspaceID, id, status, note string) {
		t.Helper()
		if err := records.InsertRecord(t.Context(), workspaceID, object, recordmodel.Record{
			ID: id, CreatedAt: stamp, UpdatedAt: stamp,
			Data: map[string]any{"status": status, "note": note},
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert("workspace-a", "older", "open", "initial")
	insert("workspace-a", "newer", "open", "initial")
	insert("workspace-b", "other", "open", "initial")

	request := reportcontract.ReportSnapshotSourceVersionRequest{
		WorkspaceID: "workspace-a",
		Objects:     map[string]definitionmodel.ObjectSchema{"t": object},
		Queries: map[string]recordmodel.RecordListQuery{"t": {
			AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted,
			SelectFields:      []string{"status"},
		}},
	}
	reader := NewReportSQLStore(store)
	read := func() string {
		t.Helper()
		version, err := reader.ReadReportSnapshotSourceVersion(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if version.SourceVersions["t"] == "" {
			t.Fatal("source version is empty")
		}
		return version.SourceVersions["t"]
	}
	original := read()

	// Keep the timestamp unchanged to prove that content, rather than
	// count+MAX(updated_at), invalidates evidence for a non-latest row.
	if err := records.UpdateRecord(t.Context(), "workspace-a", object, recordmodel.Record{
		ID: "older", UpdatedAt: stamp, Data: map[string]any{"status": "closed"},
	}); err != nil {
		t.Fatal(err)
	}
	changed := read()
	if changed == original {
		t.Fatal("projected same-timestamp update did not change source version")
	}

	if err := records.UpdateRecord(t.Context(), "workspace-a", object, recordmodel.Record{
		ID: "older", UpdatedAt: stamp, Data: map[string]any{"note": "not projected"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != changed {
		t.Fatalf("non-projected field changed source version: got %q want %q", got, changed)
	}

	if err := records.UpdateRecord(t.Context(), "workspace-b", object, recordmodel.Record{
		ID: "other", UpdatedAt: stamp, Data: map[string]any{"status": "closed"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != changed {
		t.Fatalf("other workspace changed scoped source version: got %q want %q", got, changed)
	}
}
