package businesssystem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"

	"github.com/domainry/domainry-foundation/requestcontext"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type businessSystemHandlerDefinitions struct{ values []metadatasdk.Definition }

func (definitions businessSystemHandlerDefinitions) List(_ context.Context, query metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	result := []metadatasdk.Definition{}
	for _, definition := range definitions.values {
		if query.ResourceType == "" || query.ResourceType == definition.ResourceType {
			result = append(result, definition)
		}
	}
	return result, nil
}

func (definitions businessSystemHandlerDefinitions) Get(_ context.Context, resourceType, resourceKey string) (metadatasdk.Definition, bool, error) {
	for _, definition := range definitions.values {
		if definition.ResourceType == resourceType && definition.ResourceKey == resourceKey {
			return definition, true, nil
		}
	}
	return metadatasdk.Definition{}, false, nil
}

func (definitions businessSystemHandlerDefinitions) Snapshot(context.Context) (metadatasdk.DefinitionSnapshot, error) {
	return metadatasdk.DefinitionSnapshot{Definitions: append([]metadatasdk.Definition(nil), definitions.values...)}, nil
}

func businessSystemHandlerApplication(featureErr error) *businesssystemapplication.BusinessSystemApplicationService {
	return businessSystemHandlerApplicationWithDefinitions(featureErr, businessSystemHandlerDefinitions{})
}

func businessSystemHandlerApplicationWithDefinitions(featureErr error, definitions metadatasdk.Definitions) *businesssystemapplication.BusinessSystemApplicationService {
	schema := appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "schema-hash"}
	return businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{
		FeaturePermissions: func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
			return recordcontract.RecordFeaturePermissionSnapshot{}, featureErr
		},
		SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
			return schema
		},
		Definitions: definitions,
		Runtime: businesssystemapplication.BusinessSystemRuntimeProjectionDependencies{
			WorkflowProcesses: func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
				return []workflowmodel.WorkflowProcessInstance{}, nil
			},
			AutomationRules: func(context.Context, principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error) {
				return []automationmodel.AutomationRuleSchema{}, nil
			},
			AutomationExecutions: func(context.Context, automationmodel.AutomationExecutionFilter, principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error) {
				return automationprojection.AutomationExecutionHistory{}, nil
			},
			ConnectorCatalog: func(context.Context, principalmodel.Principal) ([]connectormodel.ConnectorSchema, error) {
				return []connectormodel.ConnectorSchema{}, nil
			},
			IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationsdk.Connection, error) {
				return []integrationsdk.Connection{}, nil
			},
			PublicationHandoff: func(context.Context, string, string, int, principalmodel.Principal) ([]publicationmodel.Message, error) {
				return []publicationmodel.Message{}, nil
			},
			SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
				return schema
			},
			ListRecords: func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
				return recordmodel.RecordPageResult{}, nil
			},
		},
	})
}

func businessSystemHandlerForTest(t *testing.T, principal principalmodel.Principal, featureErr error) *BusinessSystemHandler {
	t.Helper()
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "template", Version: "1.0.0", SourceBlueprintID: "blueprint",
	}
	handler := NewBusinessSystemHandler(BusinessSystemDependencies{
		Service:   businessSystemHandlerApplication(featureErr),
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		RuntimeMetadata: func() RuntimeMetadata {
			return RuntimeMetadata{ServiceKind: "runtime", RuntimeVersion: "test", APIContractVersion: "v1", APIContractHash: "api-hash", ManifestHash: "manifest-hash", Manifest: manifest}
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "service error", http.StatusInternalServerError)
		},
	})
	return handler
}

func businessSystemRequest(t *testing.T) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/business-system/snapshot", nil)
	return request.WithContext(requestcontext.WithWorkspaceID(request.Context(), principalmodel.InstallationWorkspaceID))
}

