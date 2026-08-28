package integrationmodel

import (
	"reflect"
	"testing"
)

func TestRequiredConnectorSecretRefNamesIgnoresBlankAndNilValues(t *testing.T) {
	connector := ConnectorSchema{Config: map[string]any{
		"required_secret_refs": []any{nil, " ", "token", "token"},
	}}
	if got := RequiredConnectorSecretRefNames(connector); !reflect.DeepEqual(got, []string{"token"}) {
		t.Fatalf("secret refs=%v", got)
	}
	connector.Config["required_secret_refs"] = []string{"client_secret"}
	if got := RequiredConnectorSecretRefNames(connector); !reflect.DeepEqual(got, []string{"client_secret"}) {
		t.Fatalf("typed secret refs=%v", got)
	}
}
