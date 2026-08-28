package runtime

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"encoding/json"

	"os"
	"path/filepath"
	"strings"
	"testing"

	connector "github.com/domainry/domainry-connector-sdk"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

func TestMetadataDefinitionCompositionReusesOwnerValidationForSystemDraftCandidates(t *testing.T) {
	application, admin := newMetadataCompositionApp(t, "definition-validation", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}, {Key: "order", Name: "Order"}}, nil)
	defer application.CloseContext(t.Context())
	metadata := application.records.Applications().Metadata
	definitionsBeforePreview, err := metadata.ListMetadataDefinitions(t.Context(), "field", "default", admin)
	if err != nil {
		t.Fatal(err)
	}

	field := definitionmodel.FieldSchema{Key: "customer", Name: "Customer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}, Config: map[string]any{"cardinality": "many_to_many"}}
	result, err := metadata.ValidateMetadataDefinition(t.Context(), "field", "order.customer", metadataCompositionRequest(t, "order", field), admin)
	if err != nil || result.Valid || result.Errors[0].FieldPath != "config.cardinality" {
		t.Fatalf("field result=%#v err=%v", result, err)
	}
	definitions, err := metadata.ListMetadataDefinitions(t.Context(), "field", "default", admin)
	if err != nil || !metadataDefinitionsEqual(definitions, definitionsBeforePreview) {
		t.Fatalf("preview changed persisted definitions: before=%#v after=%#v err=%v", definitionsBeforePreview, definitions, err)
	}

	cases := []struct {
		kind, key, objectKey, code string
		value                      any
	}{
		{"action", "order.submit", "order", "backend.action.kind_invalid", definitionmodel.ActionSchema{Key: "order.submit", ObjectKey: "order", Kind: "legacy_step_action", RequiresPermission: "order.update", AuditEvent: "order.submitted"}},
		{"connector", "finance", "", "backend.integration.connector.operation_execution_mode_invalid", integrationmodel.ConnectorSchema{Key: "finance", Type: "http", Provider: "finance", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "push", Method: "POST", ExecutionMode: "later", SideEffect: "write"}}}},
		{"report", "order_summary", "", "backend.report.source_object_not_found", reportmodel.ReportSchema{Key: "order_summary", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "missing", Alias: "missing"}}}},
	}
	for _, tc := range cases {
		request := metadataCompositionRequest(t, tc.objectKey, tc.value)
		preview, err := metadata.ValidateMetadataDefinition(t.Context(), tc.kind, tc.key, request, admin)
		if err != nil || preview.Valid || len(preview.Errors) == 0 {
			t.Fatalf("kind=%s preview=%#v err=%v", tc.kind, preview, err)
		}
		err = metadata.ValidateMetadataCandidate(t.Context(), []metadatamodel.MetadataDefinitionMutation{{Operation: "create", ResourceType: tc.kind, ResourceKey: tc.key, Request: request}})
		if apperror.CodeOf(err) != "backend.change_plan.candidate_invalid" || strings.TrimSpace(apperror.ParamsOf(err)["diagnostic"]) == "" {
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
	manifest.Views, manifest.Actions, manifest.Workflows, manifest.AutomationRules = nil, nil, nil, nil
	manifest.Reports, manifest.EntryPoints, manifest.Skills, manifest.Agents, manifest.SeedRecords = nil, nil, nil, nil, nil
	manifest.BusinessLoops, manifest.StateMachines, manifest.ValidationPlan = nil, nil, nil
	manifest.IdentityProfileExtensions, manifest.SensitiveFieldPolicies, manifest.ReportExportControls = nil, nil, nil
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
			dataPermissions = append(dataPermissions, accessfixture.DataPolicyFixture{ObjectKey: objectKey, Scope: "all_records", Read: true, Write: true})
		}
		dataPermissions = append(dataPermissions,
			accessfixture.DataPolicyFixture{ObjectKey: "job_definition", Scope: "all_records", Read: true, Write: true},
			accessfixture.DataPolicyFixture{ObjectKey: "job_run", Scope: "all_records", Read: true, Write: true},
			accessfixture.DataPolicyFixture{ObjectKey: "job_dead_letter", Scope: "all_records", Read: true, Write: true},
		)
		roles = []accessfixture.Bundle{{
			Key: "admin",
			Permissions: []string{
				"workspace.admin", "metadata.read", "metadata.write",
				"scheduler.definition.read", "scheduler.definition.write", "scheduler.command",
				"job_definition.read", "job_definition.create", "job_definition.update", "job_definition.delete",
				"job_run.read", "job_run.update", "job_dead_letter.read", "job_dead_letter.update",
				"ops.workflow.read", "workflow.process.read", "workflow.process.operate",
				"integration.audit.view", "integration.retry",
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
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	application := NewProjectWithIdentity(t.Context(), config.Config{AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), name+".db"), ManifestPath: manifestPath, UploadDir: filepath.Join(t.TempDir(), "uploads")}, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory())
	adminRole := roles[0]
	for _, role := range roles {
		if role.Key == "admin" {
			adminRole = role
			break
		}
	}
	return application, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}, adminRole)
}

func metadataDefinitionsEqual(left, right []metadatamodel.MetadataDefinition) bool {
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

func metadataCompositionRequest(t *testing.T, objectKey string, value any) metadatamodel.MetadataDefinitionUpsertRequest {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return metadatamodel.MetadataDefinitionUpsertRequest{ObjectKey: objectKey, SourceKind: "user", Payload: payload}
}
