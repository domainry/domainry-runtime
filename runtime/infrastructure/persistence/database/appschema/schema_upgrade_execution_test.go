package appschema

import (
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormschema "github.com/domainry/domainry-orm/schema"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	appschemamysql "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/mysql"
	appschemapostgres "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/postgres"
	appschemasqlite "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/sqlite"
	appschemastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/storage"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func upgradeReceiptRows(t *testing.T, store *metadataTestStore, stepKeyPrefix string) map[string]string {
	t.Helper()
	rows, err := store.raw.DB().QueryContext(t.Context(), `SELECT step_key, status FROM _application_schema_upgrade_receipts WHERE step_key LIKE ?`, stepKeyPrefix+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var key, status string
		if err := rows.Scan(&key, &status); err != nil {
			t.Fatal(err)
		}
		result[key] = status
	}
	return result
}

func TestBackfillCompletesOnRerunAfterCrashBetweenAddColumnAndBackfill(t *testing.T) {
	store := openStoreForMetadataTest(t)
	scope := metadataTestInstallationScope()
	v1 := manifestmodel.ManifestSchema{Version: "1", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}}}}
	if err := store.SyncManifest(t.Context(), scope, v1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), `INSERT INTO customer (workspace_id, id, created_at, updated_at, name) VALUES (?, ?, ?, ?, ?)`, "workspace-primary", "customer-1", "now", "now", "Acme"); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash: the column was added by the previous start but the
	// process died before the backfill UPDATE and its receipt.
	if _, err := store.raw.DB().ExecContext(t.Context(), `ALTER TABLE customer ADD COLUMN tier TEXT`); err != nil {
		t.Fatal(err)
	}
	v2 := manifestmodel.ManifestSchema{Version: "2", Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text", Required: true},
		{Key: "tier", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: definitionmodel.FieldUpgradeBackfill, BackfillValue: "standard"}},
		{Key: "level", Type: "integer", DefaultValue: 3},
	}}}}
	plan, err := store.UpgradePlan(t.Context(), scope, &v1, v2)
	if err != nil || plan.Blocking {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	applied, err := store.ApplyUpgrade(t.Context(), scope, plan, v2)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Diagnostics) != 0 {
		t.Fatalf("sqlite file database must back up without diagnostics: %+v", applied.Diagnostics)
	}
	var tier string
	var level int64
	if err := store.raw.DB().QueryRowContext(t.Context(), `SELECT tier, level FROM customer WHERE id = 'customer-1'`).Scan(&tier, &level); err != nil || tier != "standard" || level != 3 {
		t.Fatalf("backfilled tier=%q level=%d err=%v", tier, level, err)
	}
	receipts := upgradeReceiptRows(t, store, "")
	for _, key := range []string{"backfill:customer.tier@2", "add_column:customer.level@2", "backfill:customer.level@2", "add_column:customer.name@1"} {
		if receipts[key] != "completed" {
			t.Fatalf("receipt %s=%q receipts=%v", key, receipts[key], receipts)
		}
	}
	if _, found := receipts["add_column:customer.tier@2"]; found {
		t.Fatalf("column added outside the upgrade must not be receipted: %v", receipts)
	}
	var backupID string
	if err := store.raw.DB().QueryRowContext(t.Context(), `SELECT backup_id FROM _application_schema_upgrade_receipts WHERE step_key = ?`, "backfill:customer.tier@2").Scan(&backupID); err != nil || backupID == "" {
		t.Fatalf("backup id=%q err=%v", backupID, err)
	}
	// A completed backfill is not repeated: a row that later cleared the value
	// keeps NULL on the next start with the same version.
	if _, err := store.raw.DB().ExecContext(t.Context(), `UPDATE customer SET tier = NULL WHERE id = 'customer-1'`); err != nil {
		t.Fatal(err)
	}
	before := len(upgradeReceiptRows(t, store, ""))
	if _, err := store.ApplyUpgrade(t.Context(), scope, appschemamodel.ApplicationSchemaUpgradePlan{FromVersion: "2", ToVersion: "2"}, v2); err != nil {
		t.Fatal(err)
	}
	var nullTier *string
	if err := store.raw.DB().QueryRowContext(t.Context(), `SELECT tier FROM customer WHERE id = 'customer-1'`).Scan(&nullTier); err != nil || nullTier != nil {
		t.Fatalf("completed backfill was repeated: tier=%v err=%v", nullTier, err)
	}
	if after := len(upgradeReceiptRows(t, store, "")); after != before {
		t.Fatalf("receipts grew on an idempotent rerun: before=%d after=%d", before, after)
	}
	if _, err := store.ApplyUpgrade(t.Context(), scope, appschemamodel.ApplicationSchemaUpgradePlan{Blocking: true}, v2); err == nil {
		t.Fatal("blocking plan was applied")
	}
}

