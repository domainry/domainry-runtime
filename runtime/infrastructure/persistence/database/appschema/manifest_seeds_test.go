package appschema

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestMetadataProjectionHelpers(t *testing.T) {
	if _, _, err := metadataPayload(make(chan int)); err == nil {
		t.Fatal("expected JSON marshal error")
	}
	raw, hash, err := metadataPayload(map[string]string{"key": "value"})
	if err != nil || len(raw) == 0 || len(hash) != 64 {
		t.Fatalf("raw=%s hash=%q err=%v", raw, hash, err)
	}
	if metadataJoinedKey("", "right") != "right" || metadataJoinedKey("left", "") != "left" || metadataJoinedKey("left", "right") != "left.right" {
		t.Fatal("joined key branches failed")
	}
	validation := definitionmodel.ValidationSchema{Type: "unique", Fields: []string{"email", "tenant"}}
	if got := validationMetadataKey(".account.", 0, validation); got != "account.unique.email_tenant" {
		t.Fatalf("validation key=%q", got)
	}
	if metadataMapString(map[string]any{"empty": nil, "fallback": " value "}, "empty", "fallback") != "value" {
		t.Fatal("map string fallback failed")
	}
}
