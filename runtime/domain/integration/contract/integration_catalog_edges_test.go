package integrationcontract

import (
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"testing"
	"testing/fstest"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

type catalogReadFailureFS struct {
	fs.FS
}

func (catalogReadFailureFS) ReadFile(string) ([]byte, error) {
	return nil, errors.New("catalog read failure")
}

func catalogLocalized(name, description string) localizationmodel.LocalizedTextMap {
	return localizationmodel.LocalizedTextMap{
		"en-US": {"name": name, "description": description},
		"zh-CN": {"name": name, "description": description},
	}
}

func validCatalogDescriptor() integrationmodel.ConnectorSchema {
	return integrationmodel.ConnectorSchema{
		Key: "test", Type: "generic", Provider: "single", Source: "runtime:test", Version: "1.0.0", MinimumRuntimeVersion: "1.0.0",
		Name: "Test", Description: "Test connector", I18n: catalogLocalized("Test", "Test connector"), FeatureFlags: []string{"provider_catalog"},
	}
}

func TestValidateDescriptorRejectsEveryContractBoundary(t *testing.T) {
	if err := validateDescriptor("test/connector.json", validCatalogDescriptor()); err != nil {
		t.Fatalf("valid descriptor: %v", err)
	}
	tests := []struct {
		name   string
		path   string
		mutate func(*integrationmodel.ConnectorSchema)
	}{
		{"missing key", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Key = "" }},
		{"invalid key", "bad key/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Key = "bad key" }},
		{"directory mismatch", "other/connector.json", func(*integrationmodel.ConnectorSchema) {}},
		{"missing type", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Type = "" }},
		{"missing provider", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Provider = "" }},
		{"foreign source", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Source = "builder:test" }},
		{"missing version", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Version = "" }},
		{"invalid runtime version", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.MinimumRuntimeVersion = "latest" }},
		{"classification", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Classification = "internal" }},
		{"lifecycle", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.LifecycleStatus = "unknown" }},
		{"replacement", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.LifecycleStatus = "retired" }},
		{"display", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.Name = "" }},
		{"legacy config", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			value.Config = map[string]any{"regional_providers": []any{}}
		}},
		{"invalid provider", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			value.Providers = []integrationmodel.ConnectorProviderSchema{{Key: "bad key"}}
		}},
		{"duplicate provider", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			provider := integrationmodel.ConnectorProviderSchema{Key: "provider", Name: "Provider", Description: "Provider", I18n: catalogLocalized("Provider", "Provider")}
			value.Providers = []integrationmodel.ConnectorProviderSchema{provider, provider}
		}},
		{"provider display", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			value.Providers = []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}
		}},
		{"provider config field", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			value.Providers = []integrationmodel.ConnectorProviderSchema{{Key: "provider", Name: "Provider", Description: "Provider", I18n: catalogLocalized("Provider", "Provider"), ConfigFields: []definitionmodel.FieldSchema{{Key: "bad key"}}}}
		}},
		{"provider secret field", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			value.Providers = []integrationmodel.ConnectorProviderSchema{{Key: "provider", Name: "Provider", Description: "Provider", I18n: catalogLocalized("Provider", "Provider"), SecretFields: []definitionmodel.FieldSchema{{Key: "bad key"}}}}
		}},
		{"invalid operation", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			value.Operations = []integrationmodel.ConnectorOperationSchema{{Key: "bad key"}}
		}},
		{"duplicate operation", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			value.Operations = []integrationmodel.ConnectorOperationSchema{op, op}
		}},
		{"operation display", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			value.Operations = []integrationmodel.ConnectorOperationSchema{{Key: "operation"}}
		}},
		{"operation method", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			op.Method = "TRACE"
			value.Operations = []integrationmodel.ConnectorOperationSchema{op}
		}},
		{"operation mode", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			op.ExecutionMode = "batch"
			value.Operations = []integrationmodel.ConnectorOperationSchema{op}
		}},
		{"operation side effect", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			op.SideEffect = "unknown"
			value.Operations = []integrationmodel.ConnectorOperationSchema{op}
		}},
		{"operation default timeout", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			op.TimeoutDefaultSeconds = 0
			value.Operations = []integrationmodel.ConnectorOperationSchema{op}
		}},
		{"operation maximum timeout", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			op.TimeoutMaxSeconds = 0
			value.Operations = []integrationmodel.ConnectorOperationSchema{op}
		}},
		{"operation timeout", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			op.TimeoutDefaultSeconds = 11
			value.Operations = []integrationmodel.ConnectorOperationSchema{op}
		}},
		{"unknown compensation", "test/connector.json", func(value *integrationmodel.ConnectorSchema) {
			op := validCatalogOperation("operation")
			op.CompensationOperation = "missing"
			value.Operations = []integrationmodel.ConnectorOperationSchema{op}
			value.FeatureFlags = []string{"operations", "provider_catalog"}
		}},
		{"feature flags", "test/connector.json", func(value *integrationmodel.ConnectorSchema) { value.FeatureFlags = []string{"wrong"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validCatalogDescriptor()
			test.mutate(&value)
			if err := validateDescriptor(test.path, value); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
	validReserve := validCatalogDescriptor()
	reserve := validCatalogOperation("reserve")
	reserve.SideEffect = "reserve"
	reserve.CompensationOperation = "compensate"
	validReserve.Operations = []integrationmodel.ConnectorOperationSchema{reserve, validCatalogOperation("compensate")}
	validReserve.FeatureFlags = []string{"operations", "provider_catalog"}
	if err := validateDescriptor("test/connector.json", validReserve); err != nil {
		t.Fatalf("valid reserve/compensation descriptor: %v", err)
	}
}

func TestBuiltinHashAndContractEncodingFailureEdges(t *testing.T) {
	broken := catalogReadFailureFS{FS: fstest.MapFS{"broken/connector.json": &fstest.MapFile{Data: []byte(`{}`)}}}
	if _, err := builtinFromFS(broken); err == nil {
		t.Fatal("unreadable descriptor accepted")
	}
	if _, err := descriptorContractHashFromFS(broken); err == nil {
		t.Fatal("unreadable descriptor hashed")
	}
	if _, err := builtinFromFS(fstest.MapFS{"broken/connector.json": &fstest.MapFile{Data: []byte("{")}}); err == nil {
		t.Fatal("malformed descriptor accepted")
	}
	invalid, err := json.Marshal(integrationmodel.ConnectorSchema{Key: "broken"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builtinFromFS(fstest.MapFS{"broken/connector.json": &fstest.MapFile{Data: invalid}}); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	if hash, err := descriptorContractHashFromFS(fstest.MapFS{}); err != nil || hash == "" {
		t.Fatalf("empty descriptor hash=%q err=%v", hash, err)
	}
	lfHash, err := descriptorContractHashFromFS(fstest.MapFS{"probe/connector.json": &fstest.MapFile{Data: []byte("{\n  \"key\": \"probe\"\n}\n")}})
	if err != nil {
		t.Fatal(err)
	}
	crlfHash, err := descriptorContractHashFromFS(fstest.MapFS{"probe/connector.json": &fstest.MapFile{Data: []byte("{\r\n  \"key\": \"probe\"\r\n}\r\n")}})
	if err != nil || crlfHash != lfHash {
		t.Fatalf("descriptor hash must ignore checkout line endings: lf=%s crlf=%s err=%v", lfHash, crlfHash, err)
	}
	if hash, err := ConnectorContractHash(nil); err != nil || hash == "" {
		t.Fatalf("empty connector hash=%q err=%v", hash, err)
	}
	if _, err := ConnectorContractHash([]integrationmodel.ConnectorSchema{{Key: "bad", Config: map[string]any{"not_json": math.Inf(1)}}}); err == nil {
		t.Fatal("unencodable connector contract accepted")
	}
}

func validCatalogOperation(key string) integrationmodel.ConnectorOperationSchema {
	return integrationmodel.ConnectorOperationSchema{
		Key: key, Name: "Operation", Description: "Operation", I18n: catalogLocalized("Operation", "Operation"), Method: "POST",
		ExecutionMode: "sync", SideEffect: "write", TimeoutDefaultSeconds: 5, TimeoutMaxSeconds: 10,
	}
}

func validCatalogField(key, fieldType string) definitionmodel.FieldSchema {
	return definitionmodel.FieldSchema{Key: key, Name: "Field", Description: "Field", Type: fieldType, I18n: catalogLocalized("Field", "Field")}
}

func TestConnectorFieldSchemaAndCatalogHelperEdges(t *testing.T) {
	minimum, maximum := 1.0, 2.0
	validText := validCatalogField("text", "text")
	validText.Validation.MaxLength = 20
	validInteger := validCatalogField("count", "integer")
	validInteger.Validation.Min, validInteger.Validation.Max = &minimum, &maximum
	validBoolean := validCatalogField("enabled", "boolean")
	validBoolean.Default = false
	validJSON := validCatalogField("payload", "json")
	validJSON.Config = map[string]any{"json_shape": "object", "required_with": []any{"text", " "}}
	if err := validateConnectorFieldSchemas("config", []definitionmodel.FieldSchema{validText, validInteger, validBoolean, validJSON}, false); err != nil {
		t.Fatalf("valid config fields: %v", err)
	}

	invalidConfig := [][]definitionmodel.FieldSchema{
		{{Key: "bad key"}},
		{validText, validText},
		{{Key: "field", Type: "text"}},
		{func() definitionmodel.FieldSchema { value := validText; value.Key = "provider"; return value }()},
		{func() definitionmodel.FieldSchema { value := validText; value.Validation.MaxLength = 0; return value }()},
		{func() definitionmodel.FieldSchema { value := validInteger; value.Validation.Min = nil; return value }()},
		{func() definitionmodel.FieldSchema { value := validInteger; value.Validation.Max = nil; return value }()},
		{func() definitionmodel.FieldSchema {
			value := validInteger
			minimum, maximum := 3.0, 2.0
			value.Validation.Min, value.Validation.Max = &minimum, &maximum
			return value
		}()},
		{func() definitionmodel.FieldSchema { value := validBoolean; value.Default = "false"; return value }()},
		{func() definitionmodel.FieldSchema {
			value := validJSON
			value.Config["json_shape"] = "array"
			return value
		}()},
		{func() definitionmodel.FieldSchema {
			value := validText
			value.Config = map[string]any{"required_with": []string{"missing"}}
			return value
		}()},
		{func() definitionmodel.FieldSchema {
			value := validText
			value.Config = map[string]any{"required_with": []string{"text"}}
			return value
		}()},
	}
	for index, fields := range invalidConfig {
		if err := validateConnectorFieldSchemas("config", fields, false); err == nil {
			t.Fatalf("invalid config field set %d accepted", index)
		}
	}

	secret := validCatalogField("token", "text")
	secret.Config = map[string]any{
		"sensitive": true, "write_only": true, "credential_kind": "api_key", "material_format": "opaque",
		"rotation_policy": "manual", "expiry_policy": "none", "test_requirement": "when_bound",
	}
	if err := validateConnectorFieldSchemas("secret", []definitionmodel.FieldSchema{secret}, true); err != nil {
		t.Fatalf("valid secret field: %v", err)
	}
	for _, key := range []string{"sensitive", "credential_kind", "material_format", "rotation_policy", "expiry_policy", "test_requirement"} {
		value := secret
		value.Config = map[string]any{}
		for configKey, configValue := range secret.Config {
			value.Config[configKey] = configValue
		}
		delete(value.Config, key)
		if err := validateConnectorFieldSchemas("secret", []definitionmodel.FieldSchema{value}, true); err == nil {
			t.Fatalf("secret without %s accepted", key)
		}
	}
	withoutWriteOnly := secret
	withoutWriteOnly.Config = map[string]any{}
	for key, value := range secret.Config {
		withoutWriteOnly.Config[key] = value
	}
	delete(withoutWriteOnly.Config, "write_only")
	if err := validateConnectorFieldSchemas("secret", []definitionmodel.FieldSchema{withoutWriteOnly}, true); err == nil {
		t.Fatal("secret without write_only accepted")
	}

	for _, test := range []struct{ name, description string }{{"", "description"}, {"name", ""}} {
		if err := validateLocalizedDisplayText("field", "key", test.name, test.description, catalogLocalized("Name", "Description")); err == nil {
			t.Fatalf("blank display text accepted: %+v", test)
		}
	}
	if err := validateLocalizedDisplayText("field", "key", "Name", "Description", localizationmodel.LocalizedTextMap{
		"en-US": {"name": "Name", "description": ""},
		"zh-CN": {"name": "Name", "description": "Description"},
	}); err == nil {
		t.Fatal("blank localized description accepted")
	}

	if !allowedCredentialKind("refresh_token") || allowedCredentialKind("unknown") || !stringInSet("one", "one", "two") || stringInSet("three", "one", "two") {
		t.Fatal("catalog set helpers changed")
	}
	if !validOperationMethod("SMTP") || validOperationMethod("TRACE") || filepathSlash(`one\two`) != "one/two" {
		t.Fatal("operation/path helpers changed")
	}
	if !equalStrings([]string{"a"}, []string{"a"}) || equalStrings([]string{"a"}, []string{"a", "b"}) || equalStrings([]string{"a"}, []string{"b"}) {
		t.Fatal("feature flag comparison changed")
	}
	if legacyProviderCatalogConfigKey("provider") || !legacyProviderCatalogConfigKey("regional_providers") || len(connectorFieldDependencyKeys(7)) != 0 {
		t.Fatal("legacy/dependency helpers changed")
	}
	if keys := connectorFieldDependencyKeys([]string{"", " one "}); len(keys) != 1 || keys[0] != "one" {
		t.Fatalf("string dependency keys=%v", keys)
	}
}
