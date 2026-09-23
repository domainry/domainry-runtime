package devdata

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestEligibleObjectsOrdersRequiredRelationsAndSkipsUnsafeObjects(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{
		{Key: "child", Fields: []definitionmodel.FieldSchema{{Key: "parent_id", Type: "relation", Required: true, Config: map[string]any{"target": "parent"}}}},
		{Key: "parent", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text", Required: true}}},
		{Key: "secret", Fields: []definitionmodel.FieldSchema{{Key: "token", Type: "text", Required: true, Sensitive: true}}},
		{Key: "record_timer", Config: map[string]any{"runtime_owned": true}, Fields: []definitionmodel.FieldSchema{{Key: "timer_key", Type: "text", Required: true}}},
	}
	got := eligibleObjects(objects)
	if len(got) != 2 || got[0].Key != "parent" || got[1].Key != "child" {
		t.Fatalf("unexpected eligible order: %#v", got)
	}
}

func TestDeterministicIDUsesSeed(t *testing.T) {
	first := deterministicID(7, "customer", 1)
	if first != deterministicID(7, "customer", 1) || first == deterministicID(8, "customer", 1) {
		t.Fatalf("development IDs must be stable per seed: %q", first)
	}
}
