package manifestmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeManifestAcceptsOnlyCurrentStrictContract(t *testing.T) {
	valid := []byte(`{"schema_version":"2","template_id":"crm","version":"1","objects":[]}`)
	manifest, err := DecodeManifest(valid)
	if err != nil || manifest.SchemaVersion != CurrentManifestSchemaVersion || manifest.ManifestHash == "" {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}

	for _, test := range []struct {
		name      string
		raw       string
		wantError string
	}{
		{name: "invalid JSON", raw: "{", wantError: "unexpected"},
		{name: "missing version", raw: `{"template_id":"crm","version":"1","objects":[]}`, wantError: "unsupported manifest schema_version"},
		{name: "unreleased v1", raw: `{"schema_version":"1","template_id":"crm","version":"1","objects":[]}`, wantError: "unsupported manifest schema_version"},
		{name: "unknown frontend field", raw: `{"schema_version":"2","template_id":"crm","version":"1","objects":[],"surfaces":[]}`, wantError: "unknown field"},
		{name: "retired action field", raw: `{"schema_version":"2","template_id":"crm","version":"1","objects":[],"actions":[{"key":"order.submit","object_key":"order","label":"Submit","kind":"record_operation","audit_event":"order.submitted","idempotency_keys":["request_id"]}]}`, wantError: `unknown field "idempotency_keys"`},
		{name: "trailing value", raw: `{"schema_version":"2","template_id":"crm","version":"1","objects":[]} {}`, wantError: "trailing JSON values"},
		{name: "incorrect declared hash", raw: `{"schema_version":"2","template_id":"crm","version":"1","manifest_hash":"incorrect","objects":[]}`, wantError: "does not match canonical content hash"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeManifest([]byte(test.raw)); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v want substring %q", err, test.wantError)
			}
		})
	}
}

func TestInitialWorkspaceAdministratorPasswordIsManifestInputOnly(t *testing.T) {
	manifest, err := DecodeManifest([]byte(`{"schema_version":"2","template_id":"crm","version":"1","objects":[],"initial_workspace_administrator_password":"domainry!123"}`))
	if err != nil || manifest.InitialWorkspaceAdministratorPassword != "domainry!123" {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "initial_workspace_administrator_password") || strings.Contains(string(raw), "domainry!123") {
		t.Fatalf("internal bootstrap password escaped through manifest serialization: %s", raw)
	}
}

func TestManifestContentHashIsStableAndRejectsUnsupportedProgrammaticValues(t *testing.T) {
	manifest := ManifestSchema{SchemaVersion: CurrentManifestSchemaVersion, TemplateID: "crm", Version: "1", ManifestHash: "old"}
	first, err := ManifestContentHash(manifest)
	if err != nil || first == "" {
		t.Fatalf("first hash=%q err=%v", first, err)
	}
	manifest.ManifestHash = "different"
	second, err := ManifestContentHash(manifest)
	if err != nil || second != first {
		t.Fatalf("second hash=%q err=%v want=%q", second, err, first)
	}
	manifest.BusinessLoops = []map[string]any{{"unsupported": make(chan int)}}
	if _, err := ManifestContentHash(manifest); err == nil {
		t.Fatal("unsupported programmatic manifest value was hashed")
	}
}
