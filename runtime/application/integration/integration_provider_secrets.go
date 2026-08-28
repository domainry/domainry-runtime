package integration

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"context"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (s *IntegrationApplicationService) ValidateProviderSecretRefs(ctx context.Context, connector integrationmodel.ConnectorSchema, providerKey, status string, refs map[string]string, workspaceID string) error {
	var fields []definitionmodel.FieldSchema
	for _, provider := range connector.Providers {
		if provider.Key == providerKey {
			fields = provider.SecretFields
			break
		}
	}
	byKey := make(map[string]definitionmodel.FieldSchema, len(fields))
	for _, field := range fields {
		byKey[field.Key] = field
		if field.Required && strings.TrimSpace(refs[field.Key]) == "" {
			return badRequest("backend.integration.connection.provider_secret_required", "field_path", "secret_refs."+field.Key, "connector", connector.Key, "provider", providerKey, "field", field.Key)
		}
	}
	for name, reference := range refs {
		field, ok := byKey[name]
		if !ok {
			return badRequest("backend.integration.connection.provider_secret_unknown", "field_path", "secret_refs."+name, "connector", connector.Key, "provider", providerKey, "field", name)
		}
		if !strings.HasPrefix(reference, "secret:") {
			continue
		}
		secretKey := strings.TrimSpace(strings.TrimPrefix(reference, "secret:"))
		secret, exists, err := s.findSecret(ctx, secretKey, workspaceID)
		if err != nil {
			return err
		}
		if !exists {
			return notFound("backend.integration.secret.not_found", "secret_key", secretKey)
		}
		expected, _ := field.Config["credential_kind"].(string)
		expected = strings.TrimSpace(expected)
		if !CredentialKindCompatible(expected, secret.Kind) {
			return badRequest("backend.integration.connection.provider_secret_kind_mismatch", "field_path", "secret_refs."+name, "connector", connector.Key, "provider", providerKey, "field", name, "expected", expected, "actual", secret.Kind)
		}
		if status == "active" && field.Config["test_requirement"] == "when_bound" && secret.LastTestStatus != "succeeded" {
			return badRequest("backend.integration.connection.provider_secret_test_required", "field_path", "secret_refs."+name, "connector", connector.Key, "provider", providerKey, "field", name, "actual", secret.LastTestStatus, "expected", "succeeded")
		}
	}
	return nil
}

func CredentialKindCompatible(expected, actual string) bool {
	expected, actual = strings.TrimSpace(expected), strings.TrimSpace(actual)
	if expected == "" || expected == "generic_secret" || actual == "generic_secret" || expected == actual {
		return true
	}
	switch expected {
	case "bearer_token":
		return actual == "api_key"
	case "signing_secret":
		return actual == "webhook_secret"
	case "connection_string":
		return actual == "database_password"
	case "identifier", "service_account":
		return actual == "api_key"
	}
	return false
}
