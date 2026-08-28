package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type manifestConnectionRepository struct {
	integrationrepository.IntegrationConnectionRepository
	values   []integrationmodel.IntegrationConnection
	existing []integrationmodel.IntegrationConnection
	err      error
	listErr  error
	cancel   context.CancelFunc
}

func (r *manifestConnectionRepository) ListConnections(_ context.Context, _ string) ([]integrationmodel.IntegrationConnection, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.existing, nil
}

func (r *manifestConnectionRepository) UpsertConnection(_ context.Context, _ string, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	if r.err != nil {
		return integrationmodel.IntegrationConnection{}, r.err
	}
	r.values = append(r.values, value)
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return value, nil
}

type manifestConnectionProvider struct {
	descriptor connector.ProviderDescriptor
	configErr  error
}

type manifestConnectionProviderWithoutValidator struct {
	descriptor connector.ProviderDescriptor
}

func (p *manifestConnectionProviderWithoutValidator) Descriptor() connector.ProviderDescriptor {
	return p.descriptor
}
func (*manifestConnectionProviderWithoutValidator) Call(context.Context, connector.CallRequest) (connector.CallResult, error) {
	return connector.CallResult{}, nil
}

func (p *manifestConnectionProvider) Descriptor() connector.ProviderDescriptor {
	return p.descriptor
}

func (*manifestConnectionProvider) Call(context.Context, connector.CallRequest) (connector.CallResult, error) {
	return connector.CallResult{}, nil
}

func (p *manifestConnectionProvider) ValidateConfig(connector.Connection) error {
	return p.configErr
}

func manifestConnectionProviderRegistry(t *testing.T, configFields []connector.ConfigField, secretFields []connector.SecretField, configErr error) *connector.Registry {
	t.Helper()
	startupActivation := connector.StartupActivationDefaultSafe
	for _, field := range configFields {
		if field.Required && len(field.Default) == 0 {
			startupActivation = connector.StartupActivationManual
		}
	}
	for _, field := range secretFields {
		if field.Required {
			startupActivation = connector.StartupActivationManual
		}
	}
	provider := &manifestConnectionProvider{
		configErr: configErr,
		descriptor: connector.ProviderDescriptor{
			ConnectorKey: "custom", ProviderKey: "primary", ProviderRevision: "provider-v1", StartupActivation: startupActivation,
			ConfigFields: configFields, SecretFields: secretFields,
			Operations: []connector.OperationDescriptor{{
				ConnectorKey: "custom", ProviderKey: "primary", Key: "probe",
				Mode: connector.ModeCall, ContractSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Reliability: connector.ReliabilityContract{
					Effect:         connector.EffectRead,
					Idempotency:    connector.IdempotencyContract{Strategy: connector.IdempotencyNatural},
					Reconciliation: connector.ReconciliationNone,
					Compensation:   connector.CompensationContract{Mode: connector.CompensationNone},
				},
			}},
		},
	}
	registry := connector.NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	return registry
}

func TestResolveManifestConnectionProviderRejectsFamilyValues(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "files", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "local"}, {Key: "s3"}}}
	for _, value := range []string{"", "multi", "generated", "unknown"} {
		if provider, err := resolveManifestConnectionProvider(connector, value); err == nil || provider != "" {
			t.Errorf("value=%q provider=%q error=%v", value, provider, err)
		}
	}
	if provider, err := resolveManifestConnectionProvider(connector, "local"); err != nil || provider != "local" {
		t.Fatalf("concrete provider=%q error=%v", provider, err)
	}
	providerless := integrationmodel.ConnectorSchema{Key: "mock", Provider: "fixture"}
	if provider, err := resolveManifestConnectionProvider(providerless, "fixture"); err == nil || provider != "" {
		t.Fatalf("provider family used as executable identity: provider=%q error=%v", provider, err)
	}
}

func TestSyncManifestIntegrationConnectionsHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := SyncManifestIntegrationConnections(ctx, nil, nil, integrationmodel.IntegrationSchema{Connections: []integrationmodel.ConnectionSchema{{Key: "files", ConnectorKey: "file_storage", ProviderKey: "local"}}}, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test manifest integration sync"))
	if err != context.Canceled {
		t.Fatalf("error=%v", err)
	}
}

