package record_test

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	"os"
	"path/filepath"
	"testing"

	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func recordStore(store *RuntimeStore) recordpersistence.RecordStore {
	return recordpersistence.NewRecordStore(store)
}

func TestListRecordsComposesDepartmentScopeWithSearchFiltersPaginationAndSorting(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()

	table := store.Identifier("scoped_record")
	createSQL := "CREATE TABLE " + table + " (" +
		store.Identifier("workspace_id") + " TEXT NOT NULL, " +
		store.Identifier("id") + " TEXT PRIMARY KEY, " +
		store.Identifier("created_at") + " TEXT NOT NULL, " +
		store.Identifier("updated_at") + " TEXT NOT NULL, " +
		store.Identifier("name") + " TEXT NOT NULL, " +
		store.Identifier("status") + " TEXT NOT NULL, " +
		store.Identifier("owner_department_path") + " TEXT" +
		")"
	if _, err := store.DB().Exec(createSQL); err != nil {
		t.Fatalf("create scoped table: %v", err)
	}

	object := definitionmodel.ObjectSchema{
		Key:  "scoped_record",
		Name: "Scoped Record",
		Fields: []definitionmodel.FieldSchema{
			{Key: "name", Name: "Name", Type: "text"},
			{Key: "status", Name: "Status", Type: "select"},
			{Key: "owner_department_path", Name: "Owner Department Path", Type: "text"},
		},
	}
	insertGeneratedScopedRecord(t, store, object, "r1", "Alpha North", "active", "/company/sales")
	insertGeneratedScopedRecord(t, store, object, "r2", "Beta North", "active", "/company/sales/enterprise")
	insertGeneratedScopedRecord(t, store, object, "r3", "Gamma North", "active", "/company/support")
	insertGeneratedScopedRecord(t, store, object, "r4", "Zeta North", "inactive", "/company/sales")
	insertGeneratedScopedRecord(t, store, object, "r5", "Outside North", "active", "/company/sales-east")
	insertGeneratedScopedRecord(t, store, object, "r6", "Aardvark South", "active", "/company/sales")
	insertGeneratedScopedRecordWithDepartmentPath(t, store, object, "r10", "Null North", "active", nil)

	page, err := recordStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{
		Page:                    2,
		PageSize:                1,
		Search:                  "North",
		SearchFields:            []string{"name"},
		Filters:                 map[string]any{"status": "active"},
		Sort:                    []recordmodel.RecordSortRule{{Field: "name", Direction: "asc"}},
		Scope:                   "department_and_children",
		PrincipalDepartmentPath: "/company/sales",
		DepartmentPathField:     "owner_department_path",
	})
	if err != nil {
		t.Fatalf("list scoped records: %v", err)
	}
	if page.Page != 2 || page.PageSize != 1 || page.Total != 2 || page.HasNext {
		t.Fatalf("expected second page of two scoped matches, got %#v", page)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "r2" {
		t.Fatalf("expected sorted second scoped record r2, got %#v", page.Items)
	}

	insertGeneratedScopedRecord(t, store, object, "r7", "Special North", "active", "/company/sales_%")
	insertGeneratedScopedRecord(t, store, object, "r8", "Special Child North", "active", "/company/sales_%/enterprise")
	insertGeneratedScopedRecord(t, store, object, "r9", "Special Leak North", "active", "/company/sales-aa")
	specialPage, err := recordStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{
		Page:                    1,
		PageSize:                10,
		Search:                  "Special",
		SearchFields:            []string{"name"},
		Filters:                 map[string]any{"status": "active"},
		Sort:                    []recordmodel.RecordSortRule{{Field: "name", Direction: "asc"}},
		Scope:                   "department_and_children",
		PrincipalDepartmentPath: "/company/sales_%",
		DepartmentPathField:     "owner_department_path",
	})
	if err != nil {
		t.Fatalf("list special-character scoped records: %v", err)
	}
	if specialPage.Total != 2 || len(specialPage.Items) != 2 || specialPage.Items[0].ID != "r8" || specialPage.Items[1].ID != "r7" {
		t.Fatalf("expected escaped LIKE query to include only exact and child special-character paths, got %#v", specialPage)
	}

	batchPage, err := recordStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{
		Page: 1, PageSize: 10, Filters: map[string]any{"id__in": []any{"r1", "r3"}}, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}},
	})
	if err != nil {
		t.Fatalf("list records by batched IDs: %v", err)
	}
	if batchPage.Total != 2 || len(batchPage.Items) != 2 || batchPage.Items[0].ID != "r1" || batchPage.Items[1].ID != "r3" {
		t.Fatalf("expected generic id__in batch filter, got %#v", batchPage)
	}
	firstWithoutTotal, err := recordStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{
		Page: 1, PageSize: 2, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}, SkipTotal: true,
	})
	if err != nil || firstWithoutTotal.Total != 0 || !firstWithoutTotal.HasNext || len(firstWithoutTotal.Items) != 2 {
		t.Fatalf("expected count-free first page with lookahead, got %#v err=%v", firstWithoutTotal, err)
	}
	cursorPage, err := recordStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{
		Page: 99, PageSize: 2, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}, SkipTotal: true, AfterID: firstWithoutTotal.Items[len(firstWithoutTotal.Items)-1].ID,
	})
	if err != nil || len(cursorPage.Items) != 2 || cursorPage.Items[0].ID <= firstWithoutTotal.Items[1].ID {
		t.Fatalf("expected keyset page strictly after cursor, got %#v err=%v", cursorPage, err)
	}
	if _, err := recordStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{
		Page: 1, PageSize: 2, Sort: []recordmodel.RecordSortRule{{Field: "name", Direction: "asc"}}, SkipTotal: true, AfterID: "r1",
	}); err == nil {
		t.Fatal("expected non-id keyset sort to be rejected")
	}
	lastWithoutTotal, err := recordStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{
		Page: 5, PageSize: 2, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}, SkipTotal: true,
	})
	if err != nil || lastWithoutTotal.Total != 0 || lastWithoutTotal.HasNext || len(lastWithoutTotal.Items) != 2 {
		t.Fatalf("expected count-free final page, got %#v err=%v", lastWithoutTotal, err)
	}
}

