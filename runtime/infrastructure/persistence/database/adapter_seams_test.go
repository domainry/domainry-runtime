package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

func TestAdapterSeamsDelegateAndNormalize(t *testing.T) {
	store := openMigrationEdgeStore(t)
	where, args, err := store.TenantListWhereClause("workspace", recordmodel.RecordListQuery{Filters: map[string]any{"status": "open"}})
	if err != nil || !strings.Contains(where, "workspace_id") || len(args) != 2 {
		t.Fatalf("where=%q args=%#v err=%v", where, args, err)
	}
	if order := store.ListOrderClause(recordmodel.RecordListQuery{}); order == "" {
		t.Fatal("missing default list order")
	}
	constraint := MutationConstraintError(errors.New("UNIQUE constraint failed"), "record", "id", mutation.MutationConflictUnique)
	if !mutation.IsMutationConflict(constraint, mutation.MutationConflictUnique) {
		t.Fatalf("constraint=%v", constraint)
	}
	transaction := MutationTransactionError(errors.New("database is locked"), "record", "id")
	if !mutation.IsTransactionTransient(transaction, "") {
		t.Fatalf("transaction=%v", transaction)
	}
	if NullableText(" \t") != nil || NullableText(" value ") != " value " || BoolInt(true) != 1 || BoolInt(false) != 0 {
		t.Fatal("nullable/bool normalization failed")
	}
	if got := store.InsertStatement("probe", []string{"id", "name"}); !strings.Contains(got, "INSERT INTO") {
		t.Fatalf("insert=%q", got)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE adapter_probe (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertSystemRowContext(t.Context(), "adapter_probe", []string{"id", "name"}, []any{"id", "name"}); err != nil {
		t.Fatal(err)
	}
	quoted := QuotedColumns(store, []string{"id", "name"})
	if len(quoted) != 2 || quoted[0] != `"id"` {
		t.Fatalf("quoted=%#v", quoted)
	}
	store.secretMaterialKey[0] = 7
	if store.SecretMaterialKey()[0] != 7 || store.SecretKeyProvider() != nil {
		t.Fatal("secret seams changed values")
	}
	if store.MetadataIDColumnType() != "TEXT" || store.LocalizedTextKeyColumnType() != "TEXT" || store.RuntimeColumnDefinition("TEXT") != "TEXT" {
		t.Fatal("sqlite type seams failed")
	}
	if err := store.SetDialectForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	if store.LocalizedTextKeyColumnType() != "VARCHAR(128)" {
		t.Fatal("mysql localized key type failed")
	}
	if NonNilMap(nil) == nil || NonNilMap(map[string]any{"x": 1})["x"] != 1 {
		t.Fatal("map normalization failed")
	}
	if options := recordMutationTxOptions(); options.Isolation != sql.LevelSerializable {
		t.Fatalf("options=%#v", options)
	}
}

func TestAdapterMigrationSeams(t *testing.T) {
	if err := ValidateExternalMigrationBackup("postgres", ""); err == nil {
		t.Fatal("missing backup evidence accepted")
	}
	explicit := filepath.Join(t.TempDir(), "explicit.sql")
	paths, err := MigrationPathsForDialect(config.Config{MigrationSQL: explicit}, sqlite.Dialect{})
	if err != nil || len(paths) != 1 || paths[0] != explicit {
		t.Fatalf("paths=%#v err=%v", paths, err)
	}
	store := openMigrationEdgeStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.CreateSQLiteMigrationBackup(ctx, config.Config{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
