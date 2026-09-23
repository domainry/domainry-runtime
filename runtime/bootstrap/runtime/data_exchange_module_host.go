package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodulehost "github.com/domainry/domainry-data-exchange-sdk/modulehost"
	dataexchangesaashost "github.com/domainry/domainry-data-exchange-sdk/saashost"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-foundation/requestcontext"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type dataExchangeModuleHost struct {
	store     *persistence.RuntimeStore
	providers *recordapplication.DataExchangeProviders
}

func (h dataExchangeModuleHost) Database() *sql.DB { return h.store.DB() }
func (h dataExchangeModuleHost) Migrations() dataexchangemodulehost.MigrationRegistrar {
	return dataExchangeMigrationRegistrar{store: h.store}
}
func (h dataExchangeModuleHost) ImportProvider(key string) (dataexchangemodulehost.ImportProvider, bool) {
	return h.providers.ImportProvider(key)
}
func (h dataExchangeModuleHost) ExportProvider(key string) (dataexchangemodulehost.ExportProvider, bool) {
	return h.providers.ExportProvider(key)
}
func (h dataExchangeModuleHost) WorkspaceContext(ctx context.Context, workspaceID, actorID string) context.Context {
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	if strings.TrimSpace(actorID) != "" {
		ctx = requestcontext.WithActorID(ctx, strings.TrimSpace(actorID))
	}
	return ctx
}

type dataExchangeMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r dataExchangeMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r dataExchangeMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }

var dataExchangeMigrationNamePattern = regexp.MustCompile(`[^a-z0-9_-]+`)

func (r dataExchangeMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []dataexchangemodulehost.Migration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for i, migration := range migrations {
		name := strings.ToLower(strings.TrimSpace(migration.ID))
		name = dataExchangeMigrationNamePattern.ReplaceAllString(name, "_")
		if name == "" {
			return fmt.Errorf("Data Exchange migration %d has no identity", i)
		}
		values[i] = notificationmodulehost.SchemaMigration{Version: uint(i + 1), Name: name, Statements: []string{migration.SQL}}
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}

func (r dataExchangeMigrationRegistrar) ApplyFoundationArtifactMigrations(ctx context.Context, owner string, migrations []sharedartifact.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, migrations)
}

func openOptionalDataExchangeBinding(ctx context.Context, factory dataexchange.Factory, application dataexchange.ApplicationRef, store *persistence.RuntimeStore, providerKey string, importProvider dataexchangemodulehost.ImportProvider, exportProvider dataexchangemodulehost.ExportProvider) (dataexchange.Binding, *recordapplication.DataExchangeProviders, error) {
	if factory == nil {
		return nil, nil, nil
	}
	providers := recordapplication.NewDataExchangeProviders(nil)
	if strings.TrimSpace(providerKey) != "" {
		if err := providers.RegisterImportProvider(providerKey, importProvider); err != nil {
			return nil, nil, fmt.Errorf("register Data Exchange import provider: %w", err)
		}
		if err := providers.RegisterExportProvider(providerKey, exportProvider); err != nil {
			return nil, nil, fmt.Errorf("register Data Exchange export provider: %w", err)
		}
	}
	binding, err := openDataExchangeBinding(ctx, factory, application, dataExchangeModuleHost{store: store, providers: providers})
	if err != nil {
		return nil, nil, fmt.Errorf("open Data Exchange module: %w", err)
	}
	return binding, providers, nil
}

func openDataExchangeBinding(ctx context.Context, factory dataexchange.Factory, application dataexchange.ApplicationRef, host dataExchangeModuleHost) (dataexchange.Binding, error) {
	var binding dataexchange.Binding
	var err error
	switch typed := factory.(type) {
	case dataexchangemodulehost.Factory:
		binding, err = typed.OpenModule(ctx, application, host)
	case dataexchangesaashost.Factory:
		binding, err = typed.OpenSaaS(ctx, application, host)
	default:
		binding, err = factory.Open(ctx, application)
	}
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, fmt.Errorf("Data Exchange factory returned no Binding")
	}
	if err = binding.Descriptor().Validate(); err != nil {
		return nil, err
	}
	return binding, nil
}

var _ dataexchangemodulehost.ModuleHost = dataExchangeModuleHost{}
