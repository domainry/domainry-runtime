package appschema

import (
	"context"
	"encoding/json"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestMetadataStorageSchemaBranches(t *testing.T) {
	for _, testCase := range []struct {
		field definitionmodel.FieldSchema
		want  bool
	}{
		{field: definitionmodel.FieldSchema{Unique: true}, want: true},
		{field: definitionmodel.FieldSchema{Type: "relation"}, want: true},
		{field: definitionmodel.FieldSchema{Type: "text"}},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"indexed": true}}, want: true},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"indexed": false}}},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"indexed": " TRUE "}}, want: true},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"indexed": "1"}}, want: true},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"indexed": "false"}}},
		{field: definitionmodel.FieldSchema{Config: map[string]any{"indexed": 1}}},
	} {
		if got := metadataFieldIndexed(testCase.field); got != testCase.want {
			t.Fatalf("field=%#v indexed=%v want=%v", testCase.field, got, testCase.want)
		}
	}
	if metadataTestStorageProfile("mysql").IDColumnType() != "VARCHAR(191)" || metadataTestStorageProfile("sqlite").IDColumnType() != "TEXT" {
		t.Fatal("ID column type mapping failed")
	}
	for _, testCase := range []struct {
		driver string
		field  definitionmodel.FieldSchema
		want   string
	}{
		{"sqlite", definitionmodel.FieldSchema{Type: "integer"}, "INTEGER"},
		{"mysql", definitionmodel.FieldSchema{Type: "integer"}, "BIGINT"},
		{"postgres", definitionmodel.FieldSchema{Type: "integer"}, "BIGINT"},
		{"mysql", definitionmodel.FieldSchema{Type: "number"}, "DOUBLE"},
		{"postgres", definitionmodel.FieldSchema{Type: "number"}, "DOUBLE PRECISION"},
		{"mysql", definitionmodel.FieldSchema{Type: "currency", Config: map[string]any{"precision": 12, "scale": 4}}, "DECIMAL(12,4)"},
		{"postgres", definitionmodel.FieldSchema{Type: "currency"}, "NUMERIC(19,2)"},
		{"sqlite", definitionmodel.FieldSchema{Type: "currency"}, "TEXT"},
		{"sqlite", definitionmodel.FieldSchema{Type: "currency", Config: map[string]any{"precision": 0}}, "TEXT"},
		{"sqlite", definitionmodel.FieldSchema{Type: " percent "}, "TEXT"},
		{"mysql", definitionmodel.FieldSchema{Type: "percent", Config: map[string]any{"precision": 8, "scale": 3}}, "DECIMAL(8,3)"},
		{"postgres", definitionmodel.FieldSchema{Type: "percent"}, "NUMERIC(19,2)"},
		{"sqlite", definitionmodel.FieldSchema{Type: "boolean"}, "INTEGER"}, {"postgres", definitionmodel.FieldSchema{Type: "boolean"}, "BOOLEAN"},
		{"sqlite", definitionmodel.FieldSchema{Type: "json"}, "TEXT"}, {"postgres", definitionmodel.FieldSchema{Type: "json"}, "JSONB"}, {"mysql", definitionmodel.FieldSchema{Type: "json"}, "JSON"},
		{"sqlite", definitionmodel.FieldSchema{Type: "multi_select"}, "TEXT"}, {"postgres", definitionmodel.FieldSchema{Type: "multi_select"}, "TEXT"}, {"mysql", definitionmodel.FieldSchema{Type: "multi_select"}, "TEXT"},
		{"sqlite", definitionmodel.FieldSchema{Type: "file"}, "TEXT"}, {"postgres", definitionmodel.FieldSchema{Type: "file"}, "JSON"}, {"mysql", definitionmodel.FieldSchema{Type: "file_list"}, "JSON"},
		{"mysql", definitionmodel.FieldSchema{Type: "text"}, "TEXT"}, {"postgres", definitionmodel.FieldSchema{Type: "text"}, "TEXT"},
		{"mysql", definitionmodel.FieldSchema{Type: "long_text", Config: map[string]any{"indexed": true}}, "TEXT"},
		{"mysql", definitionmodel.FieldSchema{Type: "text", Config: map[string]any{"indexed": true, "max_length": 128}}, "VARCHAR(128)"},
		{"mysql", definitionmodel.FieldSchema{Type: "text", Config: map[string]any{"indexed": true, "max_length": int64(80)}}, "VARCHAR(80)"},
		{"mysql", definitionmodel.FieldSchema{Type: "text", Config: map[string]any{"indexed": true, "max_length": float64(96)}}, "VARCHAR(96)"},
		{"mysql", definitionmodel.FieldSchema{Type: "text", Config: map[string]any{"indexed": true, "max_length": json.Number("64")}}, "VARCHAR(64)"},
		{"mysql", definitionmodel.FieldSchema{Type: "text", Config: map[string]any{"indexed": true, "max_length": 999}}, "VARCHAR(191)"},
	} {
		profile := metadataTestStorageProfile(testCase.driver)
		if got := profile.FieldColumnType(testCase.field, metadataFieldIndexed(testCase.field)); got != testCase.want {
			t.Fatalf("driver=%s field=%#v got=%s want=%s", testCase.driver, testCase.field, got, testCase.want)
		}
	}
}

func TestSyncManifestSkipsEmptyObjectsAndPropagatesFailure(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	repository := NewApplicationSchemaStore(store)
	if err := repository.SyncManifest(t.Context(), metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: " "}}}); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := repository.SyncManifest(cancelled, metadataTestInstallationScope(), manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "account"}}}); err == nil {
		t.Fatal("expected storage synchronization failure")
	}
}
