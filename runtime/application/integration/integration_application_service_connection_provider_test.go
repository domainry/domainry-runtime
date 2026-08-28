// Integration application service connection provider tests.
package integration

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestResolveConnectorProviderRequiresExplicitChoiceForMultiProvider(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "payment", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "paypal"}, {Key: "stripe"}}}
	if _, err := ResolveConnectorProvider(connector, ""); testErrorCode(err) != "backend.integration.connection.provider_required" {
		t.Fatalf("expected provider required, got %v", err)
	}
	if _, err := ResolveConnectorProvider(connector, "unknown"); testErrorCode(err) != "backend.integration.connection.provider_unsupported" {
		t.Fatalf("expected unsupported provider, got %v", err)
	}
	provider, err := ResolveConnectorProvider(connector, "stripe")
	if err != nil || provider != "stripe" {
		t.Fatalf("resolve explicit provider=%q err=%v", provider, err)
	}
}

func TestListConnectionsBackfillsDeterministicProvider(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"legacy": {Key: "legacy", WorkspaceID: "default", ConnectorKey: "webhook", Status: "error", Config: map[string]any{"value": true}},
	}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "webhook", Type: "webhook", Provider: "http", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http"}}}}})
	migrator := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, ConnectionNormalizer: migrator.MigratePersistedConnectionProvider})
	connections, err := service.ListIntegrationConnections(t.Context(), integrationWorkspaceAdmin("admin", "default"))
	if err != nil || len(connections) != 1 || connections[0].ProviderKey != "http" || connections[0].Status != "degraded" {
		t.Fatalf("backfilled connections=%#v err=%v", connections, err)
	}
	persisted := repository.connections["legacy"]
	if persisted.ProviderKey != "http" || persisted.Status != "degraded" || persisted.Config["value"] != true {
		t.Fatalf("persisted connection=%#v", persisted)
	}
}

func TestConnectionWritesRejectProviderCatalogInConfig(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "probe", Type: "custom", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary"}}}
	for _, key := range []string{"provider", "providers", "regional_providers"} {
		err := ValidateConnectionConfig(connector, "primary", "configured", map[string]any{key: "primary"})
		if testErrorCode(err) != "backend.integration.connection.provider_in_config_forbidden" || testErrorParams(err)["field_path"] != "config."+key {
			t.Errorf("config key %q error=%v params=%#v", key, err, testErrorParams(err))
		}
	}
}

func TestProviderSecretContractRejectsUnknownKindAndUntestedActivation(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{
		"wrong":  {Key: "wrong", WorkspaceID: "workspace", Kind: "certificate", Status: "active"},
		"token":  {Key: "token", WorkspaceID: "workspace", Kind: "bearer_token", Status: "active"},
		"tested": {Key: "tested", WorkspaceID: "workspace", Kind: "bearer_token", Status: "active", LastTestStatus: "succeeded"},
	}, materials: map[string]string{}}
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	connector := integrationmodel.ConnectorSchema{Key: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary", SecretFields: []definitionmodel.FieldSchema{{
		Key: "access_token", Type: "text", Required: true, Config: map[string]any{"credential_kind": "bearer_token", "test_requirement": "when_bound"},
	}}}}}
	check := func(status string, refs map[string]string) error {
		return application.ValidateProviderSecretRefs(t.Context(), connector, "primary", status, refs, "workspace")
	}
	if err := check("configured", map[string]string{"invented": "secret:token"}); testErrorCode(err) != "backend.integration.connection.provider_secret_required" {
		t.Fatalf("required secret error=%v", err)
	}
	if err := check("configured", map[string]string{"access_token": "secret:wrong"}); testErrorCode(err) != "backend.integration.connection.provider_secret_kind_mismatch" {
		t.Fatalf("kind mismatch error=%v", err)
	}
	if err := check("active", map[string]string{"access_token": "secret:token"}); testErrorCode(err) != "backend.integration.connection.provider_secret_test_required" {
		t.Fatalf("test requirement error=%v", err)
	}
	if err := check("active", map[string]string{"access_token": "secret:tested"}); err != nil {
		t.Fatalf("tested credential rejected: %v", err)
	}
	if err := check("configured", map[string]string{"access_token": "secret:token", "invented": "secret:token"}); testErrorCode(err) != "backend.integration.connection.provider_secret_unknown" {
		t.Fatalf("unknown secret error=%v", err)
	}
}

