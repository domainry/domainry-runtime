package manifestmodel

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeManifestMigratesV1FrontendPayloadWithGoldenReport(t *testing.T) {
	raw, err := os.ReadFile("testdata/manifest-v1-frontend.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, report, err := DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != CurrentManifestSchemaVersion || manifest.ManifestHash == "" || len(manifest.EntryPoints) != 1 {
		t.Fatalf("migrated manifest=%#v", manifest)
	}
	migratedRaw, _ := json.Marshal(manifest)
	for _, forbidden := range []string{`"surfaces"`, `"components"`, `"route":"/customers"`, `"surface_key"`, `"layout"`, `"theme"`} {
		if strings.Contains(string(migratedRaw), forbidden) {
			t.Fatalf("migrated manifest retained %s: %s", forbidden, migratedRaw)
		}
	}
	actual, _ := json.MarshalIndent(report, "", "  ")
	expected, err := os.ReadFile("testdata/manifest-v1-frontend-report.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var actualValue, expectedValue any
	if json.Unmarshal(actual, &actualValue) != nil || json.Unmarshal(expected, &expectedValue) != nil {
		t.Fatal("decode golden migration report")
	}
	actualCanonical, _ := json.Marshal(actualValue)
	expectedCanonical, _ := json.Marshal(expectedValue)
	if string(actualCanonical) != string(expectedCanonical) {
		t.Fatalf("migration report drift\nactual: %s\nexpected: %s", actual, expected)
	}
}

func TestDecodeManifestV2RejectsRetiredFrontendKeys(t *testing.T) {
	raw := []byte(`{"schema_version":"2","template_id":"crm","version":"2","objects":[{"key":"customer"}],"surfaces":[]}`)
	if _, _, err := DecodeManifest(raw); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("v2 retired frontend key was not rejected: %v", err)
	}
}

func TestDecodeManifestV2RejectsRetiredActionConfig(t *testing.T) {
	raw := []byte(`{"schema_version":"2","template_id":"crm","version":"2","objects":[],"views":[],"actions":[{"key":"order.submit","object_key":"order","kind":"record_operation","requires_permission":"order.update","audit_event":"order.submitted","config":{"steps":[]}}]}`)
	if _, _, err := DecodeManifest(raw); err == nil || !strings.Contains(err.Error(), "unknown field \"config\"") {
		t.Fatalf("v2 retired Action config was not rejected by strict decode: %v", err)
	}
}

func TestManifestContentHashIsStableAndRejectsUnsupportedProgrammaticValues(t *testing.T) {
	manifest := ManifestSchema{SchemaVersion: CurrentManifestSchemaVersion, TemplateID: "crm", Version: "1", ManifestHash: "old", Objects: nil, Views: nil}
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

func TestDecodeManifestVersionAndStrictDecodeMatrix(t *testing.T) {
	for _, test := range []struct {
		name      string
		raw       string
		wantError string
		migrated  bool
	}{
		{name: "invalid JSON", raw: "{", wantError: "unexpected"},
		{name: "unsupported version", raw: `{"schema_version":"3"}`, wantError: "unsupported manifest schema_version"},
		{name: "strict v2", raw: `{"schema_version":"2","template_id":"crm","version":"1","objects":[],"views":[]}`},
		{name: "versionless", raw: `{"template_id":"crm","version":"1","objects":[],"views":[]}`, migrated: true},
		{name: "v1 decode error", raw: `{"schema_version":"1","objects":"invalid"}`, wantError: "cannot unmarshal", migrated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, report, err := DecodeManifest([]byte(test.raw))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("manifest=%#v report=%#v err=%v", manifest, report, err)
				}
				return
			}
			if err != nil || report.Migrated != test.migrated || manifest.SchemaVersion != CurrentManifestSchemaVersion {
				t.Fatalf("manifest=%#v report=%#v err=%v", manifest, report, err)
			}
			if test.migrated && (report.FromVersion != "1" || manifest.ManifestHash == "") {
				t.Fatalf("migration report=%#v manifest=%#v", report, manifest)
			}
		})
	}
}

