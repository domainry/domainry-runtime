package appschema

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestApplicationSchemaStoreLoadsPersistedManifest(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationSchemaStore(store)
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "manifest-read", Version: "1", Name: "Manifest read", DefaultLocale: "zh-CN",
		Objects: []definitionmodel.ObjectSchema{{Key: "account", Name: "Account", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text", Config: map[string]any{"definition_object_key": "ignored"}}}, Validations: []definitionmodel.ValidationSchema{{Key: "account.name.required", ObjectKey: "account", Type: "required", FieldKey: "name"}}}},
	}
	if err := repository.SyncManifestProjection(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.LoadManifest(t.Context(), metadataTestInstallationScope())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TemplateID != manifest.TemplateID || loaded.Version != "1" || len(loaded.Objects) != 1 || len(loaded.Objects[0].Fields) != 1 || len(loaded.Objects[0].Validations) != 1 || len(loaded.SchedulerDefinitions) != 0 {
		t.Fatalf("loaded=%+v", loaded)
	}
}

func TestMetadataSQLAndSeedHelpers(t *testing.T) {
	for _, tc := range []struct {
		input any
		want  any
	}{{float32(1.5), float64(1.5)}, {int(2), float64(2)}, {int64(3), float64(3)}, {"x", "x"}} {
		if got := dbValue(tc.input); got != tc.want {
			t.Fatalf("input=%v got=%v want=%v", tc.input, got, tc.want)
		}
	}
	if len(nonNilMap(nil)) != 0 {
		t.Fatal("nil map was not normalized")
	}
	original := map[string]any{"x": 1}
	if nonNilMap(original)["x"] != 1 {
		t.Fatal("map changed")
	}
	if nullableText(" ") != nil || nullableText("x") != "x" {
		t.Fatal("nullable text mismatch")
	}
	if validationMetadataKey("", 0, definitionmodel.ValidationSchema{}) != ".validation.1" {
		t.Fatal("validation fallback mismatch")
	}
	if got := validationMetadataKey("account", 0, definitionmodel.ValidationSchema{Type: "compare", FieldKey: "a", Fields: []string{"b", "c"}}); got != "account.compare.a.b_c" {
		t.Fatalf("key=%s", got)
	}
	if metadataJoinedKey("", "right") != "right" || metadataJoinedKey("left", "") != "left" || metadataJoinedKey("left", "right") != "left.right" {
		t.Fatal("joined key mismatch")
	}
	if metadataFieldObjectKey(definitionmodel.FieldSchema{Config: map[string]any{"_definition_object_key": nil, "definition_object_key": "account"}}) != "account" {
		t.Fatal("field object fallback mismatch")
	}
	if metadataMapString(map[string]any{"a": nil, "b": "value"}, "a", "b") != "value" {
		t.Fatal("map string fallback mismatch")
	}
}
