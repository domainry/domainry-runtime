package validation

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestMetadataValidateViewDefinitionUsesLiveObjectFields(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "number"}, {Key: "status"}, {Key: "created_at"}}}}
	valid := json.RawMessage(`{"key":"order_list","name":"Orders","object_key":"order","type":"table","config":{"columns":["number","status"],"search_fields":["number"],"sort":["-created_at",{"field":"number","direction":"asc"}],"filters":[{"key":"mine","field":"status","source":"current_user"}],"page_size":50}}`)
	if normalized, err := ApplicationSchemaValidateViewDefinition("order_list", valid, objects); err != nil || len(normalized) == 0 {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}

	tests := []struct {
		name, payload, code string
	}{
		{name: "key mismatch", payload: `{"key":"other","name":"Orders","object_key":"order","type":"table","config":{}}`, code: "backend.metadata.view_key_mismatch"},
		{name: "object missing", payload: `{"key":"order_list","name":"Orders","object_key":"missing","type":"table","config":{}}`, code: "backend.metadata.view_object_not_found"},
		{name: "unknown config", payload: `{"key":"order_list","name":"Orders","object_key":"order","type":"table","config":{"component":"custom"}}`, code: "backend.metadata.view_config_unknown"},
		{name: "field missing", payload: `{"key":"order_list","name":"Orders","object_key":"order","type":"table","config":{"columns":["missing"]}}`, code: "backend.metadata.view_field_not_found"},
		{name: "page size", payload: `{"key":"order_list","name":"Orders","object_key":"order","type":"table","config":{"page_size":201}}`, code: "backend.metadata.view_page_size_invalid"},
		{name: "sort direction", payload: `{"key":"order_list","name":"Orders","object_key":"order","type":"table","config":{"sort":[{"field":"number","direction":"sideways"}]}}`, code: "backend.metadata.view_config_invalid"},
		{name: "top level unknown", payload: `{"key":"order_list","name":"Orders","object_key":"order","type":"table","config":{},"component":"custom"}`, code: "backend.metadata.view_definition_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ApplicationSchemaValidateViewDefinition("order_list", json.RawMessage(test.payload), objects); apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%q want=%q err=%v", apperror.CodeOf(err), test.code, err)
			}
		})
	}
}
