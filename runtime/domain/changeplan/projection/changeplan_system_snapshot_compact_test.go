package projection

import (
	"encoding/json"
	"fmt"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestBusinessSystemCompactIndexExcludesFullSchemaManifestAndRuntimeState(t *testing.T) {
	snapshot := BusinessSystemSnapshot{
		SnapshotHash: "snapshot", SchemaHash: "schema", RuntimeVersion: "runtime",
		AuthoringContractVersion: "authoring-v1", AuthoringContractHash: "contract",
		RuntimeMetadata: RuntimeNativeMetadataModel{ModelVersion: RuntimeNativeMetadataModelVersion, ManifestHash: "manifest", Manifest: &manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "secret-full-manifest"}}}},
		Schema:          appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}}}}},
		ResourceSources: []SystemResourceSource{{ResourceType: "object", ResourceKey: "order", SchemaHash: "order-hash", SourceKind: "builder"}},
		CapabilityKeys:  []string{"schema.object", "schema.field"},
		RuntimeState: BusinessRuntimeStateSnapshot{
			RunningWorkflowProcesses: []WorkflowProcessSummary{{ID: "process-secret"}},
		},
	}
	index := snapshot.CompactIndex()
	if index.CapabilityCount != 2 || index.ResourceCount != 1 || index.ResourceCounts["object"] != 1 || len(index.ResourceCollections) != 1 || index.ResourceCollections[0].CollectionHash == "" || index.RuntimeMetadata.ManifestHash != "manifest" {
		t.Fatalf("compact index lost summary facts: %#v", index)
	}
	if index.Links.ResourcePages == "" || index.Links.ResourceDetail == "" || index.Links.RuntimeStateIndex == "" || index.Links.FullSnapshot == "" {
		t.Fatalf("compact index cannot be progressively traversed: %#v", index.Links)
	}
	raw, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret-full-manifest", "process-secret", `"schema":`, `"runtime_state":`, `"resources":`} {
		if jsonBytesContain(raw, forbidden) {
			t.Fatalf("compact index leaked %q: %s", forbidden, raw)
		}
	}
}

func TestBusinessSystemResourcePagesPreserveDiscoverabilityWithinContextBudget(t *testing.T) {
	resources := make([]SystemResourceSource, 0, 3600)
	for index := 0; index < 3600; index++ {
		resources = append(resources, SystemResourceSource{
			ResourceType: "field", ResourceKey: fmt.Sprintf("field-%04d", index), ObjectKey: fmt.Sprintf("object-%03d", index/36),
			SchemaHash: fmt.Sprintf("hash-%04d", index), SourceKind: "project_json", SourceID: "backend/model/objects.json",
		})
	}
	snapshot := BusinessSystemSnapshot{
		RuntimeVersion: "runtime", AuthoringContractVersion: "authoring-v1", AuthoringContractHash: "contract", SchemaHash: "schema",
		ResourceSources: resources, CapabilityKeys: []string{"schema.field"}, ResourceVisibility: map[string]string{"resource_sources": "summarized"},
	}
	index := snapshot.CompactIndex()
	raw, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 {
		t.Fatalf("default index exceeded the model context budget: bytes=%d body=%s", len(raw), raw)
	}
	if index.ResourceCount != len(resources) || len(index.ResourceCollections) != 1 || index.ResourceCollections[0].ResourceCount != len(resources) {
		t.Fatalf("index lost resource coverage: %#v", index)
	}

	cursor := ""
	discovered := []SystemResourceSource{}
	collectionHash := ""
	for {
		page, valid := ProjectBusinessSystemResourcePage(resources, "field", cursor, 25)
		if !valid || len(page.Items) == 0 || len(page.Items) > 25 {
			t.Fatalf("invalid resource page at cursor %q: %#v", cursor, page)
		}
		if collectionHash == "" {
			collectionHash = page.CollectionHash
		} else if page.CollectionHash != collectionHash {
			t.Fatalf("collection hash changed between pages: %q != %q", page.CollectionHash, collectionHash)
		}
		discovered = append(discovered, page.Items...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(discovered) != len(resources) || collectionHash != index.ResourceCollections[0].CollectionHash {
		t.Fatalf("bounded traversal lost resources: discovered=%d want=%d page_hash=%q index_hash=%q", len(discovered), len(resources), collectionHash, index.ResourceCollections[0].CollectionHash)
	}
	for position, resource := range discovered {
		if resource.ResourceKey != fmt.Sprintf("field-%04d", position) || resource.SchemaHash != fmt.Sprintf("hash-%04d", position) {
			t.Fatalf("resource %d changed during traversal: %#v", position, resource)
		}
	}
}

func TestBusinessSystemResourcePageRejectsUnboundedOrInvalidTraversal(t *testing.T) {
	resources := []SystemResourceSource{{ResourceType: "field", ResourceKey: "status"}}
	for _, request := range []struct {
		resourceType string
		cursor       string
		limit        int
	}{
		{resourceType: "", limit: 25},
		{resourceType: "field", cursor: "invalid", limit: 25},
		{resourceType: "field", cursor: "2", limit: 25},
		{resourceType: "field", limit: BusinessSystemMaximumResourcePageSize + 1},
	} {
		if page, valid := ProjectBusinessSystemResourcePage(resources, request.resourceType, request.cursor, request.limit); valid {
			t.Fatalf("invalid traversal produced page: request=%#v page=%#v", request, page)
		}
	}
}

func TestBusinessSystemResourceDetailReturnsOneDefinition(t *testing.T) {
	snapshot := BusinessSystemSnapshot{
		SnapshotHash: "snapshot",
		Schema: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
			{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}, {Key: "amount"}}},
			{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name"}}},
		}},
		ResourceSources: []SystemResourceSource{
			{ResourceType: "field", ResourceKey: "order.status", ObjectKey: "order", SchemaHash: "status-hash"},
			{ResourceType: "field", ResourceKey: "customer.name", ObjectKey: "customer", SchemaHash: "name-hash"},
		},
	}
	detail, found := snapshot.ResourceDetail("field", "order.status")
	if !found || detail.ResourceHash != "status-hash" || detail.Source == nil || detail.Source.ObjectKey != "order" {
		t.Fatalf("detail=%#v found=%v", detail, found)
	}
	if !jsonBytesContain(detail.Definition, `"key":"status"`) || jsonBytesContain(detail.Definition, "amount") || jsonBytesContain(detail.Definition, "customer") {
		t.Fatalf("resource detail leaked unrelated definitions: %s", detail.Definition)
	}
	if _, found := snapshot.ResourceDetail("field", "missing"); found {
		t.Fatal("missing resource detail was reported as present")
	}
}

func jsonBytesContain(raw []byte, value string) bool {
	for index := 0; index+len(value) <= len(raw); index++ {
		if string(raw[index:index+len(value)]) == value {
			return true
		}
	}
	return false
}
