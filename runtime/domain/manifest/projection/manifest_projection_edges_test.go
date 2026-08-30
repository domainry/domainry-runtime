package projection

import (
	"reflect"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestRenderManifestReviewMarkdownEmptyAndFallbackTitles(t *testing.T) {
	for _, tc := range []struct {
		manifest manifestmodel.ManifestSchema
		opts     ReviewArtifactOptions
		want     string
	}{
		{manifestmodel.ManifestSchema{}, ReviewArtifactOptions{}, "# Business System Review"},
		{manifestmodel.ManifestSchema{TemplateID: " template "}, ReviewArtifactOptions{}, "# template"},
		{manifestmodel.ManifestSchema{TemplateID: "template", Name: " Name "}, ReviewArtifactOptions{}, "# Name"},
		{manifestmodel.ManifestSchema{Name: "Name"}, ReviewArtifactOptions{Title: " Explicit "}, "# Explicit"},
		{manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "empty"}}}, ReviewArtifactOptions{}, "# Business System Review"},
	} {
		if got := RenderManifestReviewMarkdown(tc.manifest, tc.opts); !strings.HasPrefix(got, tc.want) || !strings.HasSuffix(got, "\n") {
			t.Errorf("render = %q, want prefix %q", got, tc.want)
		}
	}
}

func TestManifestArtifactHelpers(t *testing.T) {
	field := definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Target: " user "}, Config: map[string]any{"dictionary_key": " status "}, Options: []any{map[string]any{"value": "active"}, "bad", map[string]any{"value": " "}}}
	if got := fieldEvidence(field); got != "target `user`, dictionary `status`, `active`" {
		t.Fatalf("field evidence = %q", got)
	}
	if fieldEvidence(definitionmodel.FieldSchema{}) != "" {
		t.Fatal("empty field evidence not empty")
	}
	mapOptions := definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Options: []string{" first ", ""}}, Options: []map[string]any{{"value": "second"}}}
	if got := manifestFieldAllowedValues(mapOptions); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("allowed values = %#v", got)
	}
	if got := manifestFieldAllowedValues(definitionmodel.FieldSchema{Options: "bad"}); len(got) != 0 {
		t.Fatalf("bad options = %#v", got)
	}
	if got := sortedMapKeys(map[string]any{"b": 1, "a": 2, "omit": 3}, "omit"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("sorted keys = %#v", got)
	}
	refs := seedRecordRefs(map[string]any{"one": "$record: first ", "two": []any{"$record:second", "$record: ", 3}, "three": map[string]any{"x": "$record:first"}, "plain": "value"})
	if !reflect.DeepEqual(refs, []string{"first", "second"}) {
		t.Fatalf("refs = %#v", refs)
	}
	if codeList([]string{"", " a ", "b"}) != "`a`, `b`" || codeList(nil) != "" || valueOrFallback(" x ", "y") != "x" || valueOrFallback(" ", " y ") != "y" {
		t.Fatal("list/value helpers mismatch")
	}
	seeds := []businessseedmodel.SeedRecordSchema{{ObjectKey: "order", Data: map[string]any{"__seed_key": " explicit "}}, {ObjectKey: "order", Data: map[string]any{}}, {ObjectKey: "", Data: nil}, {ObjectKey: "order", Data: map[string]any{"__seed_key": ""}}}
	if manifestSeedKey(seeds[0], 0) != "explicit" || manifestSeedKey(seeds[1], 1) != "order_seed_002" || manifestSeedKey(seeds[2], 2) != "" || manifestSeedKey(seeds[3], 3) != "order_seed_004" {
		t.Fatal("seed key fallback mismatch")
	}
	if manifestMapString(nil, "key") != "" || manifestMapString(map[string]any{"key": nil}, "key") != "" || manifestMapString(map[string]any{"key": " value "}, "key") != "value" {
		t.Fatal("map string mismatch")
	}
	if got := manifestWorkflowObjectKeys(definitionmodel.WorkflowSchema{Trigger: map[string]any{"object_key": " order ", "object_keys": []any{" invoice ", "", 3}}}); !reflect.DeepEqual(got, []string{"order", "invoice", "3"}) {
		t.Fatalf("workflow object keys = %#v", got)
	}
	if got := manifestWorkflowObjectKeys(definitionmodel.WorkflowSchema{Trigger: map[string]any{"object_keys": "bad"}}); len(got) != 0 {
		t.Fatalf("bad workflow object keys = %#v", got)
	}
	if got := manifestWorkflowObjectKeys(definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{ObjectKeys: []string{"order"}}}); !reflect.DeepEqual(got, []string{"order"}) {
		t.Fatalf("blank contract object key = %#v", got)
	}
	var builder strings.Builder
	writeKV(&builder, "Blank", " ")
	writeKV(&builder, "Value", " x ")
	if builder.String() != "- Value: `x`\n" {
		t.Fatalf("KV output = %q", builder.String())
	}
}

func TestManifestRestorationProjection(t *testing.T) {
	persisted := manifestmodel.ManifestSchema{
		Dictionaries: []appschemamodel.DictionarySchema{{Key: "existing"}}, AutomationRules: []automationmodel.AutomationRuleSchema{{Key: "existing"}}, Workflows: []definitionmodel.WorkflowSchema{{Key: "existing"}},
		Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "existing"}}},
	}
	installed := manifestmodel.ManifestSchema{
		ManifestHash: "hash", Description: "description", Dictionaries: []appschemamodel.DictionarySchema{{Key: ""}, {Key: " existing "}, {Key: "new"}, {Key: "new"}},
		AutomationRules: []automationmodel.AutomationRuleSchema{{Key: ""}, {Key: " existing "}, {Key: "new"}, {Key: "new"}},
		Workflows:       []definitionmodel.WorkflowSchema{{Key: ""}, {Key: " existing "}, {Key: "new"}, {Key: "new"}},
		Integrations: integrationmodel.IntegrationSchema{
			Connectors:  []integrationmodel.ConnectorSchema{{Key: "new"}},
			Connections: []integrationmodel.ConnectionSchema{{Key: "connection"}},
		},
		SeedRecords:          []businessseedmodel.SeedRecordSchema{{ObjectKey: "order"}},
		SchedulerDefinitions: []map[string]any{{"key": "nightly"}},
		Reports:              []reportmodel.ReportSchema{{Key: "summary"}},
		Skills:               []agentsdk.SkillSchema{{Key: "lookup"}},
		Agents:               []agentsdk.AgentSchema{{Key: "assistant"}},
	}
	merged := MergeInstalledEnvelope(persisted, installed, []notificationmodel.NotificationTemplate{{Key: "template"}})
	if merged.ManifestHash != "hash" || merged.Description != "description" || len(merged.Dictionaries) != 2 || len(merged.AutomationRules) != 2 || len(merged.Workflows) != 2 || len(merged.NotificationTemplates) != 1 || len(merged.Integrations.Connectors) != 2 || len(merged.Integrations.Connections) != 1 || len(merged.SchedulerDefinitions) != 1 || len(merged.Reports) != 1 || len(merged.Skills) != 1 || len(merged.Agents) != 1 {
		t.Fatalf("merged envelope = %#v", merged)
	}
	merged = MergeConnectorValidationCatalog(merged, []integrationmodel.ConnectorSchema{{Key: "existing"}, {Key: "new"}, {Key: "new"}})
	if len(merged.Integrations.Connectors) != 2 {
		t.Fatalf("connectors = %#v", merged.Integrations.Connectors)
	}
}
