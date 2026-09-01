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

type businessSystemHandlerDefinitions struct{}

func (businessSystemHandlerDefinitions) List(context.Context, metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	return []metadatasdk.Definition{}, nil
}

func (businessSystemHandlerDefinitions) Get(context.Context, string, string) (metadatasdk.Definition, bool, error) {
	return metadatasdk.Definition{}, false, nil
}

func (businessSystemHandlerDefinitions) Snapshot(context.Context) (metadatasdk.DefinitionSnapshot, error) {
	return metadatasdk.DefinitionSnapshot{}, nil
}

func businessSystemHandlerApplication(featureErr error) *businesssystemapplication.BusinessSystemApplicationService {
	schema := appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "schema-hash"}
	return businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{
		FeaturePermissions: func(context.Context, principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
			return recordcontract.RecordFeaturePermissionSnapshot{}, featureErr
		},
		SchemaForPrincipal: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
			return schema
		},
		Definitions: businessSystemHandlerDefinitions{},
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
	request := httptest.NewRequest(http.MethodGet, "/domain-system-snapshot", nil)
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
	body := []byte(`{"coverage":{"version":"runtime-authoring-coverage-v1","requirements":[{"requirement_id":"order-management","capability_keys":["schema.object"],"resources":[{"resource_type":"object","resource_key":"order"}],"scenario_ids":["order.create.success"]}]}}`)
	request := httptest.NewRequest(http.MethodPost, "/domain-system-validation", bytes.NewReader(body))
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

	request := businessSystemRequest(t)
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	handler.businessSystemSnapshot(notModified, request)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional response code=%d body=%q", notModified.Code, notModified.Body.String())
	}

	want := errors.New("snapshot unavailable")
	handler = businessSystemHandlerForTest(t, principal, want)
	failure := httptest.NewRecorder()
	handler.businessSystemSnapshot(failure, businessSystemRequest(t))
	if failure.Code != http.StatusInternalServerError {
		t.Fatalf("failure status=%d body=%q", failure.Code, failure.Body.String())
	}
}
