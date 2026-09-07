package definitionmodel

import (
	"encoding/json"
	"testing"
)

func TestActionPayloadFieldPreservesPublishedDescription(t *testing.T) {
	raw, err := json.Marshal(ActionPayloadField{
		Key: "reason", Name: "Reason", Description: "Why the action is requested", Type: "text",
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded ActionPayloadField
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Description != "Why the action is requested" {
		t.Fatalf("payload description was not preserved: %#v", decoded)
	}
}
