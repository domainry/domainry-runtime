package runtime

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	"encoding/json"

	"os"
	"path/filepath"
	"strings"
	"testing"

	connector "github.com/domainry/domainry-connector-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	apperror "github.com/domainry/domainry-foundation/apperror"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

func TestApplicationDefinitionCompositionReusesOwnerValidationForSystemDraftCandidates(t *testing.T) {
	application, admin := newMetadataCompositionApp(t, "definition-validation", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}, {Key: "order", Name: "Order"}}, nil)
	defer application.CloseContext(t.Context())
	metadata := application.records.Applications().ApplicationSchema
	definitionsBeforePreview, err := application.metadataBinding.Definitions().List(t.Context(), metadatasdk.DefinitionQuery{ResourceType: "field"})
	if err != nil {
		t.Fatal(err)
	}

	field := definitionmodel.FieldSchema{Key: "customer", Name: "Customer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}, Config: map[string]any{"cardinality": "many_to_many"}}
	result, err := metadata.ValidateApplicationDefinition(t.Context(), "field", "order.customer", metadataCompositionRequest(t, "order", field), admin)
	if err != nil || result.Valid || result.Errors[0].FieldPath != "config.cardinality" {
		t.Fatalf("field result=%#v err=%v", result, err)
	}
	definitions, err := application.metadataBinding.Definitions().List(t.Context(), metadatasdk.DefinitionQuery{ResourceType: "field"})
	if err != nil || !metadataDefinitionsEqual(definitions, definitionsBeforePreview) {
		t.Fatalf("preview changed persisted definitions: before=%#v after=%#v err=%v", definitionsBeforePreview, definitions, err)
	}

	cases := []struct {
		kind, key, objectKey, code string
		value                      any
	}{
		{"action", "order.submit", "order", "backend.action.kind_invalid", definitionmodel.ActionSchema{Key: "order.submit", ObjectKey: "order", Kind: "legacy_step_action", AuditEvent: "order.submitted"}},
		{"connector", "finance", "", "backend.integration.connector.operation_execution_mode_invalid", connectormodel.ConnectorSchema{Key: "finance", Providers: []connectormodel.ConnectorProviderSchema{{Key: "finance"}}, Operations: []connectormodel.ConnectorOperationSchema{{Key: "push", Method: "POST", ExecutionMode: "later", SideEffect: "write"}}}},
		{"report", "order_summary", "", "backend.report.source_object_not_found", reportmodel.ReportSchema{Key: "order_summary", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{SQL: "SELECT source.id AS id FROM missing source LIMIT 1", SourceObjects: []string{"missing"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}}}}},
	}
	for _, tc := range cases {
		request := metadataCompositionRequest(t, tc.objectKey, tc.value)
		preview, err := metadata.ValidateApplicationDefinition(t.Context(), tc.kind, tc.key, request, admin)
		if err != nil || preview.Valid || len(preview.Errors) == 0 {
			t.Fatalf("kind=%s preview=%#v err=%v", tc.kind, preview, err)
		}
		err = metadata.ValidateMetadataCandidate(t.Context(), []appschemamodel.ApplicationDefinitionMutation{{Operation: "create", ResourceType: tc.kind, ResourceKey: tc.key, Request: request}})
		if apperror.CodeOf(err) != "backend.metadata.candidate_invalid" || strings.TrimSpace(apperror.ParamsOf(err)["diagnostic"]) == "" {
			t.Fatalf("kind=%s preview=%s candidate=%s params=%#v err=%v", tc.kind, preview.Errors[0].ErrorCode, apperror.CodeOf(err), apperror.ParamsOf(err), err)
		}
	}
}

func newMetadataCompositionApp(t *testing.T, name string, objects []definitionmodel.ObjectSchema, roles []accessfixture.Bundle) (*Runtime, principalmodel.Principal) {
	return newMetadataCompositionAppWithManifest(t, name, objects, roles, nil)
}

func newMetadataCompositionAppWithManifest(t *testing.T, name string, objects []definitionmodel.ObjectSchema, roles []accessfixture.Bundle, configure func(*manifestmodel.ManifestSchema)) (*Runtime, principalmodel.Principal) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.TemplateID, manifest.Version, manifest.Name, manifest.Objects = name, "1", name, objects
	manifest.Actions, manifest.Workflows, manifest.AutomationRules = nil, nil, nil
	manifest.Reports, manifest.Skills, manifest.Agents, manifest.SeedRecords = nil, nil, nil, nil
	manifest.BusinessLoops, manifest.StateMachines, manifest.ValidationPlan = nil, nil, nil
	manifest.IdentityProfileExtensions, manifest.SensitiveFieldPolicies, manifest.ReportExportControls = nil, nil, nil
	manifest.Roles = nil
	if configure != nil {
		configure(&manifest)
	}
	if len(roles) == 0 {
		dataPermissionKeys := make([]string, 0, len(objects))
		for _, object := range objects {
			dataPermissionKeys = append(dataPermissionKeys, object.Key)
		}
		dataPermissions := make([]accessfixture.DataPolicyFixture, 0, len(dataPermissionKeys))
		for _, objectKey := range dataPermissionKeys {
			dataPermissions = append(dataPermissions, accessfixture.DataPolicyFixture{ObjectKey: objectKey, Scope: "all", Read: true, Write: true})
		}
		roles = []accessfixture.Bundle{{
			Key: "admin",
			Permissions: []string{
				"runtime.appschema.validate_application_definition",
				"runtime.workflows.list_ops_workflow_executions", "runtime.workflows.list_ops_workflow_processes", "runtime.workflows.get_ops_workflow_process",
				"integration.invocations.list", "integration.events.replay",
			},
			DataPolicies: dataPermissions,
		}}
	}
	manifest.SeedRecords = make([]businessseedmodel.SeedRecordSchema, 0, len(objects))
	for _, object := range objects {
		manifest.SeedRecords = append(manifest.SeedRecords, businessseedmodel.SeedRecordSchema{ObjectKey: object.Key, SourceKind: "system", SourceID: name + ":" + object.Key, Data: map[string]any{"__seed_key": name + "." + object.Key}})
	}
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	handlers := runtimeext.NewProjectExtensionRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	application := NewProjectWithIdentity(t.Context(), config.Config{AppLocale: "en-US", IdentityWorkspaceID: "workspace-primary", IdentityAudience: "domainry-runtime", NotificationWorkspaceID: "workspace-primary", NotificationApplicationKey: "domainry-runtime", AuditExportTokenKey: "test-audit-export-signing-key", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), name+".db"), ManifestPath: manifestPath, UploadDir: filepath.Join(t.TempDir(), "uploads")}, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	adminRole := roles[0]
	for _, role := range roles {
		if role.Key == "admin" {
			adminRole = role
			break
		}
	}
	return application, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, adminRole)
}

func metadataDefinitionsEqual(left, right []appschemamodel.ApplicationDefinition) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		leftDefinition, rightDefinition := left[index], right[index]
		if leftDefinition.ResourceType != rightDefinition.ResourceType ||
			leftDefinition.ResourceKey != rightDefinition.ResourceKey ||
			leftDefinition.SchemaVersion != rightDefinition.SchemaVersion ||
			leftDefinition.SchemaHash != rightDefinition.SchemaHash ||
			string(leftDefinition.Payload) != string(rightDefinition.Payload) {
			return false
		}
	}
	return true
}

func metadataCompositionRequest(t *testing.T, objectKey string, value any) appschemamodel.ApplicationDefinitionUpsertRequest {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: objectKey, Payload: payload}
}
