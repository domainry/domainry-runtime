package integrationcontract

import (
	"math"
	"testing"
)

func TestIntegrationProtocolConditionEdges(t *testing.T) {
	for _, value := range []any{float32(math.NaN()), float32(math.Inf(1))} {
		kind, ok := IntegrationProtocolValueType(value)
		if kind != "decimal" || !ok {
			t.Fatalf("type(%v) = %s/%v", value, kind, ok)
		}
	}
	if IntegrationProtocolTypesCompatible("currency", "decimal") || IntegrationProtocolTypesCompatible("text", "number") {
		t.Fatal("incompatible numeric types accepted")
	}
}
