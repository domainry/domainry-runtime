package runtime

import (
	"context"
	"errors"
	"fmt"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	knowledgecontract "github.com/domainry/domainry-knowledge-sdk/contract"
	knowledgemodulehost "github.com/domainry/domainry-knowledge-sdk/modulehost"
	ormdriver "github.com/domainry/domainry-orm/driver"
	knowledgehost "github.com/domainry/domainry-runtime/runtime/application/knowledgehost"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// runtimeKnowledgeModuleHost gives Knowledge the same host-owned database,
// dialect, migration lock and sole migration ledger as every embedded module.
type runtimeKnowledgeModuleHost struct{ store *persistence.RuntimeStore }

func (h runtimeKnowledgeModuleHost) Database() knowledgemodulehost.Database { return h.store.DB() }
func (h runtimeKnowledgeModuleHost) Dialect() knowledgemodulehost.Dialect   { return h.store.SQLRenderer }
func (h runtimeKnowledgeModuleHost) Migrations() knowledgemodulehost.MigrationRegistrar {
	return runtimeKnowledgeMigrationRegistrar{store: h.store}
}
func (h runtimeKnowledgeModuleHost) Profile() ormdriver.Profile { return h.store.RuntimeProfile() }

type runtimeKnowledgeMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r runtimeKnowledgeMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r runtimeKnowledgeMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r runtimeKnowledgeMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []knowledgemodulehost.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, migrations)
}

var _ knowledgemodulehost.Host = runtimeKnowledgeModuleHost{}

type runtimeKnowledgeOwner struct {
	binding   knowledgemodulehost.ModuleBinding
	service   knowledgecontract.Service
	retrieval knowledgecontract.Knowledge
}

func openRuntimeKnowledge(ctx context.Context, runtimeID string, application identitysdk.ApplicationScope, store *persistence.RuntimeStore, principals identitysdk.PrincipalResolver, projection identitysdk.Projection, records *recordapplication.RecordApplicationService, factory knowledgemodulehost.Factory, configured knowledgecontract.Options) (owner runtimeKnowledgeOwner, err error) {
	if factory == nil {
		return runtimeKnowledgeOwner{}, nil
	}
	authority, err := knowledgehost.New(runtimeID, application, principals, projection, records)
	if err != nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("configure Knowledge application host: %w", err)
	}
	if configured.LibraryAuthorizer != nil || configured.SourceAuthorizer != nil || configured.PrincipalDirectory != nil || configured.AccessExplainer != nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("Knowledge Identity and record policy ports are Runtime-owned")
	}
	configured.LibraryAuthorizer = authority
	configured.SourceAuthorizer = authority
	configured.PrincipalDirectory = authority
	configured.AccessExplainer = authority
	binding, err := factory.OpenModule(ctx, knowledgecontract.ApplicationRef{RuntimeID: runtimeID}, runtimeKnowledgeModuleHost{store: store})
	if err != nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("open Knowledge module: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			err = errors.Join(err, binding.Close(context.WithoutCancel(ctx)))
		}
	}()
	ownerRuntime := binding.Runtime()
	if ownerRuntime == nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("Knowledge module returned no Runtime")
	}
	if err = ownerRuntime.Validate(&configured); err != nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("validate Knowledge module: %w", err)
	}
	retrieval, err := ownerRuntime.Prepare(runtimeID, configured)
	if err != nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("prepare Knowledge module: %w", err)
	}
	configured.Knowledge = retrieval
	if err = ownerRuntime.Activate(runtimeID, configured); err != nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("activate Knowledge module: %w", err)
	}
	service := ownerRuntime.NewService(runtimeID, configured)
	if service == nil {
		return runtimeKnowledgeOwner{}, fmt.Errorf("Knowledge module returned no Service")
	}
	closeOnError = false
	return runtimeKnowledgeOwner{binding: binding, service: service, retrieval: retrieval}, nil
}
