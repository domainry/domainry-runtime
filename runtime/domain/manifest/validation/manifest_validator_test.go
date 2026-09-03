package validation

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestValidateManifestAcceptsRuntimeFixtures(t *testing.T) {
	fixtures := []string{
		"domain-only-minimal.json",
		"scheduler-ops-minimal.json",
		"crm-customer-360.json",
		"restaurant-kitchen.json",
		"erp-inventory.json",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			manifest := loadFixtureManifest(t, fixture)
			if err := ValidateManifest(manifest); err != nil {
				t.Fatalf("ValidateManifest() error = %v", err)
			}
		})
	}
}

func TestValidateManifestDoesNotRequireSeedRecords(t *testing.T) {
	manifest := loadFixtureManifest(t, "domain-only-minimal.json")
	manifest.SeedRecords = nil
	for reportIndex := range manifest.Reports {
		manifest.Reports[reportIndex].EvidenceRequirements = nil
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("manifest without model-authored seed records was rejected: %v", err)
	}
}

func TestValidateManifestAcceptsRuntimeIdentityFoundationRelationTargets(t *testing.T) {
	manifest := loadFixtureManifest(t, "domain-only-minimal.json")
	for _, target := range []string{"identity_user", "identity_organization_unit"} {
		manifest.Objects[0].Fields = append(manifest.Objects[0].Fields, definitionmodel.FieldSchema{Key: target, Type: "relation", Validation: definitionmodel.FieldValidation{Target: target}})
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("published Runtime Foundation relation target rejected: %v", err)
	}
}

func TestValidateManifestRejectsAmbiguousSubjectLifecycleFieldPolicy(t *testing.T) {
	manifest := loadFixtureManifest(t, "domain-only-minimal.json")
	manifest.Objects[0].Fields[0].Config = map[string]any{"lifecycle_subject_identity": "yes", "lifecycle_subject_file": true, "lifecycle_erase": "anonymize"}
	err := ValidateManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), "lifecycle_subject_identity") || !strings.Contains(err.Error(), "file lifecycle erase") {
		t.Fatalf("expected lifecycle governance diagnostics, got %v", err)
	}
	manifest.Objects[0].Fields[0].Config = map[string]any{"lifecycle_subject_identity": true, "lifecycle_subject_file": true, "lifecycle_erase": "delete"}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid lifecycle field policy rejected: %v", err)
	}
}

func TestValidateManifestEnforcesReportFieldAndSeedEvidenceContract(t *testing.T) {
	manifest := loadFixtureManifest(t, "domain-only-minimal.json")
	manifest.Reports = []reportmodel.ReportSchema{{
		Key: "customer.summary", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "name", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "name"}}}},
		EvidenceRequirements: []reportmodel.ReportEvidenceRequirement{{ObjectKey: "customer", MinimumRecords: 2, RequiredNonEmptyFields: []string{"name"}}},
	}}
	err := ValidateManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), "requires at least 2 qualifying seed records") {
		t.Fatalf("expected Report seed evidence error, got %v", err)
	}
	manifest.Reports[0].EvidenceRequirements[0].MinimumRecords = 1
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid Report evidence contract: %v", err)
	}
}

func TestValidateManifestRequiresConcreteProviderForRuntimeConnection(t *testing.T) {
	manifest := loadFixtureManifest(t, "domain-only-minimal.json")
	manifest.Integrations.Connectors = append(manifest.Integrations.Connectors, connectormodel.ConnectorSchema{Key: "file_storage", Providers: []connectormodel.ConnectorProviderSchema{{Key: "multi"}, {Key: "local"}}})
	manifest.Integrations.Connections = []connectormodel.ConnectionSchema{{Key: "files", ConnectorKey: "file_storage"}}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "concrete Provider is required") {
		t.Fatalf("missing Provider error=%v", err)
	}
	manifest.Integrations.Connections[0].ProviderKey = "multi"
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "non-executable Provider") {
		t.Fatalf("multi Provider error=%v", err)
	}
	manifest.Integrations.Connections[0].ProviderKey = "local"
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("concrete Provider rejected: %v", err)
	}
	manifest.Integrations.Connections[0].Config = map[string]any{"provider": "local"}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "must use provider_key") {
		t.Fatalf("config Provider error=%v", err)
	}
}

func TestValidateManifestRejectsWorkflowNodeWithMissingBusinessAction(t *testing.T) {
	manifest := loadFixtureManifest(t, "hr-personnel.json")
	actions := make([]definitionmodel.ActionSchema, 0, len(manifest.Actions))
	for _, action := range manifest.Actions {
		if action.Key != "leave_request.approve" {
			actions = append(actions, action)
		}
	}
	manifest.Actions = actions
	err := ValidateManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), "unknown Business Action") {
		t.Fatalf("expected missing Business Action validation error, got %v", err)
	}
}

