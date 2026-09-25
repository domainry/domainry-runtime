package runtimehost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type projectCheckBusinessHandler struct {
	descriptor runtimeext.HandlerDescriptor
}

func (handler projectCheckBusinessHandler) Descriptor() runtimeext.HandlerDescriptor {
	return handler.descriptor
}

func (projectCheckBusinessHandler) Invoke(context.Context, runtimeext.ActionExecution, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func TestCheckProjectAcceptsIdentityOnlyModelWithoutStartingRuntime(t *testing.T) {
	modelFile := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(modelFile, []byte(`{
  "schema_version":"1",
  "project":{"key":"identity_shell","name":"Identity Shell","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{},
  "roles":{"administrator":{"name":"Administrator","permissions":[]}},
  "identity_profiles":{}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	options := minimalHostOptions()
	options.ModelFile = modelFile

	result := CheckProject(options)

	if result.State != projectCheckStateValid || result.ModelContentHash == "" || len(result.Diagnostics) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckProjectReturnsStructuredModelDiagnostic(t *testing.T) {
	modelFile := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(modelFile, []byte(`{"schema_version":"1","reports":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	options := minimalHostOptions()
	options.ModelFile = modelFile

	result := CheckProject(options)

	if result.State != projectCheckStateInvalid || len(result.Diagnostics) != 1 {
		t.Fatalf("result = %#v", result)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != "project_model.decode_failed" || diagnostic.Severity != "error" || diagnostic.Path != "/" || diagnostic.Owner != "project_model" || diagnostic.Category != "schema" || !strings.Contains(diagnostic.Message, `unknown field "reports"`) {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestCheckProjectReportsMissingModelFileConfiguration(t *testing.T) {
	result := CheckProject(minimalHostOptions())

	if result.State != projectCheckStateInvalid || len(result.Diagnostics) != 1 {
		t.Fatalf("result = %#v", result)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != "runtime_project.model_file_required" || diagnostic.Path != "/modelFile" || diagnostic.Owner != "runtime_host" || diagnostic.Remediation == "" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestCheckProjectCrossChecksCodeOwnedDefinitions(t *testing.T) {
	modelFile := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(modelFile, []byte(`{
  "schema_version":"1",
  "project":{"key":"identity_shell","name":"Identity Shell","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{},
  "roles":{"administrator":{"name":"Administrator","permissions":[]}},
  "identity_profiles":{}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	options := minimalHostOptions()
	options.ModelFile = modelFile
	options.ProjectExtensions = func(ConnectorGateway) (runtimeext.ProjectExtensions, error) {
		return runtimeext.ProjectExtensions{BusinessHandlers: []runtimeext.BusinessHandler{projectCheckBusinessHandler{descriptor: runtimeext.HandlerDescriptor{
			ActionKey: "missing.approve", ObjectKey: "missing", Kind: "record",
			InputType: "example.com/project.ApproveInput", OutputType: "example.com/project.ApproveOutput", HandlerRevision: "v1",
		}}}}, nil
	}

	result := CheckProject(options)

	if result.State != projectCheckStateInvalid || len(result.Diagnostics) != 1 {
		t.Fatalf("result = %#v", result)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Code != "project_definition.handler_target_unknown" || diagnostic.Owner != "project_code" || diagnostic.Category != "composition" || diagnostic.Path != "/definitions/handlers/missing.approve/object_key" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestCheckModelDefersCodeOwnedPermissionClosure(t *testing.T) {
	modelFile := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(modelFile, []byte(`{
  "schema_version":"1",
  "project":{"key":"crm","name":"CRM","time_zone":"UTC","initial_workspace_administrator_role":"administrator"},
  "objects":{"customer":{"name":"Customer","description":"","fields":{"name":"text!"}}},
  "roles":{"administrator":{"name":"Administrator","permissions":["customer.approve;all"]}},
  "identity_profiles":{}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	options := minimalHostOptions()
	options.ModelFile = modelFile

	modelResult := CheckModel(options)
	projectResult := CheckProject(options)

	if modelResult.State != projectCheckStateValid || modelResult.ModelContentHash == "" || len(modelResult.Diagnostics) != 0 {
		t.Fatalf("model result = %#v", modelResult)
	}
	if projectResult.State != projectCheckStateInvalid || len(projectResult.Diagnostics) != 1 || projectResult.Diagnostics[0].Code != "project_model.permission_unknown" {
		t.Fatalf("project result = %#v", projectResult)
	}
}
