package action

import (
	"encoding/json"
	"testing"
)

func TestDecodeBusinessHandlerOutputRejectsNonObjectDuplicateAndTrailingValues(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty", payload: ""},
		{name: "null", payload: "null"},
		{name: "scalar", payload: `"accepted"`},
		{name: "array", payload: `[]`},
		{name: "duplicate top level", payload: `{"accepted":true,"accepted":false}`},
		{name: "duplicate nested", payload: `{"result":{"id":"one","id":"two"}}`},
		{name: "trailing value", payload: `{"accepted":true}{"other":true}`},
		{name: "trailing invalid token", payload: `{"accepted":true} trailing`},
		{name: "incomplete object", payload: `{"accepted":true,`},
		{name: "missing object delimiter", payload: `{"accepted":true`},
		{name: "incomplete nested array", payload: `{"items":[1,`},
		{name: "missing array delimiter", payload: `{"items":[1`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if output, err := decodeBusinessHandlerOutput(json.RawMessage(test.payload)); err == nil {
				t.Fatalf("invalid output accepted: %+v", output)
			}
		})
	}
}

func TestDecodeBusinessHandlerOutputPreservesJSONNumberPrecision(t *testing.T) {
	output, err := decodeBusinessHandlerOutput(json.RawMessage(`{"sequence":9007199254740993,"nested":{"accepted":true},"items":[1,{"id":"two"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if sequence, ok := output["sequence"].(json.Number); !ok || sequence.String() != "9007199254740993" {
		t.Fatalf("sequence=%T(%v)", output["sequence"], output["sequence"])
	}
	items, ok := output["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items=%T(%v)", output["items"], output["items"])
	}
}
