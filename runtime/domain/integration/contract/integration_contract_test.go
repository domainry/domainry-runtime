package integrationcontract

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"strings"
	"testing"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

const expectedDescriptorContractHash = "4a45efe5f916bfdd527e52bbfa18ab7eb1a2fbf5388ec0fda72ed52c3513860c"

func TestRuntimeConnectorDescriptorContractHash(t *testing.T) {
	actual, err := DescriptorContractHash()
	if err != nil {
		t.Fatalf("hash runtime connector descriptors: %v", err)
	}
	if actual != expectedDescriptorContractHash {
		t.Fatalf("runtime connector descriptor contract changed: got %s want %s", actual, expectedDescriptorContractHash)
	}
}

func TestFeishuCalendarIDIsOptionalForPrimaryCalendarDiscovery(t *testing.T) {
	connectors, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, connector := range connectors {
		if connector.Key != "appointment_scheduling" {
			continue
		}
		for _, provider := range connector.Providers {
			if provider.Key != "feishu_calendar" {
				continue
			}
			for _, field := range provider.ConfigFields {
				if field.Key == "calendar_id" {
					if field.Required {
						t.Fatal("Feishu calendar_id must remain optional so the adapter can discover a writable primary calendar")
					}
					return
				}
			}
			t.Fatal("Feishu calendar_id field is missing")
		}
		t.Fatal("Feishu calendar provider is missing")
	}
	t.Fatal("appointment_scheduling connector is missing")
}

func TestRuntimeConnectorDescriptorRejectsInvalidOrDuplicateIdentityKeys(t *testing.T) {
	localized := localizationmodel.LocalizedTextMap{"en-US": {"name": "Probe", "description": "Probe description"}, "zh-CN": {"name": "探针", "description": "探针说明"}}
	base := integrationmodel.ConnectorSchema{Key: "probe", Name: "Probe", Description: "Probe description", I18n: localized, Type: "external_connector", Provider: "multi", Source: "runtime:probe", Version: "1", MinimumRuntimeVersion: "0.1.0", FeatureFlags: []string{"connection_test", "operations", "provider_catalog"}, Providers: []integrationmodel.ConnectorProviderSchema{{Key: "first", Name: "First", Description: "First provider", I18n: localized}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "test_connection", Name: "Test connection", Description: "Test the connection", I18n: localized, Method: "GET", ExecutionMode: "sync", SideEffect: "read", TimeoutDefaultSeconds: 15, TimeoutMaxSeconds: 30}}}
	tests := []struct {
		name      string
		mutate    func(*integrationmodel.ConnectorSchema)
		wantError string
	}{
		{name: "connector format", mutate: func(value *integrationmodel.ConnectorSchema) { value.Key = "Probe" }, wantError: "invalid key"},
		{name: "connector localization", mutate: func(value *integrationmodel.ConnectorSchema) { value.I18n = nil }, wantError: "requires localized name and description"},
		{name: "minimum runtime version", mutate: func(value *integrationmodel.ConnectorSchema) { value.MinimumRuntimeVersion = "latest" }, wantError: "invalid minimum runtime version"},
		{name: "classification", mutate: func(value *integrationmodel.ConnectorSchema) { value.Classification = "maybe" }, wantError: "invalid classification"},
		{name: "lifecycle", mutate: func(value *integrationmodel.ConnectorSchema) { value.LifecycleStatus = "paused" }, wantError: "invalid lifecycle status"},
		{name: "replacement", mutate: func(value *integrationmodel.ConnectorSchema) { value.LifecycleStatus = "reclassified" }, wantError: "requires replacement_capability"},
		{name: "feature flags", mutate: func(value *integrationmodel.ConnectorSchema) { value.FeatureFlags = []string{"provider_catalog"} }, wantError: "feature flags"},
		{name: "provider format", mutate: func(value *integrationmodel.ConnectorSchema) { value.Providers[0].Key = "bad provider" }, wantError: "invalid provider key"},
		{name: "provider localization", mutate: func(value *integrationmodel.ConnectorSchema) { value.Providers[0].I18n = nil }, wantError: "requires localized name and description"},
		{name: "provider duplicate", mutate: func(value *integrationmodel.ConnectorSchema) {
			value.Providers = append(value.Providers, value.Providers[0])
		}, wantError: "duplicate provider key"},
		{name: "operation format", mutate: func(value *integrationmodel.ConnectorSchema) { value.Operations[0].Key = "BadOperation" }, wantError: "invalid operation key"},
		{name: "operation localization", mutate: func(value *integrationmodel.ConnectorSchema) { value.Operations[0].I18n = nil }, wantError: "requires localized name and description"},
		{name: "operation duplicate", mutate: func(value *integrationmodel.ConnectorSchema) {
			value.Operations = append(value.Operations, value.Operations[0])
		}, wantError: "duplicate operation key"},
		{name: "operation method", mutate: func(value *integrationmodel.ConnectorSchema) { value.Operations[0].Method = "TRACE" }, wantError: "invalid method"},
		{name: "operation execution mode", mutate: func(value *integrationmodel.ConnectorSchema) { value.Operations[0].ExecutionMode = "" }, wantError: "invalid execution mode"},
		{name: "operation side effect", mutate: func(value *integrationmodel.ConnectorSchema) { value.Operations[0].SideEffect = "" }, wantError: "invalid side effect"},
		{name: "operation timeout", mutate: func(value *integrationmodel.ConnectorSchema) { value.Operations[0].TimeoutDefaultSeconds = 31 }, wantError: "invalid timeout contract"},
		{name: "operation compensation", mutate: func(value *integrationmodel.ConnectorSchema) { value.Operations[0].CompensationOperation = "missing" }, wantError: "unknown compensation operation"},
		{name: "legacy provider catalog in config", mutate: func(value *integrationmodel.ConnectorSchema) {
			value.Config = map[string]any{"providers": []any{"probe"}}
		}, wantError: "legacy provider catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.Providers = append([]integrationmodel.ConnectorProviderSchema(nil), base.Providers...)
			value.Operations = append([]integrationmodel.ConnectorOperationSchema(nil), base.Operations...)
			test.mutate(&value)
			err := validateDescriptor("probe/connector.json", value)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v, want %q", err, test.wantError)
			}
		})
	}
}