func TestSyncManifestIntegrationConnectionsReturnsCatalogFailure(t *testing.T) {
	want := errors.New("catalog failed")
	err := syncManifestIntegrationConnections(t.Context(), nil, nil, integrationmodel.IntegrationSchema{}, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test catalog failure"), func() ([]integrationmodel.ConnectorSchema, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("catalog error=%v", err)
	}
}

func TestSyncManifestIntegrationConnectionsEdges(t *testing.T) {
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test manifest connection sync")
	connector := integrationmodel.ConnectorSchema{Key: "custom", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary"}}}
	repository := &manifestConnectionRepository{}
	if err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{}, principalmodel.SystemScope{}); err == nil {
		t.Fatal("invalid system scope accepted")
	}
	if err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{
		Connectors:  []integrationmodel.ConnectorSchema{connector},
		Connections: []integrationmodel.ConnectionSchema{{}, {Key: "missing-connector"}, {Key: "valid", ConnectorKey: "custom", ProviderKey: "primary", Name: "Valid", Config: map[string]any{"endpoint": "https://example.invalid"}}},
	}, scope); err != nil || len(repository.values) != 1 || repository.values[0].Status != "configured" || repository.values[0].WorkspaceID != principalmodel.InstallationWorkspaceID || repository.values[0].CreatedBy != "manifest" {
		t.Fatalf("synced values=%#v err=%v", repository.values, err)
	}
	if err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{Connections: []integrationmodel.ConnectionSchema{{Key: "unknown", ConnectorKey: "does-not-exist"}}}, scope); err == nil {
		t.Fatal("unknown connector accepted")
	}
	if err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}, Connections: []integrationmodel.ConnectionSchema{{Key: "missing-provider", ConnectorKey: "custom"}}}, scope); err == nil {
		t.Fatal("missing concrete provider accepted")
	}
	for _, config := range []map[string]any{{"provider": "primary"}, {"providers": []any{}}, {"auth_providers": []any{}}} {
		if err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}, Connections: []integrationmodel.ConnectionSchema{{Key: "reserved", ConnectorKey: "custom", ProviderKey: "primary", Config: config}}}, scope); err == nil {
			t.Fatalf("reserved provider config accepted: %#v", config)
		}
	}
	repository.err = errIntegrationManagementTest
	if err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}, Connections: []integrationmodel.ConnectionSchema{{Key: "failure", ConnectorKey: "custom", ProviderKey: "primary", Status: "active"}}}, scope); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("repository error=%v", err)
	}
	repository.err = nil
	ctx, cancel := context.WithCancel(t.Context())
	repository.cancel = cancel
	err := SyncManifestIntegrationConnections(ctx, repository, nil, integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}, Connections: []integrationmodel.ConnectionSchema{{Key: "first", ConnectorKey: "custom", ProviderKey: "primary"}, {Key: "second", ConnectorKey: "custom", ProviderKey: "primary"}}}, scope)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-sync cancellation error=%v", err)
	}
}

