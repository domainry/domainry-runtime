package runtime

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"strings"

	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-foundation/secrets"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	integrationmodulehost "github.com/domainry/domainry-integration-sdk/modulehost"
	integrationsaashost "github.com/domainry/domainry-integration-sdk/saashost"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type openedRuntimeIntegration struct {
	Binding      integrationsdk.Binding
	Delivery     integrationsdk.Delivery
	Catalog      integrationsdk.Catalog
	Requirements integrationsdk.Requirements
	Management   integrationsdk.Management
	Operations   integrationsdk.Operations
	Workers      integrationsdk.LocalWorkers
	Subjects     integrationsdk.SubjectLifecycle
}

func openRuntimeIntegration(ctx context.Context, application integrationsdk.ApplicationRef, factory integrationsdk.Factory, host runtimeIntegrationModuleHost) (openedRuntimeIntegration, error) {
	binding, err := openIntegrationBinding(ctx, application, factory, host)
	if err != nil {
		return openedRuntimeIntegration{}, err
	}
	if binding == nil {
		return openedRuntimeIntegration{}, fmt.Errorf("Integration Factory returned no Binding")
	}
	if err := binding.Descriptor().Validate(); err != nil {
		return openedRuntimeIntegration{}, err
	}
	result := openedRuntimeIntegration{Binding: binding, Delivery: binding.Delivery(), Catalog: binding.Catalog(), Requirements: binding.Requirements()}
	if result.Delivery == nil {
		return openedRuntimeIntegration{}, fmt.Errorf("Integration Binding returned no Delivery port")
	}
	if result.Catalog == nil {
		return openedRuntimeIntegration{}, fmt.Errorf("Integration Binding returned no Catalog port")
	}
	if result.Requirements == nil {
		return openedRuntimeIntegration{}, fmt.Errorf("Integration Binding returned no Requirements port")
	}
	management, ok := binding.(integrationsdk.ManagementBinding)
	if !ok || management.Management() == nil {
		return openedRuntimeIntegration{}, fmt.Errorf("Integration Binding returned no Management port")
	}
	result.Management = management.Management()
	operations, ok := binding.(integrationsdk.OperationsBinding)
	if !ok || operations.Operations() == nil {
		return openedRuntimeIntegration{}, fmt.Errorf("Integration Binding returned no Operations port")
	}
	result.Operations = operations.Operations()
	if port, ok := binding.(integrationsdk.SubjectLifecycleBinding); ok {
		result.Subjects = port.SubjectLifecycle()
	}
	if binding.Descriptor().Mode == integrationsdk.DeploymentModeModule {
		workers, ok := binding.(integrationsdk.LocalWorkerBinding)
		if !ok {
			return openedRuntimeIntegration{}, fmt.Errorf("Integration Module Binding returned no local worker boundary")
		}
		var found bool
		result.Workers, found = workers.LocalWorkers()
		if !found || result.Workers == nil {
			return openedRuntimeIntegration{}, fmt.Errorf("Integration Module Binding returned no local workers")
		}
	}
	webPush, ok := binding.(integrationsdk.WebPushBinding)
	if !ok || webPush.WebPushSubscriptions() == nil {
		return openedRuntimeIntegration{}, fmt.Errorf("Integration Binding returned no Web Push subscriptions port")
	}
	return result, nil
}

func openIntegrationBinding(ctx context.Context, application integrationsdk.ApplicationRef, factory integrationsdk.Factory, moduleHost integrationmodulehost.Host) (integrationsdk.Binding, error) {
	switch factory.DeploymentMode() {
	case integrationsdk.DeploymentModeModule:
		moduleFactory, ok := factory.(integrationmodulehost.Factory)
		if !ok {
			return nil, fmt.Errorf("Integration Module factory does not implement modulehost.Factory")
		}
		return moduleFactory.OpenModule(ctx, application, moduleHost)
	case integrationsdk.DeploymentModeSaaS:
		saasFactory, ok := factory.(integrationsaashost.Factory)
		if !ok {
			return nil, fmt.Errorf("Integration SaaS factory does not implement saashost.Factory")
		}
		return saasFactory.OpenSaaS(ctx, application, struct{}{})
	default:
		return nil, fmt.Errorf("unsupported Integration deployment mode %q", factory.DeploymentMode())
	}
}

