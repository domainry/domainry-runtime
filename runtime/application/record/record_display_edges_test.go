package record

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordApplicationDisplaySelection(t *testing.T) {
	tests := []struct {
		name        string
		object      definitionmodel.ObjectSchema
		record      recordmodel.Record
		wantKey     string
		wantDisplay string
	}{
		{
			name:    "declared title field",
			object:  definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{"title_field": "legal_name"}}},
			record:  recordmodel.Record{ID: "record-1", Data: map[string]any{"legal_name": "Acme"}},
			wantKey: "legal_name", wantDisplay: "Acme",
		},
		{
			name:    "blank declared value falls back to conventional field",
			object:  definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{"title_field": "legal_name"}}},
			record:  recordmodel.Record{ID: "record-2", Data: map[string]any{"legal_name": " ", "name": "Fallback"}},
			wantKey: "name", wantDisplay: "Fallback",
		},
		{
			name:    "missing display data falls back to id",
			record:  recordmodel.Record{ID: "record-3", Data: map[string]any{"name": nil}},
			wantKey: "id", wantDisplay: "record-3",
		},
		{
			name:    "blank configured title key",
			object:  definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{"title_field": " "}}},
			record:  recordmodel.Record{ID: "record-4", Data: map[string]any{"name": " "}},
			wantKey: "id", wantDisplay: "record-4",
		},
		{
			name:    "nil configured title key",
			object:  definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{}}},
			record:  recordmodel.Record{ID: "record-5"},
			wantKey: "id", wantDisplay: "record-5",
		},
		{
			name:    "missing configured title value",
			object:  definitionmodel.ObjectSchema{UX: map[string]any{"display": map[string]any{"title_field": "legal_name"}}},
			record:  recordmodel.Record{ID: "record-6"},
			wantKey: "id", wantDisplay: "record-6",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, display := recordApplicationDisplay(test.object, test.record)
			if key != test.wantKey || display != test.wantDisplay {
				t.Fatalf("display=(%q,%q), want=(%q,%q)", key, display, test.wantKey, test.wantDisplay)
			}
		})
	}
}