func TestValidateManifestRejectsRuntimeWorkflowContractViolations(t *testing.T) {
	t.Run("approval outcomes", func(t *testing.T) {
		manifest := loadFixtureManifest(t, "hr-personnel.json")
		workflow := &manifest.Workflows[0]
		edges := make([]definitionmodel.WorkflowGraphEdge, 0, len(workflow.Graph.Edges))
		for _, edge := range workflow.Graph.Edges {
			if edge.Source != "manager" || edge.Branch != "rejected" {
				edges = append(edges, edge)
			}
		}
		workflow.Graph.Edges = edges
		err := ValidateManifest(manifest)
		if err == nil || !strings.Contains(err.Error(), "backend.workflow.approval_outcomes_required") {
			t.Fatalf("expected approval outcomes validation error, got %v", err)
		}
	})

	t.Run("trigger type", func(t *testing.T) {
		manifest := loadFixtureManifest(t, "hr-personnel.json")
		manifest.Workflows[0].TriggerContract.Type = "record_event"
		err := ValidateManifest(manifest)
		if err == nil || !strings.Contains(err.Error(), "backend.workflow.trigger_type_invalid") {
			t.Fatalf("expected trigger type validation error, got %v", err)
		}
	})

	t.Run("run as role is resolved dynamically by Identity", func(t *testing.T) {
		manifest := loadFixtureManifest(t, "hr-personnel.json")
		manifest.Workflows[0].RunAs = "restricted"
		err := ValidateManifest(manifest)
		if err != nil {
			t.Fatalf("dynamic Identity role was rejected by static Runtime validation: %v", err)
		}
	})
}

func TestValidateManifestRejectsWorkflowNodeWithMissingRequiredActionInput(t *testing.T) {
	manifest := loadFixtureManifest(t, "hr-personnel.json")
	for actionIndex := range manifest.Actions {
		if manifest.Actions[actionIndex].Key == "leave_request.reject" {
			manifest.Actions[actionIndex].PayloadFields = []definitionmodel.ActionPayloadField{{Key: "reason", Type: "text", Required: true}}
		}
	}
	for workflowIndex := range manifest.Workflows {
		workflow := &manifest.Workflows[workflowIndex]
		if workflow.Key != "leave_request_approval" || workflow.Graph == nil {
			continue
		}
		for nodeIndex := range workflow.Graph.Nodes {
			node := &workflow.Graph.Nodes[nodeIndex]
			if node.ID == "reject" && node.Contract != nil && node.Contract.Action != nil {
				node.Contract.Action.Input = nil
			}
		}
	}
	err := ValidateManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), "required input for Business Action") || !strings.Contains(err.Error(), ".input.reason") {
		t.Fatalf("expected required Workflow Action input validation error, got %v", err)
	}
}

func TestValidateManifestRejectsInvalidArtifacts(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*manifestmodel.ManifestSchema)
		wantErr string
	}{
		{
			name: "localized option storage value",
			mutate: func(manifest *manifestmodel.ManifestSchema) {
				manifest.Objects[0].Fields[1].Validation.Options = []string{"潜在客户"}
			},
			wantErr: "stable English storage value",
		},
		{
			name: "missing required seed field",
			mutate: func(manifest *manifestmodel.ManifestSchema) {
				delete(manifest.SeedRecords[0].Data, "name")
			},
			wantErr: "required seed field is missing",
		},
		{
			name: "unknown seed reference",
			mutate: func(manifest *manifestmodel.ManifestSchema) {
				for index := range manifest.SeedRecords {
					if manifest.SeedRecords[index].ObjectKey == "contact" {
						manifest.SeedRecords[index].Data["customer"] = "$record:missing_customer"
						return
					}
				}
				t.Fatalf("fixture should include contact seed")
			},
			wantErr: "references unknown seed key",
		},
		{
			name: "lifecycle insertion point encoded as workflow",
			mutate: func(manifest *manifestmodel.ManifestSchema) {
				manifest.Workflows[0].Trigger["phase"] = "before"
			},
			wantErr: "belong in automation_rules",
		},
		{
			name: "workflow approval encoded as automation instruction",
			mutate: func(manifest *manifestmodel.ManifestSchema) {
				manifest.AutomationRules[0].Instructions = append(manifest.AutomationRules[0].Instructions, automationmodel.AutomationInstructionSchema{Key: "approval", Type: "approval"})
			},
			wantErr: "unsupported Automation instruction type",
		},
		{
			name: "inline connector secret",
			mutate: func(manifest *manifestmodel.ManifestSchema) {
				manifest.Integrations.Connectors = append(manifest.Integrations.Connectors, connectormodel.ConnectorSchema{Key: "private_api", Providers: []connectormodel.ConnectorProviderSchema{{Key: "generic"}}})
				manifest.Integrations.Connections = append(manifest.Integrations.Connections, connectormodel.ConnectionSchema{Key: "private_api_default", ConnectorKey: "private_api", ProviderKey: "generic", Config: map[string]any{"api_token": "plaintext-secret"}})
			},
			wantErr: "inline Connector secrets are not allowed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := loadFixtureManifest(t, "crm-customer-360.json")
			tt.mutate(&manifest)
			err := ValidateManifest(manifest)
			if err == nil {
				t.Fatalf("ValidateManifest() expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateManifest() error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func loadFixtureManifest(t *testing.T, name string) manifestmodel.ManifestSchema {
	t.Helper()
	path := filepath.Join("..", "testdata", "manifests", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	for _, connection := range manifest.Integrations.Connections {
		found := false
		for _, connector := range manifest.Integrations.Connectors {
			found = found || connector.Key == connection.ConnectorKey
		}
		if !found {
			manifest.Integrations.Connectors = append(manifest.Integrations.Connectors, connectormodel.ConnectorSchema{Key: connection.ConnectorKey, Providers: []connectormodel.ConnectorProviderSchema{{Key: connection.ProviderKey}}})
		}
	}
	return manifest
}