func TestManifestConnectionDefaultStatusRequiresExecutableProviderInputs(t *testing.T) {
	schema := integrationmodel.ConnectorSchema{Key: "custom", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary"}}}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test manifest connection readiness")
	requiredConfig := connector.ConfigField{Key: "endpoint", Name: "Endpoint", Type: connector.ConfigFieldText, Required: true}
	requiredSecret := connector.SecretField{
		Key: "token", Name: "Token", Required: true,
		CredentialKind: connector.SecretCredentialGeneric, MaterialFormat: connector.SecretMaterialOpaque,
		RotationPolicy: connector.SecretRotationManual, ExpiryPolicy: connector.SecretExpiryNone,
		TestRequirement: connector.SecretTestOptional,
	}
	tests := []struct {
		name       string
		registry   *connector.Registry
		connection integrationmodel.ConnectionSchema
		want       string
	}{
		{
			name:       "registered provider without required setup is active",
			registry:   manifestConnectionProviderRegistry(t, nil, nil, nil),
			connection: integrationmodel.ConnectionSchema{Key: "ready", ConnectorKey: "custom", ProviderKey: "primary"},
			want:       "active",
		},
		{
			name:       "required config missing remains configured",
			registry:   manifestConnectionProviderRegistry(t, []connector.ConfigField{requiredConfig}, nil, nil),
			connection: integrationmodel.ConnectionSchema{Key: "missing-config", ConnectorKey: "custom", ProviderKey: "primary"},
			want:       "configured",
		},
		{
			name:       "manual provider with required config present remains configured",
			registry:   manifestConnectionProviderRegistry(t, []connector.ConfigField{requiredConfig}, nil, nil),
			connection: integrationmodel.ConnectionSchema{Key: "configured", ConnectorKey: "custom", ProviderKey: "primary", Config: map[string]any{"endpoint": "https://example.invalid"}},
			want:       "configured",
		},
		{
			name:       "required secret remains configured",
			registry:   manifestConnectionProviderRegistry(t, nil, []connector.SecretField{requiredSecret}, nil),
			connection: integrationmodel.ConnectionSchema{Key: "missing-secret", ConnectorKey: "custom", ProviderKey: "primary"},
			want:       "configured",
		},
		{
			name:       "provider config validation failure remains configured",
			registry:   manifestConnectionProviderRegistry(t, nil, nil, errors.New("provider config rejected")),
			connection: integrationmodel.ConnectionSchema{Key: "rejected", ConnectorKey: "custom", ProviderKey: "primary"},
			want:       "configured",
		},
		{
			name:       "explicit verified status is preserved",
			registry:   manifestConnectionProviderRegistry(t, []connector.ConfigField{requiredConfig}, nil, nil),
			connection: integrationmodel.ConnectionSchema{Key: "explicit", ConnectorKey: "custom", ProviderKey: "primary", Status: "verified"},
			want:       "verified",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &manifestConnectionRepository{}
			err := SyncManifestIntegrationConnections(t.Context(), repository, test.registry, integrationmodel.IntegrationSchema{
				Connectors: []integrationmodel.ConnectorSchema{schema}, Connections: []integrationmodel.ConnectionSchema{test.connection},
			}, scope)
			if err != nil || len(repository.values) != 1 || repository.values[0].Status != test.want {
				t.Fatalf("values=%#v error=%v want status=%s", repository.values, err, test.want)
			}
		})
	}
}

