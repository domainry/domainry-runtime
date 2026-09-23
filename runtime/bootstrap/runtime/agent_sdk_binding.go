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
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type runtimeAgentHost struct {
	runtimeID       string
	store           *persistence.RuntimeStore
	artifactContent sharedartifact.ContentStore
	artifactWriter  sharedartifact.ContentWriter
}

func (runtimeAgentHost) DeferConversationHostBinding() bool { return true }

// runtimeAgentSaaSHost deliberately has no database or migration methods. A
// remote Agent Binding receives only deployment identity and cannot recover
// Runtime persistence through a type assertion on the concrete host value.
type runtimeAgentSaaSHost struct{ runtimeID string }

func (h runtimeAgentSaaSHost) RuntimeID() string { return h.runtimeID }

func synchronizeAgentDefinitions(ctx context.Context, binding agentsdk.Binding, sourceID, revision string, definitions runtimeext.ProjectDefinitions) error {
	repositories, ok := binding.(agentpersistence.DefinitionBinding)
	if !ok || repositories.DefinitionRepository() == nil {
		if !projectDefinitionsUseAgent(definitions) {
			return nil
		}
		return errors.New("Agent Binding returned no definition repository")
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		sourceID = "project"
	}
	version := strings.TrimSpace(revision)
	if version == "" {
		version = "1"
	}
	snapshot := agentpersistence.DefinitionSnapshot{
		SchemaVersion: version, SourceKind: "project_registry", SourceID: sourceID,
		Skills: definitions.AgentSkills, Agents: definitions.Agents, Tasks: definitions.AgentTasks,
		Entrypoints: definitions.AgentEntrypoints, Principals: definitions.AgentServicePrincipals,
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
	return nil
}

func (h runtimeAgentHost) RuntimeID() string                  { return h.runtimeID }
func (h runtimeAgentHost) Database() agentmodulehost.Database { return h.store.DB() }
func (h runtimeAgentHost) Dialect() agentmodulehost.Dialect   { return h.store.SQLRenderer }
func (h runtimeAgentHost) Migrations() agentmodulehost.MigrationRegistrar {
	return runtimeAgentMigrationRegistrar{store: h.store}
}
func (h runtimeAgentHost) ArtifactContentStore() sharedartifact.ContentStore {
	return h.artifactContent
}
func (h runtimeAgentHost) ArtifactContentWriter() sharedartifact.ContentWriter {
	return h.artifactWriter
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

func openAgentBinding(ctx context.Context, runtimeID string, store *persistence.RuntimeStore, artifactContent sharedartifact.ContentStore, artifactWriter sharedartifact.ContentWriter, factory agentsdk.Factory) (agentsdk.Binding, error) {
	if factory == nil {
		return nil, nil
	}
	application := agentsdk.ApplicationRef{RuntimeID: runtimeID}
	host := runtimeAgentHost{runtimeID: runtimeID, store: store, artifactContent: artifactContent, artifactWriter: artifactWriter}
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

func openProjectAgentBinding(ctx context.Context, runtimeID string, store *persistence.RuntimeStore, artifactContent sharedartifact.ContentStore, artifactWriter sharedartifact.ContentWriter, factory agentsdk.Factory, definitions runtimeext.ProjectDefinitions) (agentsdk.Binding, error) {
	usesAgent := projectDefinitionsUseAgent(definitions)
	conversations := false
	if configured, ok := factory.(agentsdk.ConversationFactory); ok {
		conversations = configured.ConversationEnabled()
	}
	if !usesAgent && !conversations {
		return nil, nil
	}
	if factory == nil {
		return nil, errors.New("project Agent definitions require an Agent SDK Factory")
	}
	return openAgentBinding(ctx, runtimeID, store, artifactContent, artifactWriter, factory)
}
