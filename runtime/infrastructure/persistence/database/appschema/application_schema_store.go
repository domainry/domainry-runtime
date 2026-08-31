// Metadata persistence.
package appschema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemamysql "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/mysql"
	appschemapostgres "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/postgres"
	appschemasqlite "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/sqlite"
	appschemastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/storage"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

// ApplicationSchemaStore is the request-aware storage boundary for metadata.
type ApplicationSchemaStore struct {
	store                *database.RuntimeStore
	db                   *sql.DB
	schemaDB             runtimeschema.SQLDatabase
	createIndex          func(context.Context, string, string, bool, ...string) error
	storage              appschemastorage.Profile
	exactDecimalMigrator metadataExactDecimalMigrator
	metadata             metadatasdk.Binding
}

type metadataProfileFactory func() (appschemastorage.Profile, metadataExactDecimalMigrator)

var metadataProfileFactories = map[ormdialect.Name]metadataProfileFactory{
	ormdialect.SQLite: func() (appschemastorage.Profile, metadataExactDecimalMigrator) {
		return appschemasqlite.NewApplicationSchemaStorageProfile(), sqliteExactDecimalMigrator{}
	},
	ormdialect.MySQL: func() (appschemastorage.Profile, metadataExactDecimalMigrator) {
		return appschemamysql.NewApplicationSchemaStorageProfile(), mysqlExactDecimalMigrator{}
	},
	ormdialect.Postgres: func() (appschemastorage.Profile, metadataExactDecimalMigrator) {
		return appschemapostgres.NewApplicationSchemaStorageProfile(), postgresExactDecimalMigrator{}
	},
}

var _ appschemarepository.ApplicationSchemaRepository = ApplicationSchemaStore{}

func (r ApplicationSchemaStore) SnapshotRevision(ctx context.Context, scope principalmodel.SystemScope) (string, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return "", err
	}
	executor := database.ActionExecutionExecutor(r.database())
	if actionExecutor := database.ActionExecutionTransaction(ctx); actionExecutor != nil {
		executor = actionExecutor
	}
	var sourceHash, schemaHash string
	query := "SELECT " + r.store.Identifier("source_hash") + ", " + r.store.Identifier("schema_hash") + " FROM " + r.store.TableIdentifier("_application_schema_projection") + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(1)
	err := executor.QueryRowContext(ctx, query, "current").Scan(&sourceHash, &schemaHash)
	if err == sql.ErrNoRows {
		if refreshErr := r.refreshCatalogHashWithExecutor(ctx, executor); refreshErr != nil {
			return "", refreshErr
		}
		err = executor.QueryRowContext(ctx, query, "current").Scan(&sourceHash, &schemaHash)
	}
	if err != nil {
		return "", fmt.Errorf("load metadata snapshot revision: %w", err)
	}
	// source_hash changes when the generated manifest changes (including names
	// and artifact metadata), while schema_hash changes when the effective
	// catalog changes through another authoring source. A watcher must observe
	// both; using schema_hash alone left peer Runtime instances stale after a
	// generated-only publication.
	return strings.TrimSpace(sourceHash) + ":" + strings.TrimSpace(schemaHash), nil
}

func NewApplicationSchemaStore(store *database.RuntimeStore) ApplicationSchemaStore {
	factory := metadataProfileFactories[store.Engine.Name()]
	if factory == nil {
		panic(fmt.Sprintf("unsupported Metadata storage profile %q", store.Engine.Name()))
	}
	profile, migrator := factory()
	return ApplicationSchemaStore{store: store, db: store.DB(), storage: profile, exactDecimalMigrator: migrator, metadata: store.Metadata()}
}

func (r ApplicationSchemaStore) database() *sql.DB {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}

func (r ApplicationSchemaStore) schemaDatabase() runtimeschema.SQLDatabase {
	if r.schemaDB != nil {
		return r.schemaDB
	}
	return r.store.SchemaDB()
}

func (r ApplicationSchemaStore) createIndexIfMissing(ctx context.Context, table, name string, unique bool, columns ...string) error {
	if r.createIndex != nil {
		return r.createIndex(ctx, table, name, unique, columns...)
	}
	return r.store.CreateIndexIfMissing(ctx, table, name, unique, columns...)
}

func recordMutationTxOptions() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func placeholders(store *database.RuntimeStore, count int) []string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return values
}

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	return strings.Join(database.QuotedColumns(store, columns), ", ")
}

func joinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	return stringsJoinIdentifiers(store, columns...)
}

func joinPlaceholders(store *database.RuntimeStore, count int) string {
	return strings.Join(placeholders(store, count), ", ")
}