func TestSyncManifestIntegrationConnectionsMaterializesProviderDefaultsWithoutOverwritingExplicitValues(t *testing.T) {
	schema := integrationmodel.ConnectorSchema{Key: "custom", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary"}}}
	registry := manifestConnectionProviderRegistry(t, []connector.ConfigField{
		{Key: "base_url", Name: "Base URL", Type: connector.ConfigFieldText, Required: true, Default: []byte(`"https://provider.example"`)},
		{Key: "timeout_seconds", Name: "Timeout", Type: connector.ConfigFieldInteger, Default: []byte(`15`)},
	}, nil, nil)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test manifest provider defaults")

	repository := &manifestConnectionRepository{}
	err := SyncManifestIntegrationConnections(t.Context(), repository, registry, integrationmodel.IntegrationSchema{
		Connectors: []integrationmodel.ConnectorSchema{schema}, Connections: []integrationmodel.ConnectionSchema{
			{Key: "defaulted", ConnectorKey: "custom", ProviderKey: "primary"},
			{Key: "explicit", ConnectorKey: "custom", ProviderKey: "primary", Config: map[string]any{"base_url": "https://manifest.example", "timeout_seconds": 45}},
		},
	}, scope)
	if err != nil || len(repository.values) != 2 {
		t.Fatalf("values=%#v error=%v", repository.values, err)
	}
	if got := repository.values[0]; got.Status != "active" || got.Config["base_url"] != "https://provider.example" || fmt.Sprint(got.Config["timeout_seconds"]) != "15" {
		t.Fatalf("provider defaults were not materialized: %#v", got)
	}
	if got := repository.values[1]; got.Config["base_url"] != "https://manifest.example" || got.Config["timeout_seconds"] != 45 {
		t.Fatalf("manifest values were overwritten: %#v", got)
	}

	repository = &manifestConnectionRepository{existing: []integrationmodel.IntegrationConnection{{
		Key: "managed", WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: "custom", ProviderKey: "primary",
		Status: "verified", Config: map[string]any{"base_url": "https://admin.example"}, CreatedBy: "admin",
	}}}
	err = SyncManifestIntegrationConnections(t.Context(), repository, registry, integrationmodel.IntegrationSchema{
		Connectors:  []integrationmodel.ConnectorSchema{schema},
		Connections: []integrationmodel.ConnectionSchema{{Key: "managed", ConnectorKey: "custom", ProviderKey: "primary"}},
	}, scope)
	if err != nil || len(repository.values) != 1 {
		t.Fatalf("managed values=%#v error=%v", repository.values, err)
	}
	managed := repository.values[0]
	if managed.Status != "verified" || managed.CreatedBy != "admin" || managed.Config["base_url"] != "https://admin.example" || fmt.Sprint(managed.Config["timeout_seconds"]) != "15" {
		t.Fatalf("administrator value priority/default fill changed: %#v", managed)
	}

	repository = &manifestConnectionRepository{existing: []integrationmodel.IntegrationConnection{{
		Key: "managed", WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: "custom", ProviderKey: "primary",
		Status: "configured", Config: map[string]any{}, CreatedBy: "admin",
	}}}
	err = SyncManifestIntegrationConnections(t.Context(), repository, registry, integrationmodel.IntegrationSchema{
		Connectors: []integrationmodel.ConnectorSchema{schema}, Connections: []integrationmodel.ConnectionSchema{{Key: "managed", ConnectorKey: "custom", ProviderKey: "primary"}},
	}, scope)
	if err != nil || repository.values[0].Status != "configured" || repository.values[0].Config["base_url"] != "https://provider.example" {
		t.Fatalf("administrator configured state was promoted: values=%#v error=%v", repository.values, err)
	}
}

func TestSyncManifestIntegrationConnectionsPreservesManagedConnectionState(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "custom", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary"}}}
	repository := &manifestConnectionRepository{existing: []integrationmodel.IntegrationConnection{{
		Key: "primary", WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: "custom", ProviderKey: "primary",
		Name: "Managed name", Status: "verified", Config: map[string]any{"merchant_id": "1900000109"},
		SecretRefs: map[string]string{"private_key": "secret:merchant-private"}, CreatedBy: "admin",
	}}}
	err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{
		Connectors: []integrationmodel.ConnectorSchema{connector}, Connections: []integrationmodel.ConnectionSchema{{
			Key: "primary", ConnectorKey: "custom", ProviderKey: "primary", Name: "Manifest name", Config: map[string]any{"currency": "CNY"},
		}},
	}, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test preserve managed connection"))
	if err != nil || len(repository.values) != 1 {
		t.Fatalf("values=%#v error=%v", repository.values, err)
	}
	saved := repository.values[0]
	if saved.Status != "verified" || saved.CreatedBy != "admin" || saved.Name != "Manifest name" || saved.Config["currency"] != "CNY" || saved.Config["merchant_id"] != "1900000109" || saved.SecretRefs["private_key"] != "secret:merchant-private" {
		t.Fatalf("managed connection state was not preserved: %#v", saved)
	}

	repository = &manifestConnectionRepository{listErr: errIntegrationManagementTest}
	if err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{}, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test list failure")); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("list error=%v", err)
	}
}

func TestResolveManifestConnectionProviderRejectsProviderFamilyFallback(t *testing.T) {
	providerless := integrationmodel.ConnectorSchema{Key: "legacy", Provider: "fixture"}
	if _, err := resolveManifestConnectionProvider(providerless, "fixture"); err == nil {
		t.Fatal("Provider family accepted as executable provider")
	}
	if _, err := resolveManifestConnectionProvider(providerless, "other"); err == nil {
		t.Fatal("undeclared Provider accepted")
	}
	if _, err := resolveManifestConnectionProvider(integrationmodel.ConnectorSchema{Key: "empty"}, ""); err == nil {
		t.Fatal("empty legacy provider accepted")
	}
	for _, family := range []string{"multi", "generated"} {
		if _, err := resolveManifestConnectionProvider(integrationmodel.ConnectorSchema{Key: "family", Provider: family}, ""); err == nil {
			t.Fatalf("family %q accepted without concrete provider", family)
		}
	}
}