type runtimeIntegrationModuleHost struct {
	store     *persistence.RuntimeStore
	providers *connector.Registry
	triggers  integrationsdk.TriggerSink
}

func (h runtimeIntegrationModuleHost) Database() integrationmodulehost.Database { return h.store.DB() }
func (h runtimeIntegrationModuleHost) Dialect() integrationmodulehost.Dialect {
	return h.store.SQLRenderer
}
func (h runtimeIntegrationModuleHost) Migrations() integrationmodulehost.MigrationRegistrar {
	return runtimeIntegrationMigrationRegistrar{store: h.store}
}
func (h runtimeIntegrationModuleHost) Providers() integrationmodulehost.ProviderRegistry {
	return h.providers
}
func (h runtimeIntegrationModuleHost) SecretCipher() integrationmodulehost.SecretMaterialCipher {
	return h
}
func (h runtimeIntegrationModuleHost) RuntimeTriggers() integrationsdk.TriggerSink { return h.triggers }
func (h runtimeIntegrationModuleHost) EncryptSecretMaterial(ctx context.Context, workspaceID, secretKey, plaintext string) (string, error) {
	return (secrets.Cipher{Keys: h.store.SecretKeyProvider(), Purpose: "integration-secret"}).Encrypt(ctx, workspaceID, secretKey, []byte(plaintext))
}
func (h runtimeIntegrationModuleHost) DecryptSecretMaterial(ctx context.Context, workspaceID, secretKey, encoded string) (string, error) {
	if strings.HasPrefix(encoded, secrets.EnvelopeVersion+":") {
		plain, err := (secrets.Cipher{Keys: h.store.SecretKeyProvider(), Purpose: "integration-secret"}).Decrypt(ctx, workspaceID, secretKey, encoded)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	if !strings.HasPrefix(encoded, "v1:") {
		return "", fmt.Errorf("Integration secret ciphertext version is unsupported")
	}
	key := h.store.SecretMaterialKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil || len(raw) < aead.NonceSize() {
		return "", fmt.Errorf("Integration secret ciphertext is invalid")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(workspaceID+"\x00"+secretKey))
	if err != nil {
		return "", fmt.Errorf("decrypt Integration secret material: %w", err)
	}
	return string(plain), nil
}

type runtimeIntegrationMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r runtimeIntegrationMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r runtimeIntegrationMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r runtimeIntegrationMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []integrationmodulehost.SchemaMigration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for index, migration := range migrations {
		values[index] = notificationmodulehost.SchemaMigration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
		if migration.Baseline == nil {
			continue
		}
		baseline := notificationmodulehost.SchemaBaseline{Tables: make([]notificationmodulehost.SchemaTable, len(migration.Baseline.Tables))}
		for tableIndex, table := range migration.Baseline.Tables {
			baseline.Tables[tableIndex] = notificationmodulehost.SchemaTable{Name: table.Name, Columns: make([]notificationmodulehost.SchemaColumn, len(table.Columns)), Indexes: make([]notificationmodulehost.SchemaIndex, len(table.Indexes))}
			for columnIndex, column := range table.Columns {
				baseline.Tables[tableIndex].Columns[columnIndex] = notificationmodulehost.SchemaColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
			}
			for indexIndex, item := range table.Indexes {
				baseline.Tables[tableIndex].Indexes[indexIndex] = notificationmodulehost.SchemaIndex{Name: item.Name, Unique: item.Unique, Columns: append([]string(nil), item.Columns...)}
			}
		}
		values[index].Baseline = &baseline
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}
