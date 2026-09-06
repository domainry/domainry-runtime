package workspaceaggregate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	aggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestResolveActiveUsesActionTransactionAndFailsClosedForInactiveOrUnknownWorkspaces(t *testing.T) {
	runtimeStore := openWorkspaceAggregateTestStore(t, filepath.Join(t.TempDir(), "Workspace-usage-catalog.db"))
	defer runtimeStore.Close()
	insertWorkspaceAggregateCatalogRow(t, runtimeStore, "workspace-active", "active-code", "active")
	insertWorkspaceAggregateCatalogRow(t, runtimeStore, "workspace-inactive", "inactive-code", "inactive")
	insertWorkspaceAggregateCommercialRow(t, runtimeStore, "workspace-active", "standard", 5, 25)
	insertWorkspaceAggregateCommercialRow(t, runtimeStore, "workspace-inactive", "standard", 5, 25)
	store := NewStore(runtimeStore)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve usage Workspace page")
	if _, err := store.ResolveActive(t.Context(), scope, []string{"workspace-active"}); err == nil {
		t.Fatal("Workspace usage catalog resolve was allowed outside an Action transaction")
	}
	tx, err := runtimeStore.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(context.Background(), tx)
	resolved, err := store.ResolveActive(txCtx, scope, []string{"workspace-active", "workspace-inactive", "workspace-unknown"})
	if err != nil {
		t.Fatal(err)
	}
	active := resolved["workspace-active"]
	if len(resolved) != 1 || active.CanonicalCode != "active-code" || active.DisplayName != "active-code" ||
		active.CommercialPlan != "standard" || active.IncludedUserLimit != 5 || active.MaxUserLimit != 25 {
		t.Fatalf("resolved=%#v", resolved)
	}
}

