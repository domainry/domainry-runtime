package report_test

import (
	"context"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type boundedDatasetAccess struct {
	objects map[string]definitionmodel.ObjectSchema
}

func (a boundedDatasetAccess) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
	return a.objects[objectKey], nil
}

func (boundedDatasetAccess) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}

func (boundedDatasetAccess) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}

func (boundedDatasetAccess) CanPushdownReportDataset(context.Context, principalmodel.Principal, []definitionmodel.ObjectSchema) bool {
	return true
}

func TestDatasetBoundedPageExecutesLimitInDatabaseWithWorkspaceIsolation(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "bounded-dataset.db"), IntegrationSecretKey: "bounded-dataset-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE entry (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, category TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "entry", Fields: []definitionmodel.FieldSchema{{Key: "category", Type: "text"}}}
	records := recordpersistence.NewRecordStore(store)
	insert := func(workspaceID, id, category string) {
		t.Helper()
		if err := records.InsertRecord(t.Context(), workspaceID, object, recordmodel.Record{ID: id, CreatedAt: "2026-08-17T00:00:00Z", UpdatedAt: "2026-08-17T00:00:00Z", Data: map[string]any{"category": category}}); err != nil {
			t.Fatal(err)
		}
	}
	insert("workspace-a", "a-1", "alpha")
	insert("workspace-a", "a-2", "alpha")
	insert("workspace-a", "b-1", "beta")
	insert("workspace-b", "foreign", "foreign")
	report := reportmodel.ReportSchema{Key: "entry-count", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "entry", Alias: "entries"},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "category", Field: reportmodel.ReportDatasetField{SourceAlias: "entries", FieldKey: "category"}}},
		Measures:   []reportmodel.ReportDatasetMeasure{{Key: "records", Operation: "count", SourceAlias: "entries"}},
		Sort:       []reportmodel.ReportDatasetSort{{Key: "category", Direction: "asc"}},
		Limit:      100,
	}}
	service := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Access:    boundedDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"entry": object}},
		ObjectSQL: reportpersistence.NewReportDatasetStore(store),
	})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	first, err := service.ExecuteExportReportPage(t.Context(), report, nil, "", 0, 1, principal)
	if err != nil || len(first.Rows) != 1 || first.Rows[0].Dimensions["category"] != "alpha" || first.Rows[0].Measures["records"] != "2" || !first.Truncated || first.Total != 2 || first.TotalSemantics != reportmodel.ReportTotalExact {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := service.ExecuteExportReportPage(t.Context(), report, nil, first.ExecutionCursor, 1, 1, principal)
	if err != nil || len(second.Rows) != 1 || second.Rows[0].Dimensions["category"] != "beta" || second.Rows[0].Measures["records"] != "1" || second.Truncated || second.Total != 2 || second.TotalSemantics != reportmodel.ReportTotalExact {
		t.Fatalf("second=%#v err=%v", second, err)
	}
}
