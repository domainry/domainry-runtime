package businessseed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestBuildManifestBusinessSeedRowsGeneratesCoherentRelationshipGraph(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "sales",
		Objects: []definitionmodel.ObjectSchema{
			{Key: "customer", Name: "Customer", Fields: []definitionmodel.FieldSchema{
				{Key: "name", Name: "Name", Type: "text", Required: true},
				{Key: "owner", Name: "Owner", Type: "user", Required: true},
			}},
			{Key: "sales_order", Name: "Sales Order", Fields: []definitionmodel.FieldSchema{
				{Key: "order_number", Name: "Order Number", Type: "text", Required: true, Unique: true},
				{Key: "customer", Name: "Customer", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: "customer"}},
				{Key: "status", Name: "Status", Type: "select", Required: true, Validation: definitionmodel.FieldValidation{Options: []string{"cancelled", "draft", "approved"}}},
				{Key: "amount", Name: "Amount", Type: "number", Required: true},
			}},
		},
	}
	rows, err := BuildManifestBusinessSeedRows(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ObjectKey != "customer" || rows[1].ObjectKey != "sales_order" {
		t.Fatalf("rows=%#v", rows)
	}
	if rows[0].SourceKind != runtimeGeneratedBusinessSeedSourceKind || rows[1].OwnerUserID != "admin" {
		t.Fatalf("generated provenance=%#v", rows)
	}
	customer := generatedSeedTestData(t, rows[0])
	if customer["name"] != "Acme Trading" || customer["owner"] != "admin" {
		t.Fatalf("customer=%#v", customer)
	}
	order := generatedSeedTestData(t, rows[1])
	if order["customer"] != "$record:runtime_baseline_customer" || order["status"] != "draft" || order["order_number"] != "SO-001" || order["amount"] != float64(1000) {
		t.Fatalf("order=%#v", order)
	}
}

func TestBuildManifestBusinessSeedRowsResolvesDictionaryBackedSelects(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{
			Key: "account", Fields: []definitionmodel.FieldSchema{{
				Key: "industry", Type: "select", Required: true, Config: map[string]any{"dictionary_key": "industry_sector"},
			}},
		}},
		Dictionaries: []appschemamodel.DictionarySchema{{
			Key: "industry_sector", Items: []appschemamodel.DictionaryItemSchema{
				{Key: "technology", Value: "technology", Label: "Technology", SortOrder: 2},
				{Key: "finance", Value: "finance", Label: "Finance", SortOrder: 1},
			},
		}},
	}
	rows, err := BuildManifestBusinessSeedRows(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%#v", rows)
	}
	data := generatedSeedTestData(t, rows[0])
	if data["industry"] != "finance" {
		t.Fatalf("dictionary-backed select=%#v", data)
	}
}

func TestBuildManifestBusinessSeedRowsPreservesExplicitRowsAndOnlyFillsUncoveredObjects(t *testing.T) {
	manifest := businessSeedManifest()
	manifest.SeedRecords = []businessseedmodel.SeedRecordSchema{{ObjectKey: "parent", Data: map[string]any{"__seed_key": "curated-parent", "name": "Curated Parent"}}}
	rows, err := BuildManifestBusinessSeedRows(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Key != "curated-parent" || rows[1].ObjectKey != "child" || rows[1].SourceKind != runtimeGeneratedBusinessSeedSourceKind {
		t.Fatalf("rows=%#v", rows)
	}
}

func TestBuildManifestBusinessSeedRowsRejectsUnsafeRequiredValues(t *testing.T) {
	for _, object := range []definitionmodel.ObjectSchema{
		{Key: "broken_relation", Fields: []definitionmodel.FieldSchema{{Key: "owner_unit", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: "identity_organization_unit"}}}},
		{Key: "broken_pattern", Fields: []definitionmodel.FieldSchema{{Key: "opaque", Type: "text", Required: true, Config: map[string]any{"pattern": "^impossible-literal$"}}}},
	} {
		_, err := BuildManifestBusinessSeedRows(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}})
		if err == nil || !strings.Contains(err.Error(), object.Key) {
			t.Fatalf("object=%s err=%v", object.Key, err)
		}
	}
}

