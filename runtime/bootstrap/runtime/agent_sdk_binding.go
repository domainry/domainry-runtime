package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentsaashost "github.com/domainry/domainry-agent-sdk/saashost"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type runtimeAgentHost struct {
	runtimeID string
	store     *persistence.RuntimeStore
}

func (runtimeAgentHost) DeferConversationHostBinding() bool { return true }

// runtimeAgentSaaSHost deliberately has no database or migration methods. A
// remote Agent Binding receives only deployment identity and cannot recover
// Runtime persistence through a type assertion on the concrete host value.
type runtimeAgentSaaSHost struct{ runtimeID string }

func (h runtimeAgentSaaSHost) RuntimeID() string { return h.runtimeID }

func synchronizeAgentDefinitions(ctx context.Context, binding agentsdk.Binding, manifest *manifestmodel.ManifestSchema) error {
	repositories, ok := binding.(agentpersistence.DefinitionBinding)
	if !ok || repositories.DefinitionRepository() == nil {
		if len(manifest.Skills) == 0 && len(manifest.Agents) == 0 && len(manifest.AgentTasks) == 0 && len(manifest.AgentEntrypoints) == 0 && len(manifest.AgentServicePrincipals) == 0 {
			return nil
		}
		return errors.New("Agent Binding returned no definition repository")
	}
	sourceID := strings.TrimSpace(manifest.TemplateID)
	if sourceID == "" {
		sourceID = "generated-template"
	}
	version := strings.TrimSpace(manifest.Version)
	if version == "" {
		version = "1"
	}
	snapshot := agentpersistence.DefinitionSnapshot{
		SchemaVersion: version, SourceKind: "manifest", SourceID: sourceID,
		Skills: manifest.Skills, Agents: manifest.Agents, Tasks: manifest.AgentTasks,
		Entrypoints: manifest.AgentEntrypoints, Principals: manifest.AgentServicePrincipals,
	}
	raw, err := json.Marshal(struct {
		Skills      []agentsdk.SkillSchema
		Agents      []agentsdk.AgentSchema
		Tasks       []agentsdk.AgentTaskDefinition
		Entrypoints []agentsdk.AgentEntrypointAssignment
		Principals  []agentsdk.AgentServicePrincipalBinding
	}{snapshot.Skills, snapshot.Agents, snapshot.Tasks, snapshot.Entrypoints, snapshot.Principals})
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	snapshot.SchemaHash = hex.EncodeToString(hash[:])
	if err := repositories.DefinitionRepository().SyncDefinitions(ctx, snapshot); err != nil {
		return err
	}
	persisted, err := repositories.DefinitionRepository().DefinitionSnapshot(ctx)
	if err != nil {
		return err
	}
	manifest.Skills, manifest.Agents, manifest.AgentTasks = persisted.Skills, persisted.Agents, persisted.Tasks
	manifest.AgentEntrypoints, manifest.AgentServicePrincipals = persisted.Entrypoints, persisted.Principals
	return nil
}

func (h runtimeAgentHost) RuntimeID() string                  { return h.runtimeID }
func (h runtimeAgentHost) Database() agentmodulehost.Database { return h.store.DB() }
func (h runtimeAgentHost) Dialect() agentmodulehost.Dialect   { return h.store.SQLRenderer }
func (h runtimeAgentHost) Migrations() agentmodulehost.MigrationRegistrar {
	return runtimeAgentMigrationRegistrar{store: h.store}
}

type runtimeAgentMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r runtimeAgentMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r runtimeAgentMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r runtimeAgentMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []agentmodulehost.SchemaMigration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for i, migration := range migrations {
		values[i] = notificationmodulehost.SchemaMigration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
		if migration.Baseline == nil {
			continue
		}
		baseline := notificationmodulehost.SchemaBaseline{Tables: make([]notificationmodulehost.SchemaTable, len(migration.Baseline.Tables))}
		for ti, table := range migration.Baseline.Tables {
			baseline.Tables[ti] = notificationmodulehost.SchemaTable{Name: table.Name, Columns: make([]notificationmodulehost.SchemaColumn, len(table.Columns)), Indexes: make([]notificationmodulehost.SchemaIndex, len(table.Indexes))}
			for ci, column := range table.Columns {
				baseline.Tables[ti].Columns[ci] = notificationmodulehost.SchemaColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
			}
			for ii, index := range table.Indexes {
				baseline.Tables[ti].Indexes[ii] = notificationmodulehost.SchemaIndex{Name: index.Name, Unique: index.Unique, Columns: append([]string(nil), index.Columns...)}
			}
		}
		values[i].Baseline = &baseline
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}

func openAgentBinding(ctx context.Context, runtimeID string, store *persistence.RuntimeStore, factory agentsdk.Factory) (agentsdk.Binding, error) {
	if factory == nil {
		return nil, nil
	}
	application := agentsdk.ApplicationRef{RuntimeID: runtimeID}
	host := runtimeAgentHost{runtimeID: runtimeID, store: store}
	var binding agentsdk.Binding
	var err error
	if moduleFactory, ok := factory.(agentmodulehost.Factory); ok {
		binding, err = moduleFactory.OpenModule(ctx, application, host)
	} else if saasFactory, ok := factory.(agentsaashost.Factory); ok {
		binding, err = saasFactory.OpenSaaS(ctx, application, runtimeAgentSaaSHost{runtimeID: runtimeID})
	} else {
		binding, err = factory.Open(ctx, application)
	}
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, errors.New("Agent SDK Factory returned no Binding")
	}
	if err := binding.Descriptor().Validate(); err != nil {
		_ = binding.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return binding, nil
}

func openManifestAgentBinding(ctx context.Context, runtimeID string, store *persistence.RuntimeStore, factory agentsdk.Factory, manifest manifestmodel.ManifestSchema) (agentsdk.Binding, error) {
	conversations := false
	if configured, ok := factory.(agentsdk.ConversationFactory); ok {
		conversations = configured.ConversationEnabled()
	}
	if !manifestUsesAgent(manifest) && !conversations {
		return nil, nil
	}
	return openAgentBinding(ctx, runtimeID, store, factory)
}

func manifestUsesAgent(manifest manifestmodel.ManifestSchema) bool {
	return len(manifest.Skills) != 0 ||
		len(manifest.Agents) != 0 ||
		len(manifest.AgentTasks) != 0 ||
		len(manifest.AgentEntrypoints) != 0 ||
		len(manifest.AgentServicePrincipals) != 0
}