func TestLegacyEnvelopeMigrationHandlesAbsentMalformedAndUnchangedSections(t *testing.T) {
	report := ManifestMigrationReport{}
	envelope := map[string]json.RawMessage{
		"frontend":    json.RawMessage(`{"theme":"legacy"}`),
		"menus":       json.RawMessage(`{}`),
		"surfaces":    json.RawMessage(`{`),
		"components":  json.RawMessage(`[{"key":""}]`),
		"entrypoints": json.RawMessage(`[{"key":"entry"}]`),
		"actions":     json.RawMessage(`[{"key":"action","config":"invalid"},{"key":"plain","config":{}}]`),
	}
	originalEntrypoints := append(json.RawMessage(nil), envelope["entrypoints"]...)
	originalActions := append(json.RawMessage(nil), envelope["actions"]...)
	migrateLegacyFrontendEnvelope(envelope, &report)
	for _, removed := range []string{"frontend", "menus", "surfaces", "components"} {
		if _, exists := envelope[removed]; exists {
			t.Fatalf("legacy key %q retained: %#v", removed, envelope)
		}
	}
	if !reflect.DeepEqual(envelope["entrypoints"], originalEntrypoints) || !reflect.DeepEqual(envelope["actions"], originalActions) {
		t.Fatalf("unchanged sections were rewritten: %#v", envelope)
	}
	if len(report.LegacyFrontendReferences) != 0 || len(report.Warnings) != 4 {
		t.Fatalf("migration report=%#v", report)
	}
	malformedSections := map[string]json.RawMessage{"entrypoints": json.RawMessage(`{}`), "actions": json.RawMessage(`{}`)}
	migrateLegacyFrontendEnvelope(malformedSections, &ManifestMigrationReport{})

	report = ManifestMigrationReport{}
	envelope = map[string]json.RawMessage{
		"entrypoints": json.RawMessage(`[{"key":"entry","route":"/entry"}]`),
		"actions":     json.RawMessage(`[{"key":"action","ui_placement":"row","confirmation":"confirm","config":{"confirmation":"again"}},{"key":"without-config"}]`),
	}
	migrateLegacyFrontendEnvelope(envelope, &report)
	if len(report.Warnings) != 4 || strings.Contains(string(envelope["entrypoints"]), "route") || strings.Contains(string(envelope["actions"]), "confirmation") {
		t.Fatalf("changed migration envelope=%s report=%#v", envelope, report)
	}
}

func TestLegacyReferenceExtractionAndValueHelpers(t *testing.T) {
	report := ManifestMigrationReport{}
	extractLegacyMenuReferences(json.RawMessage(`{`), &report)
	extractLegacyMenuReferences(json.RawMessage(`[
		{"key":"","target":{}},
		{"key":" menu ","roles":["admin","admin",""],"object_keys":["customer",""],"target":{"object_key":"order","page_key":"detail"},"required_permission":" read ","route":" /menu "}
	]`), &report)
	extractLegacyFrontendReferences("surfaces", json.RawMessage(`{`), &report)
	extractLegacyFrontendReferences("components", json.RawMessage(`[
		{"key":""},
		{"key":" component ","roles":["admin"],"object_keys":["customer"],"action_keys":["open"],"primary_action":" open ","report_keys":["summary"],"view_key":" list ","status_field":"status","timeline_field":"","permission":" customer.read "}
	]`), &report)
	if len(report.LegacyFrontendReferences) != 2 {
		t.Fatalf("references=%#v", report.LegacyFrontendReferences)
	}
	menu, component := report.LegacyFrontendReferences[0], report.LegacyFrontendReferences[1]
	if menu.Key != "menu" || !reflect.DeepEqual(menu.Objects, []string{"customer", "order"}) || !reflect.DeepEqual(menu.Views, []string{"detail"}) || !reflect.DeepEqual(menu.Roles, []string{"admin"}) {
		t.Fatalf("menu reference=%#v", menu)
	}
	if component.Kind != "component" || !reflect.DeepEqual(component.Actions, []string{"open"}) || !reflect.DeepEqual(component.Fields, []string{"status"}) {
		t.Fatalf("component reference=%#v", component)
	}
	if got := nestedMapText(map[string]any{"target": "invalid"}, "target", "key"); got != "" {
		t.Fatalf("invalid nested text=%q", got)
	}
	if got := rawJSONString(json.RawMessage(`42`)); got != "" {
		t.Fatalf("non-string raw JSON=%q", got)
	}
	if got := mapText(map[string]any{"nil": nil}, "nil"); got != "" {
		t.Fatalf("nil map text=%q", got)
	}
	if got := mapStrings(map[string]any{"items": "invalid"}, "items"); len(got) != 0 {
		t.Fatalf("invalid map strings=%#v", got)
	}
	if got := migrationCompactStrings([]string{" b ", "", "<nil>", "a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("compact strings=%#v", got)
	}
}