func TestBuildManifestBusinessSeedRowsSupportsBoundedMonthAndClockPatterns(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "business_period",
		Fields: []definitionmodel.FieldSchema{
			{Key: "billing_month", Type: "text", Required: true, Validation: definitionmodel.FieldValidation{Pattern: `^[0-9]{4}-(0[1-9]|1[0-2])$`}},
			{Key: "open_time", Type: "text", Required: true, Validation: definitionmodel.FieldValidation{Pattern: `^([01]\d|2[0-3]):[0-5]\d$`}},
		},
	}}}
	rows, err := BuildManifestBusinessSeedRows(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data := generatedSeedTestData(t, rows[0])
	if data["billing_month"] != "2000-01" || data["open_time"] != "09:00" {
		t.Fatalf("structured text baseline=%#v", data)
	}
}

func TestSyncManifestBusinessSeedsIsPerTableAndRestartIdempotent(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{Key: "parent", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}},
		{Key: "child", Fields: []definitionmodel.FieldSchema{
			{Key: "parent", Type: "relation", Required: true, Validation: definitionmodel.FieldValidation{Target: "parent"}},
			{Key: "name", Type: "text", Required: true},
		}},
	}}
	rows, err := BuildManifestBusinessSeedRows(manifest)
	if err != nil {
		t.Fatal(err)
	}
	repository := &businessSeedRecordProbe{existing: map[string][]recordmodel.Record{
		"parent": {{ID: "parent-existing", Data: map[string]any{"name": "Existing Parent"}}},
	}}
	if err := SyncManifestBusinessSeeds(t.Context(), repository, manifest, rows); err != nil {
		t.Fatal(err)
	}
	if len(repository.inserted) != 1 || repository.insertOrder[0] != "child" || repository.inserted[0].Data["parent"] != "parent-existing" {
		t.Fatalf("inserted=%#v order=%v", repository.inserted, repository.insertOrder)
	}

	repository.existing["child"] = append([]recordmodel.Record(nil), repository.inserted...)
	repository.inserted = nil
	repository.insertOrder = nil
	if err := SyncManifestBusinessSeeds(t.Context(), repository, manifest, rows); err != nil {
		t.Fatal(err)
	}
	if len(repository.inserted) != 0 {
		t.Fatalf("restart inserted duplicates: %#v", repository.inserted)
	}
}

func TestSyncManifestBusinessSeedsWithReferenceResolverSkipsExistingExternalReferencesOnRestart(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "employee_profile",
		Fields: []definitionmodel.FieldSchema{{
			Key: "identity_user_id", Type: "user", Required: true,
		}},
	}}}
	repository := &businessSeedRecordProbe{existing: map[string][]recordmodel.Record{
		"employee_profile": {{ID: "employee-existing", Data: map[string]any{"identity_user_id": "admin"}}},
	}}
	resolverCalls := 0
	resolver := BaselineReferenceResolverFunc(func(context.Context, BaselineReferenceRequest) (string, error) {
		resolverCalls++
		return "", errBusinessSeedProbe
	})
	if err := SyncManifestBusinessSeedsWithReferenceResolver(t.Context(), repository, manifest, "workspace-a", resolver); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 0 || len(repository.inserted) != 0 {
		t.Fatalf("resolver_calls=%d inserted=%#v", resolverCalls, repository.inserted)
	}
}

func TestSyncManifestBusinessSeedsConvergesConcurrentDeterministicInsert(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}},
	}}}
	rows, err := BuildManifestBusinessSeedRows(manifest)
	if err != nil {
		t.Fatal(err)
	}
	repository := &businessSeedRecordProbe{storedErr: map[string]error{"customer": errBusinessSeedProbe}}
	if err := SyncManifestBusinessSeeds(t.Context(), repository, manifest, rows); err != nil {
		t.Fatalf("concurrent deterministic insert did not converge: %v", err)
	}
	if len(repository.inserted) != 1 || repository.inserted[0].ID != "customer_runtime_baseline_customer" {
		t.Fatalf("inserted=%#v", repository.inserted)
	}
}

func TestGeneratedBusinessSeedsCoverManifestFixturesWithoutModelSeedData(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "domain", "manifest", "testdata", "manifests", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			manifest := manifestmodel.ManifestSchema{}
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.SeedRecords = nil
			rows, err := BuildManifestBusinessSeedRows(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != len(manifest.Objects) {
				t.Fatalf("generated rows=%d objects=%d", len(rows), len(manifest.Objects))
			}
		})
	}
}

func generatedSeedTestData(t *testing.T, row manifestBusinessSeedRow) map[string]any {
	t.Helper()
	data := map[string]any{}
	if err := json.Unmarshal([]byte(row.DataJSON), &data); err != nil {
		t.Fatal(err)
	}
	return data
}
