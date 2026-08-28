package integrationcontract

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

// descriptors is owned and shipped by Runtime. Builder/template files are not
// consulted when a Runtime process starts.
//
//go:embed */connector.json
var descriptors embed.FS

var runtimeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func Builtin() ([]integrationmodel.ConnectorSchema, error) {
	return builtinFromFS(descriptors)
}

func builtinFromFS(source fs.FS) ([]integrationmodel.ConnectorSchema, error) {
	paths, _ := fs.Glob(source, "*/connector.json")
	connectors := make([]integrationmodel.ConnectorSchema, 0, len(paths))
	for _, path := range paths {
		raw, readErr := fs.ReadFile(source, path)
		if readErr != nil {
			return nil, fmt.Errorf("read runtime connector descriptor %s: %w", path, readErr)
		}
		var connector integrationmodel.ConnectorSchema
		if decodeErr := json.Unmarshal(raw, &connector); decodeErr != nil {
			return nil, fmt.Errorf("decode runtime connector descriptor %s: %w", path, decodeErr)
		}
		if err := validateDescriptor(path, connector); err != nil {
			return nil, err
		}
		connectors = append(connectors, connector)
	}
	sort.Slice(connectors, func(i, j int) bool { return connectors[i].Key < connectors[j].Key })
	return connectors, nil
}