func TestManifestConnectionRemainingConditionBoundaries(t *testing.T) {
	manifest := integrationmodel.IntegrationConnection{Config: map[string]any{"manifest": true}}
	existing := integrationmodel.IntegrationConnection{Config: map[string]any{"managed": true}}
	preserved := preserveManagedConnectionState(manifest, existing)
	if preserved.Status != "" || preserved.Name != "" || preserved.CreatedBy != "" {
		t.Fatalf("preserved=%+v", preserved)
	}
	for name, config := range map[string]map[string]any{
		"nil": {"value": nil}, "non-string": {"value": 1},
	} {
		t.Run(name, func(t *testing.T) {
			want := name == "non-string"
			if got := manifestConnectionConfigValuePresent(config, "value"); got != want {
				t.Fatalf("present=%v", got)
			}
		})
	}
	emptyRegistry := connector.NewRegistry()
	emptyRegistry.Freeze()
	if status := manifestConnectionDefaultStatus(emptyRegistry, integrationmodel.IntegrationConnection{ConnectorKey: "custom", ProviderKey: "missing"}); status != "configured" {
		t.Fatalf("missing provider status=%q", status)
	}
	descriptor := connector.ProviderDescriptor{
		ConnectorKey: "custom", ProviderKey: "primary", ProviderRevision: "provider-v1", StartupActivation: connector.StartupActivationDefaultSafe,
		ConfigFields: []connector.ConfigField{{Key: "optional", Name: "Optional", Type: connector.ConfigFieldText}},
		SecretFields: []connector.SecretField{{
			Key: "optional_secret", Name: "Optional secret", CredentialKind: connector.SecretCredentialGeneric,
			MaterialFormat: connector.SecretMaterialOpaque, RotationPolicy: connector.SecretRotationManual,
			ExpiryPolicy: connector.SecretExpiryNone, TestRequirement: connector.SecretTestOptional,
		}},
		Operations: []connector.OperationDescriptor{{ConnectorKey: "custom", ProviderKey: "primary", Key: "probe", Mode: connector.ModeCall, ContractSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Reliability: connector.ReliabilityContract{Effect: connector.EffectRead, Idempotency: connector.IdempotencyContract{Strategy: connector.IdempotencyNatural}, Reconciliation: connector.ReconciliationNone, Compensation: connector.CompensationContract{Mode: connector.CompensationNone}}}},
	}
	providerRegistry := connector.NewRegistry()
	if err := providerRegistry.Register(&manifestConnectionProviderWithoutValidator{descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	providerRegistry.Freeze()
	if status := manifestConnectionDefaultStatus(providerRegistry, integrationmodel.IntegrationConnection{ConnectorKey: "custom", ProviderKey: "primary"}); status != "active" {
		t.Fatalf("optional provider status=%q", status)
	}

	connector := integrationmodel.ConnectorSchema{Key: "custom", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary"}}}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "condition boundaries")
	invalidStatus := &manifestConnectionRepository{}
	if err := SyncManifestIntegrationConnections(t.Context(), invalidStatus, nil, integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}, Connections: []integrationmodel.ConnectionSchema{{Key: "invalid", ConnectorKey: "custom", ProviderKey: "primary", Status: "invalid"}}}, scope); err == nil {
		t.Fatal("invalid status accepted")
	}
	for name, existing := range map[string]integrationmodel.IntegrationConnection{
		"connector mismatch": {Key: "connection", ConnectorKey: "other", ProviderKey: "primary"},
		"provider mismatch":  {Key: "connection", ConnectorKey: "custom", ProviderKey: "other"},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &manifestConnectionRepository{existing: []integrationmodel.IntegrationConnection{existing}}
			err := SyncManifestIntegrationConnections(t.Context(), repository, nil, integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}, Connections: []integrationmodel.ConnectionSchema{{Key: "connection", ConnectorKey: "custom", ProviderKey: "primary"}}}, scope)
			if err != nil || len(repository.values) != 1 || repository.values[0].CreatedBy != "manifest" {
				t.Fatalf("values=%+v err=%v", repository.values, err)
			}
		})
	}
}