func TestResolveActiveCommercialProjectionFailsClosedForInvalidTerms(t *testing.T) {
	runtimeStore := openWorkspaceAggregateTestStore(t, filepath.Join(t.TempDir(), "Workspace-invalid-commercial-catalog.db"))
	defer runtimeStore.Close()
	insertWorkspaceAggregateCatalogRow(t, runtimeStore, "workspace-active", "active-code", "active")
	insertWorkspaceAggregateCommercialRow(t, runtimeStore, "workspace-active", "standard", 10, 5)
	store := NewStore(runtimeStore)
	tx, err := runtimeStore.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = store.ResolveActive(database.WithActionExecutionTransaction(t.Context(), tx), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve billing catalog"), []string{"workspace-active"})
	if err == nil || !strings.Contains(err.Error(), "invalid installation Workspace catalog row") {
		t.Fatalf("error=%v", err)
	}
}

func TestResolveUsageWorkspaceLocksCanonicalRevisionAndProjectsTypedCommercialConfiguration(t *testing.T) {
	runtimeStore := openWorkspaceAggregateTestStore(t, filepath.Join(t.TempDir(), "Workspace-exact-usage-catalog.db"))
	insertWorkspaceAggregateCatalogRow(t, runtimeStore, "workspace-physical", "night-tokyo", "active")
	insertWorkspaceAggregateCommercialRow(t, runtimeStore, "workspace-physical", "premium", 12, 40)
	store := NewStore(runtimeStore)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve exact Workspace billing facts")
	if _, err := store.ResolveUsageWorkspace(t.Context(), scope, "night-tokyo", 1); err == nil {
		t.Fatal("exact Workspace usage resolve was allowed outside the Action transaction")
	}
	tx, err := runtimeStore.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(t.Context(), tx)
	workspace, err := store.ResolveUsageWorkspace(txCtx, scope, "night-tokyo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.ID != "workspace-physical" || workspace.CanonicalCode != "night-tokyo" || workspace.Status != "active" || workspace.Revision != 1 ||
		workspace.CommercialPlan != "premium" || workspace.IncludedUserLimit != 12 || workspace.MaxUserLimit != 40 ||
		workspace.IncludedCustomerLimit != 0 || workspace.MaxCustomerLimit != 100 || workspace.IncludedStoreLimit != 1 || workspace.MaxStores != 2 ||
		workspace.ContractDate != "2026-09-06" || workspace.BillingDay != 1 || workspace.CommercialRevision != 1 {
		t.Fatalf("workspace=%#v", workspace)
	}
	if _, err := store.ResolveUsageWorkspace(txCtx, scope, "night-tokyo", 2); !errors.Is(err, aggregatecontract.ErrUsageWorkspaceRevisionConflict) {
		t.Fatalf("revision error=%v", err)
	}
	if _, err := store.ResolveUsageWorkspace(txCtx, scope, "missing", 1); !errors.Is(err, aggregatecontract.ErrUsageWorkspaceNotFound) {
		t.Fatalf("missing error=%v", err)
	}
}

func TestResolveUsageWorkspaceRejectsSuspendedWorkspace(t *testing.T) {
	runtimeStore := openWorkspaceAggregateTestStore(t, filepath.Join(t.TempDir(), "Workspace-suspended-usage-catalog.db"))
	insertWorkspaceAggregateCatalogRow(t, runtimeStore, "workspace-suspended", "night-osaka", "suspended")
	insertWorkspaceAggregateCommercialRow(t, runtimeStore, "workspace-suspended", "standard", 5, 25)
	tx, err := runtimeStore.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = NewStore(runtimeStore).ResolveUsageWorkspace(
		database.WithActionExecutionTransaction(t.Context(), tx),
		principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "reject inactive Workspace billing"),
		"night-osaka", 1,
	)
	if !errors.Is(err, aggregatecontract.ErrUsageWorkspaceInactive) {
		t.Fatalf("inactive error=%v", err)
	}
}

func TestStoreAggregatesOnlyExplicitWorkspacesAndUsesCanonicalLogicalDimension(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "Workspace-aggregate.db")
	runtimeStore := openWorkspaceAggregateTestStore(t, databasePath)
	object := definitionmodel.ObjectSchema{Key: "sale", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "select"},
		{Key: "amount", Type: "currency", Config: map[string]any{"precision": 10, "scale": 2}},
	}}
	if err := appschemapersistence.NewApplicationSchemaStore(runtimeStore).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test Workspace aggregate schema"), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []struct{ id, code, status string }{{"workspace-a", "north", "active"}, {"workspace-b", "south", "active"}, {"workspace-c", "outside", "active"}, {"workspace-disabled", "disabled", "inactive"}} {
		insertWorkspaceAggregateCatalogRow(t, runtimeStore, workspace.id, workspace.code, workspace.status)
	}
	records := recordpersistence.NewRecordStore(runtimeStore)
	rows := []struct{ workspace, id, status, amount string }{
		{"workspace-a", "sale-a1", "paid", "10.25"}, {"workspace-a", "sale-a2", "paid", "1.75"},
		{"workspace-b", "sale-b1", "paid", "8.00"}, {"workspace-b", "sale-b2", "void", "99.00"},
		{"workspace-c", "sale-c1", "paid", "1000.00"},
	}
	for _, row := range rows {
		if err := records.InsertRecord(t.Context(), row.workspace, object, recordmodel.Record{ID: row.id, CreatedAt: "2026-09-06T00:00:00Z", UpdatedAt: "2026-09-06T00:00:00Z", Data: map[string]any{"status": row.status, "amount": row.amount}}); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(runtimeStore)
	catalog, err := store.ListActive(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test catalog"), 4)
	if err != nil || len(catalog) != 3 {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	selected := make([]aggregatecontract.Workspace, 0, 2)
	for _, workspace := range catalog {
		if workspace.CanonicalCode == "north" || workspace.CanonicalCode == "south" {
			selected = append(selected, workspace)
		}
	}
	request := aggregatecontract.Query{
		Workspaces: selected, Object: object,
		Dimensions:    []runtimeext.CrossWorkspaceAggregateDimension{{Key: "store", Field: runtimeext.CrossWorkspaceDimensionWorkspace}},
		Measures:      []runtimeext.CrossWorkspaceAggregateMeasure{{Key: "orders", Operation: runtimeext.AggregateCount}, {Key: "total", Operation: runtimeext.AggregateSum, Field: "amount"}, {Key: "average", Operation: runtimeext.AggregateAvg, Field: "amount"}},
		RecordQuery:   recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, FilterExpression: &recordmodel.RecordFilterExpression{Field: "status", Operator: "eq", Value: "paid"}},
		MaxSourceRows: 10, MaxResultRows: 10,
	}
	result, err := store.Aggregate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceRowCount != 3 || len(result.Rows) != 2 || result.Rows[0]["store"] != "north" || result.Rows[0]["orders"] != "2" || result.Rows[0]["total"] != "12.00" || result.Rows[0]["average"] != "6.00" || result.Rows[1]["store"] != "south" || result.Rows[1]["total"] != "8.00" {
		t.Fatalf("result=%#v", result)
	}
	for _, row := range result.Rows {
		if row["store"] == "outside" || row["store"] == "workspace-a" || row["store"] == "workspace-b" {
			t.Fatalf("physical or out-of-scope Workspace leaked: %#v", result.Rows)
		}
	}

	request.MaxSourceRows = 2
	if _, err := store.Aggregate(t.Context(), request); !errors.As(err, new(*aggregatecontract.LimitExceededError)) {
		t.Fatalf("source limit error=%v", err)
	}
	request.MaxSourceRows, request.MaxResultRows, request.RecordQuery.FilterExpression = 10, 1, nil
	if _, err := store.Aggregate(t.Context(), request); !errors.As(err, new(*aggregatecontract.LimitExceededError)) {
		t.Fatalf("result limit error=%v", err)
	}

	if err := runtimeStore.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openWorkspaceAggregateTestStore(t, databasePath)
	reconstructed, err := NewStore(restarted).ListActive(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "restart catalog"), 4)
	if err != nil || len(reconstructed) != 3 || reconstructed[0].CanonicalCode != "north" || reconstructed[2].CanonicalCode != "south" {
		t.Fatalf("reconstructed=%#v err=%v", reconstructed, err)
	}
}

