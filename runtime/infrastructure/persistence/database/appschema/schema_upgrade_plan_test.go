package appschema

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	appschemamysql "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
)

func upgradePlanStepsByOperation(plan appschemamodel.ApplicationSchemaUpgradePlan) map[string]appschemamodel.ApplicationSchemaUpgradeStep {
	result := map[string]appschemamodel.ApplicationSchemaUpgradeStep{}
	for _, step := range plan.Steps {
		key := step.Operation + ":" + step.ObjectKey
		if step.ColumnKey != "" {
			key += "." + step.ColumnKey
		}
		result[key] = step
	}
	return result
}

func TestUpgradePlanClassifiesEveryStepKind(t *testing.T) {
	store := openStoreForMetadataTest(t)
	scope := metadataTestInstallationScope()
	v1 := manifestmodel.ManifestSchema{Version: "1", Objects: []definitionmodel.ObjectSchema{
		{Key: "customer", Fields: []definitionmodel.FieldSchema{
			{Key: "name", Type: "text", Required: true},
			{Key: "code", Type: "text"},
			{Key: "legacy", Type: "text"},
			{Key: "level", Type: "text"},
		}},
		{Key: "archive", Fields: []definitionmodel.FieldSchema{{Key: "label", Type: "text"}}},
	}}
	fresh, err := store.UpgradePlan(t.Context(), scope, nil, v1)
	if err != nil || fresh.FromVersion != "" || fresh.ToVersion != "1" || fresh.Blocking || len(fresh.Steps) != 2 || fresh.ContractVersion != appschemamodel.ApplicationSchemaUpgradePlanContractVersion {
		t.Fatalf("fresh plan=%+v err=%v", fresh, err)
	}
	for _, step := range fresh.Steps {
		if step.Operation != "create_table" || step.Classification != appschemamodel.ApplicationSchemaUpgradeCompatible {
			t.Fatalf("fresh step=%+v", step)
		}
	}
	if err := store.SyncManifest(t.Context(), scope, v1); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{{"customer-1", "Acme", "dup", "x"}, {"customer-2", "Beta", "dup", "y"}, {"customer-3", "Gamma", nil, "z"}} {
		if _, err := store.raw.DB().ExecContext(t.Context(), `INSERT INTO customer (workspace_id, id, created_at, updated_at, name, code, level) VALUES (?, ?, ?, ?, ?, ?, ?)`, "workspace-primary", row[0], "now", "now", row[1], row[2], row[3]); err != nil {
			t.Fatal(err)
		}
	}
	v2 := manifestmodel.ManifestSchema{Version: "2", Objects: []definitionmodel.ObjectSchema{
		{Key: "customer", Fields: []definitionmodel.FieldSchema{
			{Key: "name", Type: "text", Required: true},
			{Key: "code", Type: "text", Unique: true},
			{Key: "legacy", Type: "text", DisabledAt: "2026-09-01T00:00:00Z"},
			{Key: "level", Type: "integer"},
			{Key: "tier", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill", BackfillValue: "standard"}},
			{Key: "region", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "exempt"}},
			{Key: "segment", Type: "text", Required: true},
			{Key: "note", Type: "text"},
			{Key: "email", Type: "text", Unique: true},
		}, Validations: []definitionmodel.ValidationSchema{{Type: "composite_unique", Fields: []string{"name", "level"}}}},
		{Key: "invoice", Fields: []definitionmodel.FieldSchema{{Key: "number", Type: "text", Required: true}}},
	}}
	plan, err := store.UpgradePlan(t.Context(), scope, &v1, v2)
	if err != nil {
		t.Fatal(err)
	}
	if plan.FromVersion != "1" || plan.ToVersion != "2" || !plan.Blocking {
		t.Fatalf("plan header=%+v", plan)
	}
	steps := upgradePlanStepsByOperation(plan)
	expect := func(key, classification string, blocking bool, code string) appschemamodel.ApplicationSchemaUpgradeStep {
		t.Helper()
		step, ok := steps[key]
		if !ok || step.Classification != classification || step.Blocking != blocking || step.ErrorCode != code {
			t.Fatalf("step %s=%+v (found=%v) want classification=%s blocking=%v code=%s", key, step, ok, classification, blocking, code)
		}
		return step
	}
	expect("add_column:customer.tier", appschemamodel.ApplicationSchemaUpgradeCompatible, false, "")
	expect("add_column:customer.region", appschemamodel.ApplicationSchemaUpgradeCompatible, false, "")
	expect("add_column:customer.note", appschemamodel.ApplicationSchemaUpgradeCompatible, false, "")
	expect("add_column:customer.email", appschemamodel.ApplicationSchemaUpgradeCompatible, false, "")
	expect("create_table:invoice", appschemamodel.ApplicationSchemaUpgradeCompatible, false, "")
	segment := expect("add_column:customer.segment", appschemamodel.ApplicationSchemaUpgradeRequiresRule, true, appschemamodel.ApplicationSchemaUpgradeRequiredFieldRuleMissingCode)
	if segment.ExistingRows != 3 || segment.Params["existing_rows"] != "3" || segment.Params["object"] != "customer" || segment.Params["field"] != "segment" {
		t.Fatalf("requires_rule step=%+v", segment)
	}
	code := expect("create_unique_index:customer.code", appschemamodel.ApplicationSchemaUpgradeDataDependent, true, appschemamodel.ApplicationSchemaUpgradeUniqueConflictCode)
	if code.Params["duplicates"] != "1" || code.Params["fields"] != "code" {
		t.Fatalf("unique conflict step=%+v", code)
	}
	composite := expect("create_unique_index:customer.name,level", appschemamodel.ApplicationSchemaUpgradeDataDependent, false, "")
	if composite.Params["duplicates"] != "0" {
		t.Fatalf("composite unique step=%+v", composite)
	}
	if _, found := steps["create_unique_index:customer.email"]; found {
		t.Fatalf("unique index on a new column must not be probed: %+v", steps)
	}
	level := expect("change_field_type:customer.level", appschemamodel.ApplicationSchemaUpgradeIncompatible, true, appschemamodel.ApplicationSchemaUpgradeFieldTypeChangeCode)
	if level.Params["object"] != "customer" || level.Params["field"] != "level" || level.Params["from_type"] != "text" || level.Params["to_type"] != "integer" {
		t.Fatalf("incompatible step=%+v", level)
	}
	legacy := expect("retain_column:customer.legacy", appschemamodel.ApplicationSchemaUpgradeRetained, false, "")
	if legacy.Description != appschemamodel.ApplicationSchemaRetainedMigrationDescription {
		t.Fatalf("retained column=%+v", legacy)
	}
	expect("retain_table:archive", appschemamodel.ApplicationSchemaUpgradeRetained, false, "")
	if got := len(plan.PendingSteps()); got != len(plan.Steps)-2 {
		t.Fatalf("pending steps=%d total=%d", got, len(plan.Steps))
	}
	if got := len(plan.BlockingSteps()); got != 3 {
		t.Fatalf("blocking steps=%d", got)
	}

	// Once the populated table is emptied the missing rule no longer blocks
	// and the duplicate probe is clean.
	if _, err := store.raw.DB().ExecContext(t.Context(), `DELETE FROM customer`); err != nil {
		t.Fatal(err)
	}
	v2.Objects[0].Fields[3] = definitionmodel.FieldSchema{Key: "level", Type: "text"}
	plan, err = store.UpgradePlan(t.Context(), scope, &v1, v2)
	if err != nil || plan.Blocking {
		t.Fatalf("empty table plan=%+v err=%v", plan, err)
	}
	if steps = upgradePlanStepsByOperation(plan); steps["add_column:customer.segment"].Classification != appschemamodel.ApplicationSchemaUpgradeCompatible || steps["create_unique_index:customer.code"].Params["duplicates"] != "0" {
		t.Fatalf("empty table steps=%+v", steps)
	}
}

