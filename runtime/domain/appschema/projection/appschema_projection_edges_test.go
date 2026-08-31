package projection

import (
	"reflect"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestMetadataFieldValueDomainProjectionEdges(t *testing.T) {
	dictionaries := []appschemamodel.DictionarySchema{
		{Key: ""},
		{Key: "locale_only", Source: " source ", Items: []appschemamodel.DictionaryItemSchema{{Key: "a", Label: "A", Locale: "zh-CN"}}},
		{Key: "mixed", Items: []appschemamodel.DictionaryItemSchema{{Key: "b", Label: "B", SortOrder: 2, Description: "desc", Value: "value", ParentKey: "p", Color: "red", Icon: "i", Tags: []string{"tag"}, UI: map[string]any{"x": 1}, Metadata: map[string]any{"m": 1}}, {Key: "inactive", Status: "inactive"}, {Key: "b", Label: "本地", Locale: "zh-CN"}, {Key: "a", Locale: "zh-CN"}}},
		{Key: "empty", Items: []appschemamodel.DictionaryItemSchema{{Key: "", Status: "inactive"}}},
	}
	objects := []definitionmodel.ObjectSchema{{Key: "object", Fields: []definitionmodel.FieldSchema{
		{Key: "dictionary", Config: map[string]any{"dictionaryKey": "mixed"}},
		{Key: "locale", Config: map[string]any{"dictionary": "locale_only"}, Validation: definitionmodel.FieldValidation{Options: []string{"keep"}}},
		{Key: "missing", Config: map[string]any{"dictionary_key": "missing"}},
		{Key: "inline", Options: []any{"x", "y"}, Config: map[string]any{"dictionary_key": "mixed"}},
		{Key: "config_inline", Config: map[string]any{"valueDomainItems": []string{"c"}}},
		{Key: "config_options", Config: map[string]any{"options": []string{"d"}}},
		{Key: "inline_existing", Options: []string{"e"}, Validation: definitionmodel.FieldValidation{Options: []string{"keep"}}},
	}}}
	got := ApplicationSchemaEnrichObjectsWithFieldValueDomains(objects, dictionaries)
	fields := got[0].Fields
	if !reflect.DeepEqual(fields[0].Validation.Options, []string{"a", "value"}) || fields[0].Config["value_domain"] == nil || !reflect.DeepEqual(fields[1].Validation.Options, []string{"keep"}) || len(fields[2].Validation.Options) != 0 || !reflect.DeepEqual(fields[3].Validation.Options, []string{"x", "y"}) || !reflect.DeepEqual(fields[4].Validation.Options, []string{"c"}) || !reflect.DeepEqual(fields[5].Validation.Options, []string{"d"}) {
		t.Fatalf("enriched fields = %#v", fields)
	}
	if objects[0].Fields[0].Config["value_domain"] != nil {
		t.Fatal("input object mutated")
	}
	for _, tc := range []struct {
		field definitionmodel.FieldSchema
		want  string
	}{
		{definitionmodel.FieldSchema{Config: map[string]any{"dictionary_key": nil, "dictionaryKey": " key "}}, "key"},
		{definitionmodel.FieldSchema{Config: map[string]any{"dictionary_key": "<nil>", "dictionary": "fallback"}}, "fallback"},
		{definitionmodel.FieldSchema{Config: map[string]any{"dictionary_key": "", "dictionary": "fallback"}}, "fallback"},
		{definitionmodel.FieldSchema{}, ""},
	} {
		if got := generatedFieldDictionaryKey(tc.field); got != tc.want {
			t.Errorf("dictionary key = %q, want %q", got, tc.want)
		}
	}
	for _, field := range []definitionmodel.FieldSchema{{Options: []any{}}, {Config: map[string]any{"value_domain": nil, "valueDomain": map[string]any{}}}, {}} {
		_ = generatedFieldHasInlineValueDomain(field)
	}
	if got := generatedInlineValueDomainItems(definitionmodel.FieldSchema{Config: map[string]any{"value_domain": "bad", "valueDomainItems": []string{"later"}}}); len(got) != 1 {
		t.Fatalf("inline fallback items = %#v", got)
	}
}

func TestMetadataDictionaryConversionAndNormalization(t *testing.T) {
	base := appschemamodel.DictionaryItemSchema{Key: " key ", Config: map[string]any{"color": " blue ", "icon": " star "}, Tags: []string{"tag"}, Metadata: map[string]any{"m": 1}}
	normalized := ApplicationSchemaNormalizeGeneratedDictionaryItem(base)
	if normalized.Key != "key" || normalized.Value != "key" || normalized.Label != "key" || normalized.Status != "active" || normalized.Color != "blue" || normalized.Icon != "star" || normalized.UI == nil {
		t.Fatalf("normalized = %#v", normalized)
	}
	explicit := normalizeGeneratedDictionaryItem(appschemamodel.DictionaryItemSchema{Key: "x", Value: "v", Label: "l", Status: "inactive", Color: "red", Icon: "icon", UI: map[string]any{"color": "blue"}})
	if explicit.Color != "red" || explicit.Icon != "icon" || explicit.Status != "inactive" {
		t.Fatalf("explicit = %#v", explicit)
	}
	if dictionaryConfigString(map[string]any{"x": 1}, "x") != "" || dictionaryConfigString(map[string]any{"x": " y "}, "x") != "y" || metadataCloneMap(nil) != nil {
		t.Fatal("config/clone helpers mismatch")
	}
	for _, input := range []any{nil, map[string]any{"x": 1}, struct {
		X string `json:"x"`
	}{"y"}, []string{"not-map"}, make(chan int)} {
		_ = metadataMapFromAny(input)
	}
	items := metadataDictionaryItemsFromAny([]any{
		appschemamodel.DictionaryItemSchema{Key: "schema"},
		map[string]any{"key": "map", "value": "value", "label": "label", "description": "description", "config": map[string]any{"x": 1}, "ui": map[string]any{"x": 1}, "metadata": map[string]any{"x": 1}},
		" string ", " ", 1,
	})
	if len(items) != 3 || len(metadataDictionaryItemsFromAny([]appschemamodel.DictionaryItemSchema{{Key: "x"}})) != 1 || len(metadataDictionaryItemsFromAny([]string{" a ", ""})) != 1 || metadataDictionaryItemsFromAny("bad") != nil {
		t.Fatalf("converted items = %#v", items)
	}
	for _, input := range []any{
		map[string]any{"items": []string{"a"}}, map[string]any{"options": []string{"b"}}, []string{"c"},
	} {
		if len(generatedDictionaryItemsFromConfig(input)) != 1 {
			t.Errorf("failed config conversion: %#v", input)
		}
	}
	if got := generatedDictionaryOptionKeys([]appschemamodel.DictionaryItemSchema{{Key: " key "}, {Value: " value "}, {}}); !reflect.DeepEqual(got, []string{"key", "value"}) {
		t.Fatalf("option keys = %#v", got)
	}
	merged := mergeGeneratedDictionaryItems(
		[]appschemamodel.DictionaryItemSchema{{Key: "same", Description: "d", Value: "v", SortOrder: 2, Status: "active", ParentKey: "p", Color: "c", Icon: "i", Tags: []string{"t"}, UI: map[string]any{"u": 1}, Metadata: map[string]any{"m": 1}}, {Key: "disabled", Status: "inactive"}, {Key: ""}},
		[]appschemamodel.DictionaryItemSchema{{Key: "same", Label: "localized"}, {Key: "new", Label: "new", SortOrder: 2}, {Key: ""}},
	)
	if len(merged) != 2 || merged[0].Key != "new" || merged[1].Key != "same" || merged[1].Description != "d" || merged[1].ParentKey != "p" || len(merged[1].Tags) != 1 || len(merged[1].UI) != 1 || len(merged[1].Metadata) != 1 {
		t.Fatalf("merged = %#v", merged)
	}
	explicitMerged := mergeGeneratedDictionaryItems(
		[]appschemamodel.DictionaryItemSchema{{Key: "same", Description: "base", Value: "base", SortOrder: 1, Status: "active", ParentKey: "base", Color: "base", Icon: "base", Tags: []string{"base"}, UI: map[string]any{"base": true}, Metadata: map[string]any{"base": true}}},
		[]appschemamodel.DictionaryItemSchema{{Key: "same", Description: "local", Value: "local", SortOrder: 2, Status: "active", ParentKey: "local", Color: "local", Icon: "local", Tags: []string{"local"}, UI: map[string]any{"local": true}, Metadata: map[string]any{"local": true}}},
	)
	if len(explicitMerged) != 1 || explicitMerged[0].Description != "local" || explicitMerged[0].Value != "local" || explicitMerged[0].SortOrder != 2 || explicitMerged[0].ParentKey != "local" || explicitMerged[0].Color != "local" || explicitMerged[0].Icon != "local" || explicitMerged[0].Tags[0] != "local" {
		t.Fatalf("explicit localized merge = %#v", explicitMerged)
	}
}