func TestResolveConnectorProviderBackfillsOnlyDeterministicProvider(t *testing.T) {
	single := integrationmodel.ConnectorSchema{Key: "webhook", Provider: "http", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http"}}}
	provider, err := ResolveConnectorProvider(single, "")
	if err != nil || provider != "http" {
		t.Fatalf("resolve single provider=%q err=%v", provider, err)
	}
	empty := integrationmodel.ConnectorSchema{Key: "import_export", Provider: "generated"}
	if _, err := ResolveConnectorProvider(empty, ""); testErrorCode(err) != "backend.integration.connection.provider_unavailable" {
		t.Fatalf("expected unavailable provider, got %v", err)
	}
}

func TestConnectionNeverResolvesWithoutExactConnectorProviderAdapter(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "legacy", Type: "http", Provider: "only", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "only"}}}}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	admin := integrationWorkspaceAdmin("admin", "workspace")

	created, err := application.UpsertIntegrationConnection(t.Context(), "new_connection", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "legacy", ProviderKey: "only", Config: map[string]any{"url": "https://example.invalid"}}, admin)
	if err != nil {
		t.Fatalf("save catalog-defined connection: %v", err)
	}
	if adapter, ok := application.AdapterForConnection(created); ok || adapter != nil {
		t.Fatal("new connection resolved without exact provider registration")
	}

	legacy := integrationmodel.IntegrationConnection{Key: "old_connection", ConnectorKey: "legacy", ProviderKey: "only", Status: "active"}
	if adapter, ok := application.AdapterForConnection(legacy); ok || adapter != nil {
		t.Fatal("connection resolved without exact provider registration")
	}
}

func TestValidateIntegrationProviderConfigUsesTypedFields(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "webhook", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http", ConfigFields: []definitionmodel.FieldSchema{{Key: "url", Type: "text", Required: true}, {Key: "timeout_seconds", Type: "integer"}}}}}
	if err := ValidateProviderConfig(connector, "http", map[string]any{"timeout_seconds": "10"}); testErrorCode(err) != "backend.integration.connection.provider_config_required" {
		t.Fatalf("expected required config error, got %v", err)
	}
	if err := ValidateProviderConfig(connector, "http", map[string]any{"url": "https://example.invalid", "timeout_seconds": "slow"}); testErrorCode(err) != "backend.integration.connection.provider_config_type_invalid" {
		t.Fatalf("expected config type error, got %v", err)
	}
	if err := ValidateProviderConfig(connector, "http", map[string]any{"url": "https://example.invalid", "timeout_seconds": "10"}); err != nil {
		t.Fatalf("validate compatible provider config: %v", err)
	}
}

func TestValidateIntegrationProviderConfigEnforcesFieldDependencies(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "database", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "postgres", ConfigFields: []definitionmodel.FieldSchema{
		{Key: "host", Type: "text"},
		{Key: "port", Type: "integer", Config: map[string]any{"required_with": []any{"host"}}},
	}}}}
	if err := ValidateProviderConfig(connector, "postgres", map[string]any{"port": 5432}); testErrorCode(err) != "backend.integration.connection.provider_config_validation_failed" || testErrorParams(err)["rule"] != "required_with:host" {
		t.Fatalf("dependency error=%v params=%#v", err, testErrorParams(err))
	}
	if err := ValidateProviderConfig(connector, "postgres", map[string]any{"host": "db.internal", "port": 5432}); err != nil {
		t.Fatalf("valid dependent config: %v", err)
	}
}

func TestApplyIntegrationProviderConfigDefaultsDoesNotOverrideCallerValues(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary", ConfigFields: []definitionmodel.FieldSchema{
		{Key: "timeout_seconds", Type: "integer", Default: float64(30)},
		{Key: "enabled", Type: "boolean", Default: false},
	}}}}
	config := integrationcontract.IntegrationApplyProviderConfigDefaults(connector, "primary", map[string]any{"timeout_seconds": 45})
	if config["timeout_seconds"] != 45 || config["enabled"] != false {
		t.Fatalf("defaults=%#v", config)
	}
	if untouched := integrationcontract.IntegrationApplyProviderConfigDefaults(connector, "other", nil); len(untouched) != 0 {
		t.Fatalf("unselected Provider defaults leaked: %#v", untouched)
	}
}

func TestValidateIntegrationProviderConfigEnforcesOptionsAndRanges(t *testing.T) {
	minimum, maximum := float64(1), float64(10)
	connector := integrationmodel.ConnectorSchema{Key: "webhook", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http", ConfigFields: []definitionmodel.FieldSchema{
		{Key: "method", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"POST", "PUT"}}},
		{Key: "timeout_seconds", Type: "integer", Validation: definitionmodel.FieldValidation{Min: &minimum, Max: &maximum}},
	}}}}
	for name, config := range map[string]map[string]any{
		"unknown option": {"method": "DELETE", "timeout_seconds": 5},
		"below minimum":  {"method": "POST", "timeout_seconds": 0},
		"above maximum":  {"method": "PUT", "timeout_seconds": 11},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateProviderConfig(connector, "http", config); testErrorCode(err) != "backend.integration.connection.provider_config_validation_failed" {
				t.Fatalf("error=%v code=%s", err, testErrorCode(err))
			}
		})
	}
	if err := ValidateProviderConfig(connector, "http", map[string]any{"method": "POST", "timeout_seconds": "10"}); err != nil {
		t.Fatalf("valid constrained config: %v", err)
	}
}