func TestStartedReceiptFromCrashIsCompletedByRerun(t *testing.T) {
	store := openStoreForMetadataTest(t)
	scope := metadataTestInstallationScope()
	object := definitionmodel.ObjectSchema{Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "label", Type: "text", Default: "unnamed"}}}
	execution := metadataUpgradeExecution{fromVersion: "1", toVersion: "2", backupID: "backup-1"}
	if err := store.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Version: "1", Objects: []definitionmodel.ObjectSchema{{Key: "asset"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), `ALTER TABLE asset ADD COLUMN label TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.raw.DB().ExecContext(t.Context(), `INSERT INTO asset (workspace_id, id, created_at, updated_at) VALUES (?, ?, ?, ?)`, "workspace-primary", "asset-1", "now", "now"); err != nil {
		t.Fatal(err)
	}
	stepKey := metadataUpgradeStepKey("backfill", "asset", "label", "2")
	if err := store.writeUpgradeReceipt(t.Context(), execution, "asset", "label", stepKey, metadataUpgradeReceiptStarted, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.syncManifestForUpgrade(t.Context(), manifestmodel.ManifestSchema{Version: "2", Objects: []definitionmodel.ObjectSchema{object}}, execution); err != nil {
		t.Fatal(err)
	}
	var label string
	if err := store.raw.DB().QueryRowContext(t.Context(), `SELECT label FROM asset WHERE id = 'asset-1'`).Scan(&label); err != nil || label != "unnamed" {
		t.Fatalf("label=%q err=%v", label, err)
	}
	var status, from, backup string
	var count int
	if err := store.raw.DB().QueryRowContext(t.Context(), `SELECT status, from_version, backup_id FROM _application_schema_upgrade_receipts WHERE step_key = ?`, stepKey).Scan(&status, &from, &backup); err != nil || status != "completed" || from != "1" || backup != "backup-1" {
		t.Fatalf("status=%q from=%q backup=%q err=%v", status, from, backup, err)
	}
	if err := store.raw.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _application_schema_upgrade_receipts WHERE step_key = ?`, stepKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt rows=%d err=%v", count, err)
	}
}

func TestLoadPreviousManifestIsNilWithoutProjection(t *testing.T) {
	store := openStoreForMetadataTest(t)
	scope := metadataTestInstallationScope()
	previous, err := store.LoadPreviousManifest(t.Context(), scope)
	if err != nil || previous != nil {
		t.Fatalf("previous=%v err=%v", previous, err)
	}
	if _, err := store.SnapshotRevision(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	previous, err = store.LoadPreviousManifest(t.Context(), scope)
	if err != nil || previous != nil {
		t.Fatalf("header without definitions previous=%v err=%v", previous, err)
	}
	manifest := manifestmodel.ManifestSchema{SchemaVersion: "manifest-v1", TemplateID: "shop", Version: "7", Name: "Shop", Objects: []definitionmodel.ObjectSchema{{Key: "account", Name: "Account", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}}
	if err := store.SyncManifestProjection(t.Context(), scope, manifest); err != nil {
		t.Fatal(err)
	}
	previous, err = store.LoadPreviousManifest(t.Context(), scope)
	if err != nil || previous == nil || previous.Version != "7" || len(previous.Objects) != 1 || len(previous.Objects[0].Fields) != 1 {
		t.Fatalf("previous=%+v err=%v", previous, err)
	}
}

// TestMetadataAddColumnTypeDivergesFromORM documents why ensureObjectStorage
// keeps a raw ADD COLUMN statement: the dialect storage profiles render
// business column types that ormschema cannot express byte-identically.
func TestMetadataAddColumnTypeDivergesFromORM(t *testing.T) {
	currency := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}}
	indexedText := definitionmodel.FieldSchema{Key: "code", Type: "text", Config: map[string]any{"indexed": true}}
	for _, test := range []struct {
		name     string
		profile  appschemastorage.Profile
		renderer ormdialect.Renderer
		field    definitionmodel.FieldSchema
		ormType  ormschema.ColumnType
	}{
		{"postgres currency", appschemapostgres.NewApplicationSchemaStorageProfile(), postgres.NewEngine().SQLDialect().WithSchema(""), currency, ormschema.Decimal(19, 2)},
		{"mysql indexed text", appschemamysql.NewApplicationSchemaStorageProfile(), mysql.NewEngine().SQLDialect().WithSchema(""), indexedText, ormschema.Text()},
		{"sqlite currency", appschemasqlite.NewApplicationSchemaStorageProfile(), sqlite.NewEngine().SQLDialect().WithSchema(""), currency, ormschema.Decimal(19, 2)},
	} {
		t.Run(test.name, func(t *testing.T) {
			profileType := test.profile.FieldColumnType(test.field, metadataFieldIndexed(test.field))
			statement, _, err := ormschema.NewAddColumn(test.renderer, "order", ormschema.Column(test.field.Key, test.ormType)).Build()
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(statement, " "+profileType) {
				t.Fatalf("ORM add column %q renders the profile type %q; the raw statement can be replaced", statement, profileType)
			}
		})
	}
}