func validateDescriptor(path string, connector integrationmodel.ConnectorSchema) error {
	directory := strings.Split(filepathSlash(path), "/")[0]
	switch {
	case strings.TrimSpace(connector.Key) == "":
		return fmt.Errorf("runtime connector descriptor %s has no key", path)
	case !integrationmodel.ValidConnectorIdentityKey(connector.Key):
		return fmt.Errorf("runtime connector descriptor %s has invalid key %q", path, connector.Key)
	case connector.Key != directory:
		return fmt.Errorf("runtime connector descriptor %s key %q does not match directory %q", path, connector.Key, directory)
	case strings.TrimSpace(connector.Type) == "":
		return fmt.Errorf("runtime connector descriptor %s has no type", path)
	case strings.TrimSpace(connector.Provider) == "":
		return fmt.Errorf("runtime connector descriptor %s has no provider family", path)
	case !strings.HasPrefix(connector.Source, "runtime:"):
		return fmt.Errorf("runtime connector descriptor %s has non-runtime source %q", path, connector.Source)
	case strings.TrimSpace(connector.Version) == "":
		return fmt.Errorf("runtime connector descriptor %s has no version", path)
	case !runtimeVersionPattern.MatchString(strings.TrimSpace(connector.MinimumRuntimeVersion)):
		return fmt.Errorf("runtime connector descriptor %s has invalid minimum runtime version %q", path, connector.MinimumRuntimeVersion)
	}
	classification := strings.TrimSpace(connector.Classification)
	if classification == "" {
		classification = "external_connector"
	}
	if !stringInSet(classification, "external_connector", "mixed", "runtime_native") {
		return fmt.Errorf("runtime connector descriptor %s has invalid classification %q", path, connector.Classification)
	}
	lifecycle := strings.TrimSpace(connector.LifecycleStatus)
	if lifecycle == "" {
		lifecycle = "active"
	}
	if !stringInSet(lifecycle, "active", "reclassified", "retired") {
		return fmt.Errorf("runtime connector descriptor %s has invalid lifecycle status %q", path, connector.LifecycleStatus)
	}
	if lifecycle != "active" && strings.TrimSpace(connector.ReplacementCapability) == "" {
		return fmt.Errorf("runtime connector descriptor %s lifecycle %q requires replacement_capability", path, lifecycle)
	}
	if err := validateLocalizedDisplayText("connector", connector.Key, connector.Name, connector.Description, connector.I18n); err != nil {
		return fmt.Errorf("runtime connector descriptor %s: %w", path, err)
	}
	for key := range connector.Config {
		if legacyProviderCatalogConfigKey(key) {
			return fmt.Errorf("runtime connector descriptor %s keeps legacy provider catalog in config.%s; use top-level providers[]", path, key)
		}
	}
	providerKeys := map[string]bool{}
	for _, provider := range connector.Providers {
		if !integrationmodel.ValidConnectorIdentityKey(provider.Key) {
			return fmt.Errorf("runtime connector descriptor %s has invalid provider key %q", path, provider.Key)
		}
		if providerKeys[provider.Key] {
			return fmt.Errorf("runtime connector descriptor %s has duplicate provider key %q", path, provider.Key)
		}
		providerKeys[provider.Key] = true
		if err := validateLocalizedDisplayText("provider", provider.Key, provider.Name, provider.Description, provider.I18n); err != nil {
			return fmt.Errorf("runtime connector descriptor %s: %w", path, err)
		}
		if err := validateConnectorFieldSchemas("provider "+provider.Key+" config", provider.ConfigFields, false); err != nil {
			return fmt.Errorf("runtime connector descriptor %s: %w", path, err)
		}
		if err := validateConnectorFieldSchemas("provider "+provider.Key+" secret", provider.SecretFields, true); err != nil {
			return fmt.Errorf("runtime connector descriptor %s: %w", path, err)
		}
	}
	operationKeys := map[string]bool{}
	for _, operation := range connector.Operations {
		if !integrationmodel.ValidConnectorIdentityKey(operation.Key) {
			return fmt.Errorf("runtime connector descriptor %s has invalid operation key %q", path, operation.Key)
		}
		if operationKeys[operation.Key] {
			return fmt.Errorf("runtime connector descriptor %s has duplicate operation key %q", path, operation.Key)
		}
		operationKeys[operation.Key] = true
		if err := validateLocalizedDisplayText("operation", operation.Key, operation.Name, operation.Description, operation.I18n); err != nil {
			return fmt.Errorf("runtime connector descriptor %s: %w", path, err)
		}
		if !validOperationMethod(operation.Method) {
			return fmt.Errorf("runtime connector descriptor %s operation %q has invalid method %q", path, operation.Key, operation.Method)
		}
		if operation.ExecutionMode != "sync" && operation.ExecutionMode != "async" && operation.ExecutionMode != "operation" {
			return fmt.Errorf("runtime connector descriptor %s operation %q has invalid execution mode %q", path, operation.Key, operation.ExecutionMode)
		}
		if operation.SideEffect != "read" && operation.SideEffect != "reserve" && operation.SideEffect != "write" {
			return fmt.Errorf("runtime connector descriptor %s operation %q has invalid side effect %q", path, operation.Key, operation.SideEffect)
		}
		if operation.TimeoutDefaultSeconds <= 0 || operation.TimeoutMaxSeconds <= 0 || operation.TimeoutDefaultSeconds > operation.TimeoutMaxSeconds {
			return fmt.Errorf("runtime connector descriptor %s operation %q has invalid timeout contract %d/%d", path, operation.Key, operation.TimeoutDefaultSeconds, operation.TimeoutMaxSeconds)
		}
	}
	for _, operation := range connector.Operations {
		compensation := strings.TrimSpace(operation.CompensationOperation)
		if compensation != "" && !operationKeys[compensation] {
			return fmt.Errorf("runtime connector descriptor %s operation %q references unknown compensation operation %q", path, operation.Key, compensation)
		}
	}
	expectedFlags := descriptorFeatureFlags(connector)
	if !equalStrings(connector.FeatureFlags, expectedFlags) {
		return fmt.Errorf("runtime connector descriptor %s feature flags %v do not match contract %v", path, connector.FeatureFlags, expectedFlags)
	}
	return nil
}

func descriptorFeatureFlags(connector integrationmodel.ConnectorSchema) []string {
	flags := []string{"provider_catalog"}
	hasConfig, hasSecret, hasTest := false, false, false
	for _, provider := range connector.Providers {
		hasConfig = hasConfig || len(provider.ConfigFields) > 0
		hasSecret = hasSecret || len(provider.SecretFields) > 0
	}
	if hasConfig {
		flags = append(flags, "typed_config")
	}
	if hasSecret {
		flags = append(flags, "typed_secrets")
	}
	if len(connector.Operations) > 0 {
		flags = append(flags, "operations")
	}
	for _, operation := range connector.Operations {
		hasTest = hasTest || operation.Key == "test_connection"
	}
	if hasTest {
		flags = append(flags, "connection_test")
	}
	sort.Strings(flags)
	return flags
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func legacyProviderCatalogConfigKey(key string) bool {
	key = strings.TrimSpace(key)
	return key == "providers" || strings.HasSuffix(key, "_providers")
}

func validateLocalizedDisplayText(kind, key, name, description string, localized localizationmodel.LocalizedTextMap) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "" {
		return fmt.Errorf("%s %q requires default name and description", kind, key)
	}
	for _, locale := range []string{"en-US", "zh-CN"} {
		values := localized[locale]
		if strings.TrimSpace(values["name"]) == "" || strings.TrimSpace(values["description"]) == "" {
			return fmt.Errorf("%s %q requires localized name and description for %s", kind, key, locale)
		}
	}
	return nil
}

