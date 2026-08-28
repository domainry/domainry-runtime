package metadata

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestManifestMetadataSeedsLocalizedTextAndPreservesUserOverrides(t *testing.T) {
	store := openStoreForMetadataTest(t)
	defer store.raw.Close()

	manifest := manifestmodel.ManifestSchema{
		TemplateID:    "localized-app",
		Version:       "0.1.0",
		DefaultLocale: "zh-CN",
		Name:          "客户系统",
		I18n: localizationmodel.LocalizedTextMap{
			"en-US": {"name": "Customer System"},
		},
		Objects: []definitionmodel.ObjectSchema{{
			Key:  "customer",
			Name: "客户",
			I18n: localizationmodel.LocalizedTextMap{
				"en-US": {"name": "Customer"},
			},
			Fields: []definitionmodel.FieldSchema{{
				Key:  "status",
				Name: "状态",
				Type: "status",
				I18n: localizationmodel.LocalizedTextMap{
					"en-US": {"name": "Status"},
				},
				Validation: definitionmodel.FieldValidation{Options: []string{"active"}},
				Options: []any{map[string]any{
					"value": "active",
					"label": "活跃",
					"i18n":  map[string]any{"en-US": map[string]any{"label": "Active"}},
				}},
			}},
		}},
		Dictionaries: []metadatamodel.DictionarySchema{{
			Key:  "customer_status",
			Name: "客户状态",
			I18n: localizationmodel.LocalizedTextMap{
				"en-US": {"name": "Customer Status"},
			},
			Items: []metadatamodel.DictionaryItemSchema{{
				Key:   "active",
				Value: "active",
				Label: "活跃",
				I18n: localizationmodel.LocalizedTextMap{
					"en-US": {"label": "Active"},
				},
			}},
		}},
	}
	if err := store.EnsureManifestMetadata(t.Context(), manifest); err != nil {
		t.Fatalf("EnsureManifestMetadata: %v", err)
	}
	loaded, err := store.LoadManifestMetadata(t.Context())
	if err != nil {
		t.Fatalf("LoadManifestMetadata: %v", err)
	}
	if loaded.DefaultLocale != "zh-CN" {
		t.Fatalf("expected default locale to round trip, got %#v", loaded.DefaultLocale)
	}
	defaultValues, err := store.ListLocalizedTexts(t.Context(), principalmodel.InstallationWorkspaceID, metadatamodel.LocalizedTextQuery{Locale: "zh-CN"})
	if err != nil {
		t.Fatalf("ListLocalizedTexts default locale: %v", err)
	}
	assertLocalizedText(t, defaultValues, "app", "app", "name", "客户系统")
	assertLocalizedText(t, defaultValues, "object", "customer", "name", "客户")
	assertLocalizedText(t, defaultValues, "field", "customer.status", "name", "状态")
	assertLocalizedText(t, defaultValues, "field_option", "customer.status.active", "label", "活跃")
	assertLocalizedText(t, defaultValues, "dictionary", "customer_status", "name", "客户状态")
	assertLocalizedText(t, defaultValues, "dictionary_item", "customer_status.active", "label", "活跃")
	values, err := store.ListLocalizedTexts(t.Context(), principalmodel.InstallationWorkspaceID, metadatamodel.LocalizedTextQuery{Locale: "en-US"})
	if err != nil {
		t.Fatalf("ListLocalizedTexts: %v", err)
	}
	assertLocalizedText(t, values, "app", "app", "name", "Customer System")
	assertLocalizedText(t, values, "object", "customer", "name", "Customer")
	assertLocalizedText(t, values, "field", "customer.status", "name", "Status")
	assertLocalizedText(t, values, "field_option", "customer.status.active", "label", "Active")
	assertLocalizedText(t, values, "dictionary", "customer_status", "name", "Customer Status")
	assertLocalizedText(t, values, "dictionary_item", "customer_status.active", "label", "Active")

	if _, err := store.UpsertLocalizedText(t.Context(), principalmodel.InstallationWorkspaceID, metadatamodel.LocalizedTextUpsertRequest{
		EntityType: "field",
		EntityKey:  "customer.status",
		Property:   "name",
		Locale:     "en-US",
		Text:       "Lifecycle Status",
	}); err != nil {
		t.Fatalf("UpsertLocalizedText: %v", err)
	}
	upgraded := manifest
	upgraded.Version = "0.2.0"
	upgraded.Objects[0].Fields[0].I18n = localizationmodel.LocalizedTextMap{"en-US": {"name": "Generated Status"}}
	if err := store.EnsureManifestMetadata(t.Context(), upgraded); err != nil {
		t.Fatalf("EnsureManifestMetadata upgraded: %v", err)
	}
	afterOverride, err := store.ListLocalizedTexts(t.Context(), principalmodel.InstallationWorkspaceID, metadatamodel.LocalizedTextQuery{EntityType: "field", EntityKey: "customer.status", Property: "name", Locale: "en-US"})
	if err != nil {
		t.Fatalf("ListLocalizedTexts after override: %v", err)
	}
	if len(afterOverride) != 1 || afterOverride[0].Text != "Lifecycle Status" || afterOverride[0].SourceKind != "user" {
		t.Fatalf("expected user override to survive generated sync, got %#v", afterOverride)
	}
}

func assertLocalizedText(t *testing.T, values []metadatamodel.LocalizedText, entityType, entityKey, property, text string) {
	t.Helper()
	for _, value := range values {
		if value.EntityType == entityType && value.EntityKey == entityKey && value.Property == property {
			if value.Text != text {
				t.Fatalf("expected %s/%s/%s text %q, got %#v", entityType, entityKey, property, text, value)
			}
			return
		}
	}
	t.Fatalf("localized text %s/%s/%s not found in %#v", entityType, entityKey, property, values)
}

func assertGeneratedManifestField(t *testing.T, manifest manifestmodel.ManifestSchema, objectKey, fieldKey, fieldName string) {
	t.Helper()
	for _, object := range manifest.Objects {
		if object.Key != objectKey {
			continue
		}
		for _, field := range object.Fields {
			if field.Key == fieldKey {
				if field.Name != fieldName {
					t.Fatalf("expected %s.%s name %q, got %#v", objectKey, fieldKey, fieldName, field)
				}
				return
			}
		}
		t.Fatalf("field %s.%s not found in %#v", objectKey, fieldKey, object.Fields)
	}
	t.Fatalf("object %s not found in %#v", objectKey, manifest.Objects)
}
