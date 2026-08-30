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
	integrationmodulehost "github.com/domainry/domainry-integration-sdk/modulehost"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type runtimeIntegrationModuleHost struct {
	store     *persistence.RuntimeStore
	providers *connector.Registry
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