func TestBuilderSnapshotPrincipalIsRuntimeOwnedInstallationAuthority(t *testing.T) {
	fallback := builderSnapshotPrincipal(manifestmodel.ManifestSchema{})
	if !fallback.Known || fallback.UserID != "builder-snapshot" || fallback.RoleKey != "builder-snapshot" || fallback.WorkspaceID != principalmodel.InstallationWorkspaceID || fallback.SystemScope.Kind != principalmodel.SystemScopeInstallation || !fallback.SystemScope.Valid() || !fallback.HasExactPermission(businesssystemapplication.ActionBusinessSystemSnapshot) {
		t.Fatalf("fallback principal=%#v", fallback)
	}
	if withManifest := builderSnapshotPrincipal(manifestmodel.ManifestSchema{TemplateID: "ignored"}); withManifest.UserID != fallback.UserID || withManifest.RoleKey != fallback.RoleKey {
		t.Fatalf("manifest changed Runtime system authority: %#v", withManifest)
	}
}

func TestRuntimeAuthoringValidationDrivesTrustedLifecycleCallbacks(t *testing.T) {
	principal := principalmodel.NewSystemPrincipal("builder", principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "runtime authoring validation"), businesssystemapplication.ActionBusinessSystemSnapshot)
	validation := businesssystemapplication.NewRuntimeAuthoringValidationApplicationService(businesssystemapplication.RuntimeAuthoringValidationDependencies{
		CurrentManifest: func(context.Context, principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
			return manifestmodel.ManifestSchema{SchemaVersion: "2", TemplateID: "direct", Version: "configuring", Objects: []definitionmodel.ObjectSchema{}}, nil
		},
		CurrentSnapshot: func(context.Context, principalmodel.Principal) (changeplanprojection.BusinessSystemSnapshot, error) {
			return businessSystemCompleteValidationSnapshot(), nil
		},
		ValidateDefinitions: func(context.Context, []connectormodel.ConnectorSchema) error { return nil },
	})
	events := []string{}
	handler := NewBusinessSystemHandler(BusinessSystemDependencies{
		Validation: validation, Principal: func(*http.Request) principalmodel.Principal { return principal },
		BeginValidation: func(task string) error { events = append(events, "begin:"+task); return nil },
		CompleteValidation: func(task, hash string, valid bool) error {
			events = append(events, fmt.Sprintf("complete:%s:%t:%t", task, hash != "", valid))
			return nil
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "service error", http.StatusInternalServerError)
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return false
			}
			return true
		},
	})
	body := []byte(`{"coverage":{"requirements":[{"requirement_id":"order-management","capability_keys":["schema.object"],"resources":[{"resource_type":"object","resource_key":"order"}],"scenario_ids":["order.create.success"]}]}}`)
	request := httptest.NewRequest(http.MethodPost, "/business-system/validation", bytes.NewReader(body))
	ctx := operationscontract.WithBuilderTaskID(request.Context(), "task-1")
	response := httptest.NewRecorder()
	handler.validateRuntimeAuthoring(response, request.WithContext(ctx))
	if response.Code != http.StatusOK || len(events) != 2 || events[0] != "begin:task-1" || events[1] != "complete:task-1:true:false" {
		t.Fatalf("status=%d events=%#v body=%s", response.Code, events, response.Body.String())
	}
	var report map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil || report["coverage"].(map[string]any)["status"] != "complete" {
		t.Fatalf("coverage was not forwarded: report=%#v err=%v", report, err)
	}
}

func businessSystemCompleteValidationSnapshot() changeplanprojection.BusinessSystemSnapshot {
	visibility := map[string]string{}
	for _, category := range []string{
		"schema", "effective_permissions", "resource_sources",
		"runtime_state.automation", "runtime_state.integrations", "runtime_state.reports", "runtime_state.scheduler",
		"seed_records",
	} {
		visibility[category] = "visible"
	}
	snapshot := changeplanprojection.BusinessSystemSnapshot{
		SnapshotVersion: changeplanprojection.BusinessSystemSnapshotVersion, ResourceVisibility: visibility,
		CapabilityKeys:  []string{"schema.object"},
		ResourceSources: []changeplanprojection.SystemResourceSource{{ResourceType: "object", ResourceKey: "order", SourceKind: "builder_v4"}},
	}
	snapshot.Finalize()
	return snapshot
}

