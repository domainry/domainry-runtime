package agent

import (
	"context"
	"fmt"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

// AgentSchemaMigration is the sole owner of Agent persistence DDL. Runtime
// startup executes it before repositories are exposed to application code.
type AgentSchemaMigration struct {
	store *database.RuntimeStore
}

func NewAgentSchemaMigration(store *database.RuntimeStore) *AgentSchemaMigration {
	if store == nil {
		return &AgentSchemaMigration{}
	}
	return &AgentSchemaMigration{store: store}
}

func (m *AgentSchemaMigration) EnsureSchema(ctx context.Context) error {
	if m == nil || m.store == nil || m.store.SchemaDB() == nil {
		return fmt.Errorf("agent schema migration unavailable")
	}
	return runtimeschema.EnsureAgentSchema(ctx, m.store)
}

func migrateAgentStateSchema(ctx context.Context, store *database.RuntimeStore, schema runtimeschema.SQLDatabase) error {
	return runtimeschema.EnsureAgentStateSchema(ctx, schemaStoreOverride{RuntimeStore: store, schema: schema})
}

func migrateAgentTaskRunSchema(ctx context.Context, store *database.RuntimeStore, schema runtimeschema.SQLDatabase) error {
	return runtimeschema.EnsureAgentTaskRunSchema(ctx, schemaStoreOverride{RuntimeStore: store, schema: schema})
}

func migrateAgentInteractiveRunSchema(ctx context.Context, store *database.RuntimeStore, schema runtimeschema.SQLDatabase) error {
	return runtimeschema.EnsureAgentInteractiveRunSchema(ctx, schemaStoreOverride{RuntimeStore: store, schema: schema})
}

type schemaStoreOverride struct {
	*database.RuntimeStore
	schema runtimeschema.SQLDatabase
}

func (s schemaStoreOverride) SchemaDB() runtimeschema.SQLDatabase { return s.schema }
