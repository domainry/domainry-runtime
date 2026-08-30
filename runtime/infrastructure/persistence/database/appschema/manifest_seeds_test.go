package appschema

import (
	"encoding/json"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
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
		"views":[{"key":"account.list","object_key":"account","name":"Accounts"}],
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
		"entrypoints":[{"key":"home","name":"Home"}],
		"skills":[{"key":"account.skill","name":"Skill"}],
		"agents":[{"key":"account.agent","name":"Agent"}]
	}`)
	seeds, err := manifestMetadataSeeds(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 19 {
		t.Fatalf("seed count=%d seeds=%#v", len(seeds), seeds)
	}
	if seeds[0].SchemaVersion != "7" || seeds[0].SourceID != "template" {
		t.Fatalf("normalized seed=%#v", seeds[0])
	}
	field := seeds[1]
	if field.Key != "account.name" || field.ObjectKey != "account" || metadataFieldObjectKey(field.Payload.(definitionmodel.FieldSchema)) != "account" {
		t.Fatalf("field seed=%#v", field)
	}
	if metadataFieldObjectKey(manifest.Objects[0].Fields[0]) != "" {
		t.Fatal("source manifest was mutated")
	}
	if seeds[2].Key != "account.required.name" || seeds[2].ObjectKey != "account" {
		t.Fatalf("generated validation seed=%#v", seeds[2])
	}
	foundScheduler := false
	for _, seed := range seeds {
		if seed.ResourceType == "scheduler" && seed.Key == "account.refresh" {
			foundScheduler = true
		}
	}
	if !foundScheduler {
		t.Fatalf("published scheduler definition missing from metadata seeds: %#v", seeds)
	}
	defaults, err := manifestMetadataSeeds(decodeManifestSeed(t, `{"objects":[{"key":"account"}]}`))
	if err != nil || defaults[0].SchemaVersion != "1" || defaults[0].SourceID != "generated-template" {
		t.Fatalf("default seed=%#v err=%v", defaults, err)
	}
}

func TestManifestMetadataSeedRejectsMissingDirectKeys(t *testing.T) {
	if metadataAgentTaskKey("task", "") != "" {
		t.Fatal("unversioned Agent Task key accepted")
	}
	for _, raw := range []string{
		`{"objects":[{"key":""}]}`,
		`{"views":[{"key":""}]}`,
		`{"actions":[{"key":""}]}`,
		`{"workflows":[{"key":""}]}`,
		`{"scheduler_definitions":[{"key":""}]}`,
		`{"automation_rules":[{"key":""}]}`,
		`{"dictionaries":[{"key":""}]}`,
		`{"integrations":{"connectors":[{"key":""}]}}`,
		`{"integrations":{"event_mappings":[{"key":""}]}}`,
		`{"reports":[{"key":""}]}`,
		`{"operation_state_examples":[{"key":""}]}`,
		`{"sensitive_field_policies":[{"key":""}]}`,
		`{"report_export_controls":[{"key":""}]}`,
		`{"entrypoints":[{"key":""}]}`,
		`{"skills":[{"key":""}]}`,
		`{"agents":[{"key":""}]}`,
		`{"identity_profile_extensions":[{"object_key":""}]}`,
	} {
		if _, err := manifestMetadataSeeds(decodeManifestSeed(t, raw)); err == nil {
			t.Fatalf("expected missing key error for %s", raw)
		}
	}
	for _, manifest := range []manifestmodel.ManifestSchema{
		{AgentTasks: []agentmodel.AgentTaskDefinition{{}}},
		{AgentEntrypoints: []agentmodel.AgentEntrypointAssignment{{}}},
		{AgentServicePrincipals: []agentmodel.AgentServicePrincipalBinding{{}}},
	} {
		if _, err := manifestMetadataSeeds(manifest); err == nil {
			t.Fatalf("expected missing Agent metadata key error for %#v", manifest)
		}
	}
}

func TestManifestMetadataSeedAcceptsIdentityProfileBinding(t *testing.T) {
	seeds, err := manifestMetadataSeeds(manifestmodel.ManifestSchema{IdentityProfileExtensions: []profilebindingmodel.Binding{{
		ObjectKey: "customer_profile", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "customer"},
	}}})
	if err != nil || len(seeds) != 1 || seeds[0].ResourceType != "identity_profile_binding" {
		t.Fatalf("identity profile seeds=%#v err=%v", seeds, err)
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
