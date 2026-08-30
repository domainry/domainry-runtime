package projection

import (
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestMetadataEnrichObjectsWithFieldValueDomains(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "ticket", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "status", Config: map[string]any{"dictionary_key": "ticket_status"}}}}}
	dictionaries := []appschemamodel.DictionarySchema{{Key: "ticket_status", Items: []appschemamodel.DictionaryItemSchema{{Key: "open", Value: "open", Label: "Open", SortOrder: 1}, {Key: "closed", Value: "closed", Label: "Closed", SortOrder: 2}}}}

	enriched := ApplicationSchemaEnrichObjectsWithFieldValueDomains(objects, dictionaries)
	field := enriched[0].Fields[0]
	if len(field.Validation.Options) != 2 || field.Validation.Options[0] != "open" || field.Validation.Options[1] != "closed" {
		t.Fatalf("field=%#v", field)
	}
}

func TestMetadataMergeGeneratedDictionaryItemsPreservesLocalizedMetadata(t *testing.T) {
	defaults := []appschemamodel.DictionaryItemSchema{{Key: "open", Value: "open", Label: "Open", Description: "Default", Color: "green", SortOrder: 3}}
	localized := []appschemamodel.DictionaryItemSchema{{Key: "open", Label: "打开", Locale: "zh-CN"}}
	items := ApplicationSchemaMergeGeneratedDictionaryItems(defaults, localized)
	if len(items) != 1 || items[0].Label != "打开" || items[0].Description != "Default" || items[0].Color != "green" || items[0].SortOrder != 3 {
		t.Fatalf("items=%#v", items)
	}
}
