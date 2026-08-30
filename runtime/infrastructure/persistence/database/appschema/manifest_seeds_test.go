package appschema

import (
	"encoding/json"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func decodeManifestSeed(t *testing.T, raw string) manifestmodel.ManifestSchema {
	t.Helper()
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestManifestMetadataSeedsEveryResourceKind(t *testing.T) {
	manifest := decodeManifestSeed(t, `{
		"template_id":" template ","version":" 7 ",
		"objects":[{"key":"account","name":"Account","fields":[{"key":"name","name":"Name","type":"text"}],"validations":[{"type":"required","field_key":"name","message":"Required"},{"key":"custom","object_key":"other","type":"custom"}]}],
		"actions":[{"key":"account.create","object_key":"account","label":"Create"}],
		"workflows":[{"key":"account.sync","name":"Sync","trigger":{"object_key":"account"},"action":{"execution_identity":"workflow_service_role:sync_bot"},"run_as":"workflow_service_role:runner"}],
		"scheduler_definitions":[{"key":"account.refresh","name":"Refresh","status":"enabled","schedule_type":"interval","interval_seconds":60,"target_type":"workflow","target_key":"scheduled:account.sync"}],
		"automation_rules":[{"key":"account.notify","object_key":"account","name":"Notify"}],
		"dictionaries":[{"key":"status","name":"Status"}],
		"integrations":{"connectors":[{"key":"crm","name":"CRM"}],"event_mappings":[{"key":"crm.created","provider":"crm"}]},
		"reports":[{"key":"account.report","name":"Report"}],
		"operation_state_examples":[{"key":"account.state","object_key":"account","name":"State"}],
		"sensitive_field_policies":[{"key":"account.secret","object_key":"account","name":"Secret"}],
		"report_export_controls":[{"key":"account.export","report_key":"account.report","name":"Export"}],
		"skills":[{"key":"account.skill","name":"Skill"}],
		"agents":[{"key":"account.agent","name":"Agent"}]
	}`)
	seeds, err := manifestMetadataSeeds(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 4 {
		t.Fatalf("seed count=%d seeds=%#v", len(seeds), seeds)
	}
	if seeds[0].SchemaVersion != "7" || seeds[0].SourceID != "template" {
		t.Fatalf("normalized seed=%#v", seeds[0])
	}
	defaults, err := manifestMetadataSeeds(decodeManifestSeed(t, `{"workflows":[{"key":"account.sync"}]}`))
	if err != nil || defaults[0].SchemaVersion != "1" || defaults[0].SourceID != "generated-template" {
		t.Fatalf("default seed=%#v err=%v", defaults, err)
	}
}

func TestManifestMetadataSeedRejectsMissingDirectKeys(t *testing.T) {
	for _, raw := range []string{
		`{"workflows":[{"key":""}]}`,
		`{"automation_rules":[{"key":""}]}`,
		`{"integrations":{"connectors":[{"key":""}]}}`,
		`{"integrations":{"event_mappings":[{"key":""}]}}`,
	} {
		if _, err := manifestMetadataSeeds(decodeManifestSeed(t, raw)); err == nil {
			t.Fatalf("expected missing key error for %s", raw)
		}
	}
}

func TestManifestMetadataSeedsExcludeExtractedModuleDefinitions(t *testing.T) {
	manifest := decodeManifestSeed(t, `{"scheduler_definitions":[{"key":"daily"}],"reports":[{"key":"summary"}],"operation_state_examples":[{"key":"state"}],"sensitive_field_policies":[{"key":"pii"}],"report_export_controls":[{"key":"export"}],"identity_profile_extensions":[{"object_key":"profile"}]}`)
	seeds, err := manifestMetadataSeeds(manifest)
	if err != nil || len(seeds) != 0 {
		t.Fatalf("module-owned definitions leaked into Runtime seeds=%#v err=%v", seeds, err)
	}
}

func TestMetadataSeedHelpersErrorAndFallbackBranches(t *testing.T) {
	if _, _, err := metadataPayload(make(chan int)); err == nil {
		t.Fatal("expected JSON marshal error")
	}
	raw, hash, err := metadataPayload(map[string]string{"key": "value"})
	if err != nil || len(raw) == 0 || len(hash) != 64 {
		t.Fatalf("raw=%s hash=%q err=%v", raw, hash, err)
	}
	if metadataResourceID(" object ", " account ") != "object:account" {
		t.Fatal("resource ID was not normalized")
	}
	if metadataJoinedKey("", "right") != "right" || metadataJoinedKey("left", "") != "left" || metadataJoinedKey("left", "right") != "left.right" {
		t.Fatal("joined key branches failed")
	}
	for _, testCase := range []struct {
		object string
		index  int
		raw    string
		want   string
	}{
		{object: "", index: 1, raw: `{}`, want: ".validation.2"},
		{object: ".account.", raw: `{"type":"unique","fields":["email","tenant"]}`, want: "account.unique.email_tenant"},
	} {
		manifest := decodeManifestSeed(t, `{"objects":[{"key":"holder","validations":[`+testCase.raw+`]}]}`)
		if got := validationMetadataKey(testCase.object, testCase.index, manifest.Objects[0].Validations[0]); got != testCase.want {
			t.Fatalf("validation key=%q want=%q", got, testCase.want)
		}
	}
	if metadataMapString(map[string]any{"empty": nil, "fallback": " value "}, "missing", "empty", "fallback") != "value" || metadataMapString(nil, "missing") != "" {
		t.Fatal("map string fallback failed")
	}
	for _, field := range []definitionmodel.FieldSchema{
		{Config: map[string]any{"_definition_object_key": "", "definition_object_key": "fallback"}},
		{Config: map[string]any{"_definition_object_key": nil, "definition_object_key": nil}},
		{Config: map[string]any{"_definition_object_key": "", "definition_object_key": ""}},
	} {
		_ = metadataFieldObjectKey(field)
	}
	if metadataMapString(map[string]any{"empty": ""}, "empty") != "" {
		t.Fatal("empty map string was not ignored")
	}
}
