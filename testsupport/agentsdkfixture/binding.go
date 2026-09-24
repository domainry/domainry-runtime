// Package agentsdkfixture opens the real Agent module through its SDK boundary
// for Runtime integration tests.
package agentsdkfixture

import (
	"context"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodule "github.com/domainry/domainry-agent/module"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	todomodule "github.com/domainry/domainry-todo/module"
)

type host struct {
	runtimeID string
	store     *database.RuntimeStore
}

func (h host) RuntimeID() string                  { return h.runtimeID }
func (h host) Database() agentmodulehost.Database { return h.store.DB() }
func (h host) Dialect() agentmodulehost.Dialect   { return h.store.SQLRenderer }
func (h host) Migrations() agentmodulehost.MigrationRegistrar {
	return registrar{store: h.store}
}

type registrar struct{ store *database.RuntimeStore }

func (r registrar) Driver() string { return r.store.Driver() }
func (r registrar) Schema() string { return r.store.DatabaseSchema() }
func (r registrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []agentmodulehost.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, migrations)
}

func Open(ctx context.Context, store *database.RuntimeStore, runtimeID string) (agentsdk.Binding, error) {
	if store == nil {
		return nil, fmt.Errorf("Agent test store is required")
	}
	options := agentmodule.Options{
		BaseURL:          "http://127.0.0.1",
		APIKey:           "test",
		AgentID:          1,
		KnowledgeFactory: knowledgemodule.NewFactory(),
		TodoFactory:      todomodule.NewFactory(),
	}
	binding, err := agentmodule.NewFactory(options).OpenModule(ctx, agentsdk.ApplicationRef{RuntimeID: runtimeID}, host{runtimeID: runtimeID, store: store})
	if err != nil {
		return nil, err
	}
	if err := binding.Descriptor().Validate(); err != nil {
		_ = binding.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return binding, nil
}