func TestRuntimeConnectorDescriptorFieldContractsRequireLocalizationAndSecretSafety(t *testing.T) {
	localized := localizationmodel.LocalizedTextMap{"en-US": {"name": "Token", "description": "Token description"}, "zh-CN": {"name": "令牌", "description": "令牌说明"}}
	valid := definitionmodel.FieldSchema{Key: "token", Name: "Token", Description: "Token description", Type: "text", I18n: localized, Validation: definitionmodel.FieldValidation{MaxLength: 2048}}
	secret := valid
	secret.Config = map[string]any{"sensitive": true, "write_only": true, "credential_kind": "api_key", "material_format": "opaque", "rotation_policy": "manual", "expiry_policy": "optional", "test_requirement": "when_bound"}
	if err := validateConnectorFieldSchemas("config", []definitionmodel.FieldSchema{valid}, false); err != nil {
		t.Fatal(err)
	}
	if err := validateConnectorFieldSchemas("secret", []definitionmodel.FieldSchema{secret}, true); err != nil {
		t.Fatal(err)
	}
	missingLocalization := valid
	missingLocalization.I18n = nil
	if err := validateConnectorFieldSchemas("config", []definitionmodel.FieldSchema{missingLocalization}, false); err == nil || !strings.Contains(err.Error(), "requires localized") {
		t.Fatalf("localization error=%v", err)
	}
	unsafeSecret := secret
	unsafeSecret.Config = nil
	if err := validateConnectorFieldSchemas("secret", []definitionmodel.FieldSchema{unsafeSecret}, true); err == nil || !strings.Contains(err.Error(), "sensitive and write-only") {
		t.Fatalf("secret safety error=%v", err)
	}
	providerField := valid
	providerField.Key = "provider"
	if err := validateConnectorFieldSchemas("config", []definitionmodel.FieldSchema{providerField}, false); err == nil || !strings.Contains(err.Error(), "provider_key") {
		t.Fatalf("provider ownership error=%v", err)
	}
	booleanWithoutDefault := valid
	booleanWithoutDefault.Type = "boolean"
	booleanWithoutDefault.Default = nil
	if err := validateConnectorFieldSchemas("config", []definitionmodel.FieldSchema{booleanWithoutDefault}, false); err == nil || !strings.Contains(err.Error(), "boolean default") {
		t.Fatalf("boolean default error=%v", err)
	}
	minimum, maximum := float64(1), float64(65535)
	port := definitionmodel.FieldSchema{Key: "port", Name: "Port", Description: "Port description", Type: "integer", I18n: localized, Validation: definitionmodel.FieldValidation{Min: &minimum, Max: &maximum}, Config: map[string]any{"required_with": []any{"missing"}}}
	if err := validateConnectorFieldSchemas("config", []definitionmodel.FieldSchema{valid, port}, false); err == nil || !strings.Contains(err.Error(), "unknown required_with dependency") {
		t.Fatalf("dependency contract error=%v", err)
	}
}
