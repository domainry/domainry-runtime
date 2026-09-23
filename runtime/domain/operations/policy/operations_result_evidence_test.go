package policy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationsRedactResultRemovesSensitiveFieldsAndKeepsControlTokens(t *testing.T) {
	result, err := OperationsRedactResult(json.RawMessage(`{"ok":true,"confirmation_token":"fingerprint","nested":{"access_token":"secret","email":"operator@example.com"},"password":"hidden"}`))
	if err != nil {
		t.Fatal(err)
	}
	text := string(result)
	if strings.Contains(text, `"access_token":"secret"`) || strings.Contains(text, `"password":"hidden"`) {
		t.Fatalf("sensitive result was retained: %s", text)
	}
	if !strings.Contains(text, `"confirmation_token":"fingerprint"`) || !strings.Contains(text, `"email":"operator@example.com"`) {
		t.Fatalf("non-secret operation evidence was removed: %s", text)
	}
}

func TestOperationsRedactResultRejectsInvalidJSON(t *testing.T) {
	if _, err := OperationsRedactResult(json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid operation result was accepted")
	}
}
