package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestConditionalUniqueDDLUsesPartialIndexesAndMySQLNullableGuard(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	policy := recordvalidation.RecordConditionalUniquePolicy{
		Key: "one_active_booking", Fields: []string{"class_id", "member_id"},
		ConditionField: "status", ConditionValues: []string{"booked", "wait'listed"},
	}
	for _, driver := range []string{"sqlite", "postgres"} {
		if err := store.raw.SetEngineForTesting(driver); err != nil {
			t.Fatal(err)
		}
		store.storage = metadataTestStorageProfile(driver)
		plan := store.storage.ConditionalUniquePlan(store.store.SQLRenderer, "class_booking", "uidx_active", policy)
		if plan.GuardColumn != "" || plan.AddGuardStatement != "" || !reflect.DeepEqual(plan.IndexFields, []string{"workspace_id", "class_id", "member_id"}) {
			t.Fatalf("%s spec guard=%q guardSQL=%q fields=%v", driver, plan.GuardColumn, plan.AddGuardStatement, plan.IndexFields)
		}
		for _, fragment := range []string{"CREATE UNIQUE INDEX IF NOT EXISTS", "class_booking", "workspace_id", "class_id", "member_id", "WHERE", "status", "'booked'", "'wait''listed'"} {
			if !strings.Contains(plan.PartialStatement, fragment) {
				t.Fatalf("%s partial DDL missing %q: %s", driver, fragment, plan.PartialStatement)
			}
		}
	}
	if err := store.raw.SetEngineForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	store.storage = metadataTestStorageProfile("mysql")
	plan := store.storage.ConditionalUniquePlan(store.store.SQLRenderer, "class_booking", "uidx_active", policy)
	if plan.GuardColumn == "" || plan.PartialStatement != "" || !reflect.DeepEqual(plan.IndexFields, []string{"workspace_id", "class_id", "member_id", plan.GuardColumn}) {
		t.Fatalf("mysql spec guard=%q partial=%q fields=%v", plan.GuardColumn, plan.PartialStatement, plan.IndexFields)
	}
	for _, fragment := range []string{"ALTER TABLE", "ADD COLUMN", "TINYINT GENERATED ALWAYS AS", "CASE WHEN", "status", "'booked'", "'wait''listed'", "THEN 1 ELSE NULL END", "STORED"} {
		if !strings.Contains(plan.AddGuardStatement, fragment) {
			t.Fatalf("mysql guard DDL missing %q: %s", fragment, plan.AddGuardStatement)
		}
	}
}

func TestConditionalUniqueIndexLifecycleRemovesStaleDatabaseConstraint(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()
	object := definitionmodel.ObjectSchema{
		Key: "class_booking",
		Fields: []definitionmodel.FieldSchema{
			{Key: "class_id", Type: "relation"}, {Key: "member_id", Type: "relation"}, {Key: "status", Type: "select"},
		},
	}
	policy := func(value string) definitionmodel.ValidationSchema {
		return definitionmodel.ValidationSchema{
			Key: "one_active_booking", Type: "conditional_unique", Fields: []string{"class_id", "member_id"},
			Config: map[string]any{"condition_field": "status", "condition_values": []any{value}},
		}
	}
	object.Validations = []definitionmodel.ValidationSchema{policy("booked")}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	bookedPolicy, err := recordvalidation.RecordConditionalUniquePolicies(object)
	if err != nil {
		t.Fatal(err)
	}
	bookedIndex := store.conditionalUniqueIndexName(object.Key, bookedPolicy[0])

	object.Validations = []definitionmodel.ValidationSchema{policy("waitlisted")}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	waitlistedPolicy, err := recordvalidation.RecordConditionalUniquePolicies(object)
	if err != nil {
		t.Fatal(err)
	}
	waitlistedIndex := store.conditionalUniqueIndexName(object.Key, waitlistedPolicy[0])
	indexes, err := store.tableIndexes(t.Context(), object.Key)
	if err != nil || indexes[bookedIndex] || !indexes[waitlistedIndex] {
		t.Fatalf("changed conditional index lifecycle indexes=%v err=%v", indexes, err)
	}

	object.Validations = nil
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	indexes, err = store.tableIndexes(t.Context(), object.Key)
	if err != nil || indexes[waitlistedIndex] {
		t.Fatalf("removed conditional validation left stale index: indexes=%v err=%v", indexes, err)
	}
}

func TestMySQLManagedIndexEvolutionIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
			t.Fatal("RUNTIME_MYSQL_TEST_DSN is required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("parse MySQL integration DSN")
	}
	databaseName := fmt.Sprintf("domainry_opt101_index_%d", time.Now().UnixNano())
	adminConfig := parsed.Clone()
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal("open MySQL integration admin connection")
	}
	if err := admin.PingContext(t.Context()); err != nil {
		_ = admin.Close()
		t.Fatal("connect MySQL integration admin database")
	}
	identifier := "`" + databaseName + "`"
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+identifier+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		_ = admin.Close()
		t.Fatal("create isolated MySQL index-evolution database")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier)
		_ = admin.Close()
	})
	parsed.DBName = databaseName
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "mysql", DatabaseDSN: parsed.FormatDSN(), DatabaseMigrationMode: "apply",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureMetadataSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	metadata := NewMetadataStore(store)
	scope := metadataTestInstallationScope()
	object := definitionmodel.ObjectSchema{Key: "index_evolution", Fields: []definitionmodel.FieldSchema{{Key: "code", Type: "text", Unique: true}}}
	if err := metadata.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatalf("create unique index: %v", err)
	}
	uniqueName := metadata.metadataFieldIndexName(object.Key, "code", true)
	indexes, err := metadata.tableIndexes(t.Context(), object.Key)
	if err != nil || !indexes[uniqueName] {
		t.Fatalf("unique index was not created: indexes=%v err=%v", indexes, err)
	}

	object.Fields[0].Unique = false
	object.Fields[0].Config = map[string]any{"indexed": true}
	if err := metadata.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatalf("replace unique index with regular index through MySQL evolution: %v", err)
	}
	regularName := metadata.metadataFieldIndexName(object.Key, "code", false)
	indexes, err = metadata.tableIndexes(t.Context(), object.Key)
	if err != nil || indexes[uniqueName] || !indexes[regularName] {
		t.Fatalf("unique-to-regular evolution indexes=%v err=%v", indexes, err)
	}

	object.Fields[0].Config = nil
	if err := metadata.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatalf("remove regular index through MySQL evolution: %v", err)
	}
	indexes, err = metadata.tableIndexes(t.Context(), object.Key)
	if err != nil || indexes[regularName] {
		t.Fatalf("regular index removal indexes=%v err=%v", indexes, err)
	}

	booking := definitionmodel.ObjectSchema{
		Key: "conditional_index_evolution",
		Fields: []definitionmodel.FieldSchema{
			{Key: "class_id", Type: "relation"}, {Key: "member_id", Type: "relation"}, {Key: "status", Type: "select"},
		},
	}
	policy := func(value string) definitionmodel.ValidationSchema {
		return definitionmodel.ValidationSchema{
			Key: "one_active_booking", Type: "conditional_unique", Fields: []string{"class_id", "member_id"},
			Config: map[string]any{"condition_field": "status", "condition_values": []any{value}},
		}
	}
	booking.Validations = []definitionmodel.ValidationSchema{policy("booked")}
	if err := metadata.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{booking}}); err != nil {
		t.Fatalf("create conditional unique index: %v", err)
	}
	bookedPolicies, err := recordvalidation.RecordConditionalUniquePolicies(booking)
	if err != nil {
		t.Fatal(err)
	}
	bookedName := metadata.conditionalUniqueIndexName(booking.Key, bookedPolicies[0])

	booking.Validations = []definitionmodel.ValidationSchema{policy("waitlisted")}
	if err := metadata.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{booking}}); err != nil {
		t.Fatalf("replace conditional unique index through MySQL evolution: %v", err)
	}
	waitlistedPolicies, err := recordvalidation.RecordConditionalUniquePolicies(booking)
	if err != nil {
		t.Fatal(err)
	}
	waitlistedName := metadata.conditionalUniqueIndexName(booking.Key, waitlistedPolicies[0])
	indexes, err = metadata.tableIndexes(t.Context(), booking.Key)
	if err != nil || indexes[bookedName] || !indexes[waitlistedName] {
		t.Fatalf("conditional index replacement indexes=%v err=%v", indexes, err)
	}

	booking.Validations = nil
	if err := metadata.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{booking}}); err != nil {
		t.Fatalf("remove conditional unique index through MySQL evolution: %v", err)
	}
	indexes, err = metadata.tableIndexes(t.Context(), booking.Key)
	if err != nil || indexes[waitlistedName] {
		t.Fatalf("conditional index removal indexes=%v err=%v", indexes, err)
	}
}
