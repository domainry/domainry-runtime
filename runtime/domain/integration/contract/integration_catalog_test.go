package integrationcontract

import (
	"testing"
)

func TestBuiltinCatalogIsRuntimeOwnedStableAndComplete(t *testing.T) {
	connectors, err := Builtin()
	if err != nil {
		t.Fatalf("load runtime connector catalog: %v", err)
	}
	if len(connectors) != 59 {
		t.Fatalf("expected 59 runtime connector descriptors, got %d", len(connectors))
	}
	for index, connector := range connectors {
		if index > 0 && connectors[index-1].Key >= connector.Key {
			t.Fatalf("runtime connector catalog is not stable: %s then %s", connectors[index-1].Key, connector.Key)
		}
		if connector.Source != "runtime:"+connector.Key {
			t.Fatalf("connector %s is not runtime-owned: %s", connector.Key, connector.Source)
		}
		if connector.Provider == "multi" && len(connector.Providers) == 0 {
			t.Fatalf("multi-provider connector %s has no explicit providers", connector.Key)
		}
		configCoverage, secretCoverage := map[string]bool{}, map[string]bool{}
		for _, provider := range connector.Providers {
			for _, field := range provider.ConfigFields {
				configCoverage[field.Key] = true
			}
			for _, field := range provider.SecretFields {
				secretCoverage[field.Key] = true
			}
		}
		for _, key := range connector.ConfigFields {
			if key != "provider" && !configCoverage[key] {
				t.Fatalf("connector %s legacy config field %s is not owned by any typed Provider schema", connector.Key, key)
			}
		}
		for _, key := range connector.SecretRefs {
			if !secretCoverage[key] {
				t.Fatalf("connector %s legacy secret ref %s is not owned by any typed Provider schema", connector.Key, key)
			}
		}
	}
}
