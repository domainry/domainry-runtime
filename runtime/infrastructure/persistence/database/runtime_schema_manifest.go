package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

// runtimeSchemaDDL returns the exact host-rendered DDL that defines a fresh
// Runtime schema. The resulting statements are the schema identity: changing
// a table, column, index, constraint, or selected capability changes the hash.
func runtimeSchemaDDL(ctx context.Context, runtime *RuntimeStore, capabilities RuntimeSchemaCapabilities) ([]string, error) {
	if runtime == nil || runtime.engine == nil {
		return nil, fmt.Errorf("runtime schema DDL requires a database engine")
	}
	recorder := &runtimeSchemaDDLRecorder{}
	store := &runtimeSchemaManifestStore{runtime: runtime, recorder: recorder}
	if runtime.sqlBase().RuntimeEngine.ManagedDatabaseMarkerEnabled() {
		statement, err := runtimeschema.ManagedDatabaseCohortCreateStatement(store.RuntimeRenderer())
		if err != nil {
			return nil, err
		}
		if _, err := recorder.ExecContext(ctx, statement); err != nil {
			return nil, err
		}
	}
	steps := []func() error{
		func() error { return runtimeschema.EnsureWorkspaceProvisioningSchema(ctx, store) },
		func() error { return runtimeschema.EnsureApplicationSchemaFor(ctx, store, capabilities.Lifecycle) },
		func() error {
			return runtimeschema.EnsureEvidenceSchemaFor(ctx, store, runtimeschema.EvidenceSchemaCapabilities{
				Workflow: capabilities.Workflow, Automation: capabilities.Automation, Lifecycle: capabilities.Lifecycle, ReleaseCoordination: capabilities.ReleaseCoordination,
			})
		},
		func() error {
			if !capabilities.Workflow {
				return nil
			}
			return runtimeschema.EnsureWorkflowProcessSchema(ctx, store)
		},
		func() error { return runtimeschema.EnsureRateLimitSchema(ctx, store) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, fmt.Errorf("render runtime schema DDL: %w", err)
		}
	}
	if len(recorder.statements) == 0 {
		return nil, fmt.Errorf("runtime schema DDL is empty")
	}
	return append([]string(nil), recorder.statements...), nil
}

func runtimeSchemaChecksum(statements []string) string {
	hash := sha256.New()
	for _, statement := range statements {
		statement = strings.TrimSpace(statement)
		_, _ = fmt.Fprintf(hash, "%d\x00%s\x00", len(statement), statement)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func runtimeSchemaMigrationPath(checksum string) string {
	return "runtime_schema_sha256_" + strings.TrimSpace(checksum)
}

type runtimeSchemaDDLRecorder struct{ statements []string }

func (r *runtimeSchemaDDLRecorder) ExecContext(_ context.Context, statement string, arguments ...any) (sql.Result, error) {
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return nil, fmt.Errorf("runtime schema DDL contains an empty statement")
	}
	if len(arguments) != 0 {
		return nil, fmt.Errorf("runtime schema DDL contains bound values")
	}
	r.statements = append(r.statements, statement)
	return runtimeSchemaDDLResult(1), nil
}

func (*runtimeSchemaDDLRecorder) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("runtime schema DDL rendering cannot query a database")
}

func (*runtimeSchemaDDLRecorder) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func (*runtimeSchemaDDLRecorder) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, fmt.Errorf("runtime schema DDL rendering cannot begin a transaction")
}

type runtimeSchemaDDLResult int64

func (r runtimeSchemaDDLResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r runtimeSchemaDDLResult) RowsAffected() (int64, error) { return int64(r), nil }

type runtimeSchemaManifestStore struct {
	runtime  *RuntimeStore
	recorder *runtimeSchemaDDLRecorder
}

func (s *runtimeSchemaManifestStore) SchemaDB() runtimeschema.SQLDatabase { return s.recorder }
func (s *runtimeSchemaManifestStore) Driver() string                      { return s.runtime.Driver() }
func (s *runtimeSchemaManifestStore) DatabaseSchema() string              { return s.runtime.DatabaseSchema() }
func (s *runtimeSchemaManifestStore) Identifier(value string) string {
	return s.runtime.Identifier(value)
}
func (s *runtimeSchemaManifestStore) TableIdentifier(value string) string {
	return s.runtime.TableIdentifier(value)
}
func (s *runtimeSchemaManifestStore) Placeholder(position int) string {
	return s.runtime.Placeholder(position)
}
func (s *runtimeSchemaManifestStore) RuntimeProfile() persistencedriver.EngineProfile {
	return s.runtime.RuntimeProfile()
}
func (s *runtimeSchemaManifestStore) RuntimeRenderer() ormdialect.Renderer {
	return s.runtime.RuntimeRenderer()
}
func (*runtimeSchemaManifestStore) RuntimeTableExists(context.Context, string) (bool, error) {
	return false, nil
}
func (s *runtimeSchemaManifestStore) ApplicationSchemaIDColumnType() string {
	return s.runtime.ApplicationSchemaIDColumnType()
}
func (s *runtimeSchemaManifestStore) LocalizedTextKeyColumnType() string {
	return s.runtime.LocalizedTextKeyColumnType()
}
func (s *runtimeSchemaManifestStore) RuntimeColumnDefinition(value string) string {
	return s.runtime.RuntimeColumnDefinition(value)
}
func (s *runtimeSchemaManifestStore) CreateIndexIfMissing(ctx context.Context, table, index string, unique bool, columns ...string) error {
	return runtimeschema.CreateIndex(ctx, s, table, index, unique, columns...)
}

var _ runtimeschema.Store = (*runtimeSchemaManifestStore)(nil)