func TestBusinessSystemSnapshotIncludesRuntimeMetadata(t *testing.T) {
	principal := principalmodel.NewSystemPrincipal("builder", principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "snapshot test"), businesssystemapplication.ActionBusinessSystemSnapshot)
	handler := businessSystemHandlerForTest(t, principal, nil)
	snapshot, err := handler.Snapshot(businessSystemRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RuntimeMetadata.ServiceKind != "runtime" || snapshot.RuntimeMetadata.Manifest == nil || snapshot.RuntimeMetadata.Manifest.TemplateID != "template" {
		t.Fatalf("snapshot metadata=%#v", snapshot.RuntimeMetadata)
	}
	if snapshot.SnapshotHash == "" {
		t.Fatal("snapshot hash is empty")
	}
}

func TestBusinessSystemSnapshotPropagatesOwnerFailures(t *testing.T) {
	limited := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: principalmodel.InstallationWorkspaceID}}
	want := errors.New("feature permissions unavailable")
	handler := businessSystemHandlerForTest(t, limited, want)
	if _, err := handler.Snapshot(businessSystemRequest(t)); !errors.Is(err, want) {
		t.Fatalf("Snapshot() error=%v want=%v", err, want)
	}
}

func TestBusinessSystemSnapshotHTTPResponseAndConditionalRequest(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: principalmodel.InstallationWorkspaceID}}
	handler := businessSystemHandlerForTest(t, principal, nil)

	response := httptest.NewRecorder()
	handler.businessSystemSnapshot(response, businessSystemRequest(t))
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" || response.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" {
		t.Fatalf("response code=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var compact map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &compact); err != nil || compact["version"] != changeplanprojection.BusinessSystemSnapshotIndexVersion || compact["snapshot_hash"] == "" {
		t.Fatalf("default snapshot is not compact index: body=%s err=%v", response.Body.String(), err)
	}
	for _, forbidden := range []string{"schema", "runtime_state", "effective_permissions", "seed_records"} {
		if _, leaked := compact[forbidden]; leaked {
			t.Fatalf("compact snapshot leaked %q: %s", forbidden, response.Body.String())
		}
	}
	metadata, _ := compact["runtime_metadata"].(map[string]any)
	if _, leaked := metadata["manifest"]; leaked {
		t.Fatalf("compact snapshot leaked manifest: %s", response.Body.String())
	}

	request := businessSystemRequest(t)
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	handler.businessSystemSnapshot(notModified, request)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional response code=%d body=%q", notModified.Code, notModified.Body.String())
	}

	fullRequest := businessSystemRequest(t)
	fullRequest.URL.RawQuery = "projection=full"
	full := httptest.NewRecorder()
	handler.businessSystemSnapshot(full, fullRequest)
	var expanded map[string]any
	if full.Code != http.StatusOK || json.Unmarshal(full.Body.Bytes(), &expanded) != nil || expanded["schema"] == nil || expanded["runtime_state"] == nil {
		t.Fatalf("explicit full snapshot was not expanded: status=%d body=%s", full.Code, full.Body.String())
	}
	if full.Header().Get("ETag") == response.Header().Get("ETag") {
		t.Fatal("compact and full representations shared an ETag")
	}

	want := errors.New("snapshot unavailable")
	handler = businessSystemHandlerForTest(t, principal, want)
	indexWithoutFullProjection := httptest.NewRecorder()
	handler.businessSystemSnapshot(indexWithoutFullProjection, businessSystemRequest(t))
	if indexWithoutFullProjection.Code != http.StatusOK {
		t.Fatalf("default index loaded full projection: status=%d body=%q", indexWithoutFullProjection.Code, indexWithoutFullProjection.Body.String())
	}
	failureRequest := businessSystemRequest(t)
	failureRequest.URL.RawQuery = "projection=full"
	failure := httptest.NewRecorder()
	handler.businessSystemSnapshot(failure, failureRequest)
	if failure.Code != http.StatusInternalServerError {
		t.Fatalf("full projection failure status=%d body=%q", failure.Code, failure.Body.String())
	}
}