func TestFieldTypeChangeIsBlockedEvenWhenPhysicalTypesMatch(t *testing.T) {
	previous := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "item", Fields: []definitionmodel.FieldSchema{{Key: "payload", Type: "text"}}}}}
	next := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "item", Fields: []definitionmodel.FieldSchema{{Key: "payload", Type: "json"}}}}}
	steps, changed := fieldTypeChangeUpgradeSteps(previous, next)
	if len(steps) != 1 || !steps[0].Blocking || steps[0].Operation != "change_field_type" || steps[0].ErrorCode != appschemamodel.ApplicationSchemaUpgradeFieldTypeChangeCode || !changed["item\x00payload"] {
		t.Fatalf("steps=%#v changed=%#v", steps, changed)
	}
}

func TestUpgradePlanRequiresInstallationScopeAndHonorsContext(t *testing.T) {
	store := openStoreForMetadataTest(t)
	if _, err := store.UpgradePlan(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeBootstrap, "bootstrap scope"), nil, manifestmodel.ManifestSchema{}); err == nil {
		t.Fatal("workspace scope accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.UpgradePlan(ctx, metadataTestInstallationScope(), nil, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled plan err=%v", err)
	}
}

func TestMySQLPhysicalSchemaLoadsAllTablesInTwoQueries(t *testing.T) {
	state := &metadataSQLState{querySteps: []metadataSQLQueryStep{
		{
			columns: []string{"table_name", "column_name", "data_type", "numeric_precision", "numeric_scale"},
			rows: [][]driver.Value{
				{"account", "id", "varchar", nil, nil},
				{"account", "balance", "decimal", int64(19), int64(2)},
				{"invoice", "id", "varchar", nil, nil},
			},
		},
		{
			columns: []string{"table_name", "index_name"},
			rows:    [][]driver.Value{{"account", "PRIMARY"}, {"account", "idx_account_balance"}, {"invoice", "PRIMARY"}},
		},
	}}
	db := openMetadataScriptedDB(state)
	defer db.Close()
	profile := appschemamysql.NewApplicationSchemaStorageProfile()
	snapshot, err := profile.PhysicalSchema(t.Context(), db, mysql.NewEngine().SQLDialect().WithSchema(""), "", []string{"account", "invoice", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.queryLog) != 2 {
		t.Fatalf("queries=%d log=%v", len(state.queryLog), state.queryLog)
	}
	for _, statement := range state.queryLog {
		if !strings.Contains(statement, "table_schema = DATABASE()") || !strings.Contains(statement, "table_name IN (?, ?, ?)") {
			t.Fatalf("query did not batch all tables: %s", statement)
		}
	}
	if got := snapshot.ColumnsByTable["account"]["balance"]; got != "decimal(19,2)" {
		t.Fatalf("account.balance type=%q snapshot=%v", got, snapshot.ColumnsByTable)
	}
	if !snapshot.IndexesByTable["account"]["idx_account_balance"] || len(snapshot.IndexesByTable["missing"]) != 0 {
		t.Fatalf("indexes=%v", snapshot.IndexesByTable)
	}
	if _, found := snapshot.ColumnsByTable["missing"]; found {
		t.Fatalf("missing table was reported as existing: %v", snapshot.ColumnsByTable)
	}
}