func TestUpdateRecordWherePreventsStaleSchedulerLeaseClaim(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()

	if _, err := store.DB().Exec("CREATE TABLE " + store.Identifier("job_run") + " (" +
		store.Identifier("workspace_id") + " TEXT NOT NULL, " +
		store.Identifier("id") + " TEXT PRIMARY KEY, " +
		store.Identifier("created_at") + " TEXT NOT NULL, " +
		store.Identifier("updated_at") + " TEXT NOT NULL, " +
		store.Identifier("status") + " TEXT NOT NULL, " +
		store.Identifier("lease_owner") + " TEXT, " +
		store.Identifier("lease_expires_at") + " TEXT, " +
		store.Identifier("attempt") + " REAL" +
		")"); err != nil {
		t.Fatalf("create job_run table: %v", err)
	}
	object := definitionmodel.ObjectSchema{
		Key:  "job_run",
		Name: "Job Run",
		Fields: []definitionmodel.FieldSchema{
			{Key: "status", Name: "Status", Type: "text"},
			{Key: "lease_owner", Name: "Lease Owner", Type: "text"},
			{Key: "lease_expires_at", Name: "Lease Expires At", Type: "datetime"},
			{Key: "attempt", Name: "Attempt", Type: "number"},
		},
	}
	record := recordmodel.Record{
		ID:        "run_1",
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-01T00:00:00Z",
		Data: map[string]any{
			"status":           "retrying",
			"lease_owner":      "",
			"lease_expires_at": "",
			"attempt":          1,
		},
	}
	if err := recordStore(store).InsertRecord(t.Context(), "default", object, record); err != nil {
		t.Fatalf("insert job_run: %v", err)
	}

	claimed := record
	claimed.UpdatedAt = "2026-01-01T00:00:01Z"
	claimed.Data["status"] = "leased"
	claimed.Data["lease_owner"] = "worker:a"
	claimed.Data["lease_expires_at"] = "2026-01-01T00:05:01Z"
	ok, err := recordStore(store).UpdateRecordWhere(t.Context(), "default", object, claimed, map[string]any{"status": "retrying", "lease_expires_at": ""})
	if err != nil {
		t.Fatalf("first conditional update: %v", err)
	}
	if !ok {
		t.Fatalf("expected first scheduler lease claim to succeed")
	}

	stale := record
	stale.UpdatedAt = "2026-01-01T00:00:02Z"
	stale.Data["status"] = "leased"
	stale.Data["lease_owner"] = "worker:b"
	stale.Data["lease_expires_at"] = "2026-01-01T00:05:02Z"
	ok, err = recordStore(store).UpdateRecordWhere(t.Context(), "default", object, stale, map[string]any{"status": "retrying", "lease_expires_at": ""})
	if err != nil {
		t.Fatalf("second conditional update: %v", err)
	}
	if ok {
		t.Fatalf("expected stale scheduler lease claim to be rejected")
	}
}