func TestBusinessSystemSnapshotProgressiveDiscoveryPreservesModelFacts(t *testing.T) {
	definitions := businessSystemHandlerDefinitions{values: []metadatasdk.Definition{
		{ResourceType: "field", ResourceKey: "order.amount", ObjectKey: "order", Name: "Amount", SchemaHash: "amount-hash", SourceKind: "project_json", Payload: []byte(`{"key":"amount","type":"number"}`)},
		{ResourceType: "field", ResourceKey: "order.status", ObjectKey: "order", Name: "Status", SchemaHash: "status-hash", SourceKind: "project_json", Payload: []byte(`{"key":"status","type":"select"}`)},
	}}
	handler := NewBusinessSystemHandler(BusinessSystemDependencies{
		Service: businessSystemHandlerApplicationWithDefinitions(nil, definitions),
		Principal: func(*http.Request) principalmodel.Principal {
			return builderSnapshotPrincipal(manifestmodel.ManifestSchema{})
		},
		RuntimeMetadata: func() RuntimeMetadata {
			return RuntimeMetadata{ServiceKind: "runtime", RuntimeVersion: "test", APIContractVersion: "v1", APIContractHash: "api-hash", ManifestHash: "manifest-hash", Manifest: manifestmodel.ManifestSchema{TemplateID: "template", Version: "1.0.0"}}
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		},
	})

	indexResponse := httptest.NewRecorder()
	handler.businessSystemSnapshot(indexResponse, businessSystemRequest(t))
	var index changeplanprojection.BusinessSystemSnapshotIndex
	if indexResponse.Code != http.StatusOK || json.Unmarshal(indexResponse.Body.Bytes(), &index) != nil {
		t.Fatalf("index status=%d body=%s", indexResponse.Code, indexResponse.Body.String())
	}
	if index.ResourceCount != 2 || index.ResourceCounts["field"] != 2 || len(index.ResourceCollections) != 1 || index.Links.ResourcePages == "" || index.Links.ResourceDetail == "" {
		t.Fatalf("index does not explain bounded traversal: %#v", index)
	}
	var indexWire map[string]any
	_ = json.Unmarshal(indexResponse.Body.Bytes(), &indexWire)
	if _, leaked := indexWire["resources"]; leaked {
		t.Fatalf("default index leaked workspace-wide resources: %s", indexResponse.Body.String())
	}

	pageRequest := businessSystemRequest(t)
	pageRequest.URL.RawQuery = "projection=resources&resource_type=field&limit=1"
	pageResponse := httptest.NewRecorder()
	handler.businessSystemSnapshot(pageResponse, pageRequest)
	var page changeplanprojection.BusinessSystemResourcePage
	if pageResponse.Code != http.StatusOK || json.Unmarshal(pageResponse.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Total != 2 || page.NextCursor == "" {
		t.Fatalf("page status=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}

	detailRequest := businessSystemRequest(t)
	detailRequest.URL.RawQuery = "projection=resource&resource_type=field&resource_key=" + page.Items[0].ResourceKey
	detailResponse := httptest.NewRecorder()
	handler.businessSystemSnapshot(detailResponse, detailRequest)
	var detail changeplanprojection.BusinessSystemResourceDetail
	if detailResponse.Code != http.StatusOK || json.Unmarshal(detailResponse.Body.Bytes(), &detail) != nil || detail.ResourceHash != page.Items[0].SchemaHash || len(detail.Definition) == 0 {
		t.Fatalf("detail status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}

	runtimeIndexRequest := businessSystemRequest(t)
	runtimeIndexRequest.URL.RawQuery = "projection=runtime-index"
	runtimeIndexResponse := httptest.NewRecorder()
	handler.businessSystemSnapshot(runtimeIndexResponse, runtimeIndexRequest)
	var runtimeIndex changeplanprojection.BusinessSystemRuntimeStateIndex
	if runtimeIndexResponse.Code != http.StatusOK || json.Unmarshal(runtimeIndexResponse.Body.Bytes(), &runtimeIndex) != nil || runtimeIndex.Version != changeplanprojection.BusinessSystemRuntimeStateIndexVersion {
		t.Fatalf("runtime index status=%d body=%s", runtimeIndexResponse.Code, runtimeIndexResponse.Body.String())
	}
}
