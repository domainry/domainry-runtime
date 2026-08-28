package integrationcontract

import (
	"math"
	"testing"
)

func TestIntegrationProtocolValueMatchesType(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		fieldType string
		want      bool
	}{
		{name: "nil", value: nil, fieldType: "unknown", want: true},
		{name: "text", value: "value", fieldType: "text", want: true},
		{name: "long text", value: "value", fieldType: "long_text", want: true},
		{name: "date invalid", value: 1, fieldType: "date", want: false},
		{name: "integer", value: int16(1), fieldType: "integer", want: true},
		{name: "integer float", value: float64(2), fieldType: "integer", want: true},
		{name: "integer fractional", value: 2.5, fieldType: "integer", want: false},
		{name: "integer invalid", value: "2", fieldType: "integer", want: false},
		{name: "decimal", value: uint64(2), fieldType: "decimal", want: true},
		{name: "decimal invalid", value: "2", fieldType: "decimal", want: false},
		{name: "boolean", value: true, fieldType: "boolean", want: true},
		{name: "boolean invalid", value: "true", fieldType: "boolean", want: false},
		{name: "json", value: make(chan int), fieldType: "json", want: true},
		{name: "unknown", value: "value", fieldType: "email", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IntegrationProtocolValueMatchesType(test.value, test.fieldType); got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIntegrationProtocolValueType(t *testing.T) {
	tests := []struct {
		name  string
		value any
		kind  string
		ok    bool
	}{
		{name: "bool", value: true, kind: "boolean", ok: true},
		{name: "integer", value: int32(2), kind: "integer", ok: true},
		{name: "float32 integer", value: float32(2), kind: "integer", ok: true},
		{name: "float32 decimal", value: float32(2.5), kind: "decimal", ok: true},
		{name: "float64 integer", value: float64(3), kind: "integer", ok: true},
		{name: "float64 decimal", value: 3.5, kind: "decimal", ok: true},
		{name: "nan", value: math.NaN(), kind: "decimal", ok: true},
		{name: "infinity", value: math.Inf(1), kind: "decimal", ok: true},
		{name: "map", value: map[string]any{}, kind: "json", ok: true},
		{name: "slice", value: []any{}, kind: "json", ok: true},
		{name: "string", value: "value", kind: "text", ok: true},
		{name: "nil", value: nil, kind: "", ok: false},
		{name: "other", value: struct{}{}, kind: "json", ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, ok := IntegrationProtocolValueType(test.value)
			if kind != test.kind || ok != test.ok {
				t.Fatalf("type = %q,%v, want %q,%v", kind, ok, test.kind, test.ok)
			}
		})
	}
}

func TestIntegrationProtocolTypesCompatible(t *testing.T) {
	tests := []struct {
		source string
		target string
		want   bool
	}{
		{source: "text", target: "json", want: true},
		{source: " INTEGER ", target: "integer", want: true},
		{source: "integer", target: "decimal", want: true},
		{source: "number", target: "decimal", want: true},
		{source: "integer", target: "number", want: true},
		{source: "decimal", target: "number", want: true},
		{source: "currency", target: "number", want: true},
		{source: "email", target: "text", want: true},
		{source: "relation", target: "url", want: true},
		{source: "boolean", target: "text", want: false},
		{source: "text", target: "boolean", want: false},
	}
	for _, test := range tests {
		if got := IntegrationProtocolTypesCompatible(test.source, test.target); got != test.want {
			t.Fatalf("compatible(%q,%q) = %v, want %v", test.source, test.target, got, test.want)
		}
	}
}