func TestMigrationStatusAndPing(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()

	if err := store.DB().PingContext(t.Context()); err != nil {
		t.Fatalf("ping store: %v", err)
	}
	status, err := store.MigrationStatus(t.Context())
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	if !status.Current || status.Expected != 1 || status.Applied != 1 || status.Pending != 0 {
		t.Fatalf("expected current migration status, got %#v", status)
	}
	if len(status.ExpectedPaths) != 1 || status.ExpectedPaths[0] != "001_empty.sql" {
		t.Fatalf("expected migration path 001_empty.sql, got %#v", status.ExpectedPaths)
	}
	if len(status.AppliedPaths) != 1 || status.AppliedPaths[0] != "001_empty.sql" {
		t.Fatalf("expected applied migration path 001_empty.sql, got %#v", status.AppliedPaths)
	}
	if status.LastAppliedAt == "" {
		t.Fatalf("expected last applied timestamp, got %#v", status)
	}
}

func TestPendingMigrationCreatesSQLiteBackupWhenDataExists(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "app.db")
	migrationDir := filepath.Join(tempDir, "migrations")
	backupDir := filepath.Join(tempDir, "backups")
	if err := os.MkdirAll(migrationDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations: %v", err)
	}
	firstMigration := filepath.Join(migrationDir, "001_init.sql")
	if err := os.WriteFile(firstMigration, []byte(`
CREATE TABLE IF NOT EXISTS "customer" (
  "id" TEXT PRIMARY KEY,
  "created_at" TEXT NOT NULL,
  "updated_at" TEXT NOT NULL,
  "name" TEXT NOT NULL
);
INSERT INTO "customer" ("id", "created_at", "updated_at", "name") VALUES ('c1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'Ada');
`), 0644); err != nil {
		t.Fatalf("write first migration: %v", err)
	}
	store, err := OpenContext(t.Context(), config.Config{
		DatabaseDriver:     "sqlite",
		DBPath:             dbPath,
		MigrationDir:       migrationDir,
		MigrationBackupDir: backupDir,
	})
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationDir, "002_add_phone.sql"), []byte(`ALTER TABLE "customer" ADD COLUMN "phone" TEXT;`), 0644); err != nil {
		t.Fatalf("write second migration: %v", err)
	}
	upgraded, err := OpenContext(t.Context(), config.Config{
		DatabaseDriver:     "sqlite",
		DBPath:             dbPath,
		MigrationDir:       migrationDir,
		MigrationBackupDir: backupDir,
	})
	if err != nil {
		t.Fatalf("open upgraded store: %v", err)
	}
	defer upgraded.Close()
	backups, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected one sqlite migration backup, got %#v", backups)
	}
	status, err := upgraded.MigrationStatus(t.Context())
	if err != nil {
		t.Fatalf("migration status: %v", err)
	}
	if !status.Current || status.Applied != 2 {
		t.Fatalf("expected two applied migrations after backup, got %#v", status)
	}
}

func openStoreForGeneratedListTest(t *testing.T) *RuntimeStore {
	t.Helper()
	tempDir := t.TempDir()
	migrationPath := filepath.Join(tempDir, "001_empty.sql")
	if err := os.WriteFile(migrationPath, []byte("-- generated storage test migration\n"), 0644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	store, err := OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(tempDir, "app.db"),
		MigrationSQL:   migrationPath,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

func insertGeneratedScopedRecord(t *testing.T, store *RuntimeStore, object definitionmodel.ObjectSchema, id, name, status, departmentPath string) {
	t.Helper()
	insertGeneratedScopedRecordWithDepartmentPath(t, store, object, id, name, status, departmentPath)
}

func insertGeneratedScopedRecordWithDepartmentPath(t *testing.T, store *RuntimeStore, object definitionmodel.ObjectSchema, id, name, status string, departmentPath any) {
	t.Helper()
	if err := recordStore(store).InsertRecord(t.Context(), "default", object, recordmodel.Record{
		ID:        id,
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-01T00:00:00Z",
		Data: map[string]any{
			"name":                  name,
			"status":                status,
			"owner_department_path": departmentPath,
		},
	}); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}
