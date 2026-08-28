package validation

import (
	"encoding/json"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestMetadataViewDefinitionCoversStructuralEdges(t *testing.T) {
	objects := metadataViewConditionObjects()
	tests := []struct {
		resource string
		payload  string
	}{
		{"view", `{"key":"view","name":"View","object_key":"item","type":"table"}{}`},
		{"", `{"key":"view","name":"View","object_key":"item","type":"table"}`},
		{"view", `{"key":"","name":"View","object_key":"item","type":"table"}`},
		{"view", `{"key":"view","name":" ","object_key":"item","type":"table"}`},
		{"view", `{"key":"view","name":"View","object_key":" ","type":"table"}`},
		{"view", `{"key":"view","name":"View","object_key":"item","type":" "}`},
	}
	for index, testCase := range tests {
		normalized, err := MetadataValidateViewDefinition(testCase.resource, json.RawMessage(testCase.payload), objects)
		if index == 1 {
			if err != nil || len(normalized) == 0 {
				t.Fatalf("default resource normalized=%s err=%v", normalized, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("case %d accepted", index)
		}
	}
}

func TestMetadataViewConfigCoversCollectionScalarPagingSortAndFilterEdges(t *testing.T) {
	object := metadataViewConditionObjects()[0]
	validConfigs := []map[string]any{
		{"columns": []any{"id", "name"}, "search_fields": []any{"name"}},
		{"end_field": "name", "group_by": "name", "lane_field": "name", "start_field": "name", "title_field": "name"},
		{"page_size": float64(1)},
		{"sort": []any{map[string]any{"field": "name", "direction": "desc"}, map[string]any{"field": "name", "direction": ""}}},
		{"filters": []any{map[string]any{"field": "name", "source": ""}}},
	}
	for index, config := range validConfigs {
		if err := metadataValidateViewConfig(config, object); err != nil {
			t.Fatalf("valid config %d: %v", index, err)
		}
	}

	invalidConfigs := []map[string]any{
		{"columns": "name"},
		{"end_field": "missing"},
		{"page_size": "1"},
		{"page_size": float64(0)},
		{"page_size": 1.5},
		{"sort": "name"},
		{"sort": []any{float64(1)}},
		{"sort": []any{"missing"}},
		{"filters": "name"},
		{"filters": []any{"name"}},
		{"filters": []any{map[string]any{"field": "missing"}}},
		{"filters": []any{map[string]any{"field": "name", "source": "request"}}},
	}
	for index, config := range invalidConfigs {
		if err := metadataValidateViewConfig(config, object); err == nil {
			t.Fatalf("invalid config %d accepted", index)
		}
	}
}

func TestMetadataViewHelpersCoverEmptyAndIDFields(t *testing.T) {
	object := metadataViewConditionObjects()[0]
	if err := metadataValidateViewField(object, "id", "columns"); err != nil {
		t.Fatalf("id field: %v", err)
	}
	if err := metadataValidateViewField(object, "", "columns"); err == nil {
		t.Fatal("empty field accepted")
	}
	if err := metadataValidateViewField(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: ""}}}, "", "columns"); err == nil {
		t.Fatal("empty schema field accepted")
	}
	if _, found := metadataViewObject([]definitionmodel.ObjectSchema{{Key: ""}}, ""); found {
		t.Fatal("empty object key matched")
	}
}

func metadataViewConditionObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{{Key: "item", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}
}