func TestStoreDateBucketsAcrossTwoWorkspacesAtJSTHourBoundaryAndConservesTotals(t *testing.T) {
	runtimeStore := openWorkspaceAggregateTestStore(t, filepath.Join(t.TempDir(), "Workspace-hourly-aggregate.db"))
	object := definitionmodel.ObjectSchema{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "sold_at", Type: "datetime"}, {Key: "amount", Type: "integer"}}}
	if err := appschemapersistence.NewApplicationSchemaStore(runtimeStore).SyncManifest(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test hourly Workspace aggregate schema"), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	workspaces := []aggregatecontract.Workspace{{ID: "workspace-a", CanonicalCode: "north"}, {ID: "workspace-b", CanonicalCode: "south"}}
	for _, workspace := range workspaces {
		insertWorkspaceAggregateCatalogRow(t, runtimeStore, workspace.ID, workspace.CanonicalCode, "active")
	}
	records := recordpersistence.NewRecordStore(runtimeStore)
	for _, row := range []struct {
		workspace string
		id        string
		soldAt    string
		amount    int
	}{
		{"workspace-a", "sale-a-before", "2026-09-06T20:59:59Z", 10},
		{"workspace-a", "sale-a-after", "2026-09-06T21:00:00Z", 20},
		{"workspace-b", "sale-b-before", "2026-09-06T20:59:59Z", 30},
		{"workspace-b", "sale-b-after", "2026-09-06T21:00:00Z", 40},
	} {
		if err := records.InsertRecord(t.Context(), row.workspace, object, recordmodel.Record{ID: row.id, CreatedAt: row.soldAt, UpdatedAt: row.soldAt, Data: map[string]any{"sold_at": row.soldAt, "amount": row.amount}}); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(runtimeStore)
	hourly, err := store.Aggregate(t.Context(), aggregatecontract.Query{
		Workspaces: workspaces, Object: object,
		Dimensions: []runtimeext.CrossWorkspaceAggregateDimension{
			{Key: "store", Field: runtimeext.CrossWorkspaceDimensionWorkspace},
			{Key: "business_hour", Field: "sold_at", Transform: &runtimeext.CrossWorkspaceAggregateDimensionTransform{DateBucket: &runtimeext.CrossWorkspaceAggregateDateBucketTransform{Grain: "hour", TimeZone: "Asia/Tokyo"}}},
		},
		Measures:      []runtimeext.CrossWorkspaceAggregateMeasure{{Key: "total", Operation: runtimeext.AggregateSum, Field: "amount"}},
		RecordQuery:   recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted},
		MaxSourceRows: 10, MaxResultRows: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantHours := map[string]int{
		"north\x002026-09-07T05:00:00+09:00": 10,
		"north\x002026-09-07T06:00:00+09:00": 20,
		"south\x002026-09-07T05:00:00+09:00": 30,
		"south\x002026-09-07T06:00:00+09:00": 40,
	}
	hourlyTotal := 0
	for _, row := range hourly.Rows {
		key := row["store"] + "\x00" + row["business_hour"]
		want, ok := wantHours[key]
		if !ok || row["store"] == "workspace-a" || row["store"] == "workspace-b" {
			t.Fatalf("unexpected physical or bucketed row: %#v", row)
		}
		var got int
		if _, scanErr := fmt.Sscan(row["total"], &got); scanErr != nil || got != want {
			t.Fatalf("row=%#v want total=%d err=%v", row, want, scanErr)
		}
		hourlyTotal += got
	}
	if len(hourly.Rows) != 4 || hourly.SourceRowCount != 4 || hourlyTotal != 100 {
		t.Fatalf("hourly=%#v total=%d", hourly, hourlyTotal)
	}

	unbucketed, err := store.Aggregate(t.Context(), aggregatecontract.Query{
		Workspaces: workspaces, Object: object,
		Dimensions:    []runtimeext.CrossWorkspaceAggregateDimension{{Key: "store", Field: runtimeext.CrossWorkspaceDimensionWorkspace}},
		Measures:      []runtimeext.CrossWorkspaceAggregateMeasure{{Key: "total", Operation: runtimeext.AggregateSum, Field: "amount"}},
		RecordQuery:   recordmodel.RecordListQuery{AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted},
		MaxSourceRows: 10, MaxResultRows: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	unbucketedTotal := 0
	for _, row := range unbucketed.Rows {
		var value int
		if _, scanErr := fmt.Sscan(row["total"], &value); scanErr != nil {
			t.Fatal(scanErr)
		}
		unbucketedTotal += value
	}
	if hourlyTotal != unbucketedTotal {
		t.Fatalf("hourly total=%d unbucketed total=%d", hourlyTotal, unbucketedTotal)
	}
}

func TestCatalogRequiresInstallationScopeAndReportsWorkspaceLimit(t *testing.T) {
	runtimeStore := openWorkspaceAggregateTestStore(t, filepath.Join(t.TempDir(), "catalog.db"))
	store := NewStore(runtimeStore)
	if _, err := store.ListActive(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "wrong authority"), 1); err == nil {
		t.Fatal("Runtime-global scope was accepted")
	}
	insertWorkspaceAggregateCatalogRow(t, runtimeStore, "workspace-a", "a", "active")
	insertWorkspaceAggregateCatalogRow(t, runtimeStore, "workspace-b", "b", "active")
	workspaces, err := store.ListActive(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "bounded catalog"), 1)
	if err != nil || len(workspaces) != 2 {
		t.Fatalf("workspaces=%#v err=%v", workspaces, err)
	}
}

func openWorkspaceAggregateTestStore(t *testing.T, path string) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "Workspace-aggregate-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func insertWorkspaceAggregateCatalogRow(t *testing.T, store *database.RuntimeStore, id, code, status string) {
	t.Helper()
	statement, arguments, err := query.NewInsertBuilder(store.RuntimeRenderer(), "_workspaces").Columns("id", "canonical_code", "name", "status", "created_at", "updated_at").Values(id, code, code, status, "now", "now").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatal(err)
	}
}

func insertWorkspaceAggregateCommercialRow(t *testing.T, store *database.RuntimeStore, workspaceID, plan string, includedUsers, maxUsers int64) {
	t.Helper()
	statement, arguments, err := query.NewInsertBuilder(store.RuntimeRenderer(), "_workspace_commercial_configuration").Columns(
		"workspace_id", "plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit", "included_store_limit", "max_stores", "contract_date", "billing_day",
		"billing_contact_name", "billing_contact_phone", "billing_contact_email", "billing_contact_address", "billing_contact_notes", "revision", "created_at", "updated_at",
	).Values(workspaceID, plan, includedUsers, maxUsers, 0, 100, 1, 2, "2026-09-06", 1, "", "", "", "", "", 1, "now", "now").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatal(err)
	}
}