func validateConnectorFieldSchemas(kind string, fields []definitionmodel.FieldSchema, secret bool) error {
	seen := map[string]bool{}
	for _, field := range fields {
		if !integrationmodel.ValidConnectorIdentityKey(field.Key) {
			return fmt.Errorf("%s has invalid field key %q", kind, field.Key)
		}
		if seen[field.Key] {
			return fmt.Errorf("%s has duplicate field key %q", kind, field.Key)
		}
		seen[field.Key] = true
		if err := validateLocalizedDisplayText(kind+" field", field.Key, field.Name, field.Description, field.I18n); err != nil {
			return err
		}
		if secret && (field.Config["sensitive"] != true || field.Config["write_only"] != true) {
			return fmt.Errorf("%s field %q must be sensitive and write-only", kind, field.Key)
		}
		if secret {
			if !allowedCredentialKind(strings.TrimSpace(fmt.Sprint(field.Config["credential_kind"]))) {
				return fmt.Errorf("%s field %q requires a supported credential kind", kind, field.Key)
			}
			if !stringInSet(strings.TrimSpace(fmt.Sprint(field.Config["material_format"])), "opaque", "text", "json_object", "pem_or_opaque", "pem_or_reference", "uri_or_dsn") {
				return fmt.Errorf("%s field %q requires a supported material format", kind, field.Key)
			}
			if field.Config["rotation_policy"] != "manual" || !stringInSet(strings.TrimSpace(fmt.Sprint(field.Config["expiry_policy"])), "none", "optional") || field.Config["test_requirement"] != "when_bound" {
				return fmt.Errorf("%s field %q requires lifecycle and test policies", kind, field.Key)
			}
		}
		if !secret {
			if field.Key == "provider" {
				return fmt.Errorf("%s field %q duplicates top-level provider_key", kind, field.Key)
			}
			switch field.Type {
			case "text":
				if field.Validation.MaxLength <= 0 {
					return fmt.Errorf("%s field %q requires a maximum length", kind, field.Key)
				}
			case "integer":
				if field.Validation.Min == nil || field.Validation.Max == nil || *field.Validation.Min > *field.Validation.Max {
					return fmt.Errorf("%s field %q requires a numeric range", kind, field.Key)
				}
			case "boolean":
				if _, ok := field.Default.(bool); !ok {
					return fmt.Errorf("%s field %q requires a boolean default", kind, field.Key)
				}
			case "json":
				if field.Config["json_shape"] != "object" {
					return fmt.Errorf("%s field %q requires an explicit JSON shape", kind, field.Key)
				}
			}
		}
	}
	if !secret {
		for _, field := range fields {
			for _, dependency := range connectorFieldDependencyKeys(field.Config["required_with"]) {
				if dependency == field.Key || !seen[dependency] {
					return fmt.Errorf("%s field %q has unknown required_with dependency %q", kind, field.Key, dependency)
				}
			}
		}
	}
	return nil
}

func allowedCredentialKind(value string) bool {
	return stringInSet(value, "api_key", "basic_auth_password", "bearer_token", "certificate", "connection_string", "database_password", "generic_secret", "identifier", "oauth_client_secret", "private_key", "refresh_token", "service_account", "signing_secret")
}

func stringInSet(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func connectorFieldDependencyKeys(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if key := strings.TrimSpace(fmt.Sprint(item)); key != "" {
				result = append(result, key)
			}
		}
	case []string:
		for _, item := range typed {
			if key := strings.TrimSpace(item); key != "" {
				result = append(result, key)
			}
		}
	}
	return result
}

func validOperationMethod(method string) bool {
	switch method {
	case "DELETE", "GET", "PATCH", "POST", "PUT", "SMTP":
		return true
	default:
		return false
	}
}

func filepathSlash(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}
