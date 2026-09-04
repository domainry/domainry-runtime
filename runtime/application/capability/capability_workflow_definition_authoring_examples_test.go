package capability

import (
	"context"
	"encoding/json"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

type workflowAuthoringSchemaStub struct{}

type workflowAuthoringIdentityStub struct{}

func (workflowAuthoringIdentityStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return identitysdk.User{}, false, nil
}
func (workflowAuthoringIdentityStub) FindOrganizationUnit(context.Context, identitysdk.OrganizationUnitLookup) (identitysdk.OrganizationUnit, bool, error) {
	return identitysdk.OrganizationUnit{}, false, nil
}
func (workflowAuthoringIdentityStub) ListUsers(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.User, error) {
	return nil, nil
}
func (workflowAuthoringIdentityStub) ListRoles(context.Context, identitysdk.ProjectionQuery) ([]identitysdk.Role, error) {
	return []identitysdk.Role{{Key: "finance_manager"}}, nil
}
func (workflowAuthoringIdentityStub) ListUserRoleAssignments(context.Context, identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	return nil, nil
}

func (workflowAuthoringSchemaStub) WorkflowSchemaSnapshot(context.Context, principalmodel.Principal) workflowapplication.WorkflowSchemaSnapshot {
	return workflowapplication.WorkflowSchemaSnapshot{
		Actions: []definitionmodel.ActionSchema{{Key: "order.complete"}, {Key: "order.reject"}},
	}
}

func (workflowAuthoringSchemaStub) ConnectorAdapterExists(context.Context, string) bool { return false }

func TestWorkflowDefinitionAuthoringExamplesExecuteApplicationValidator(t *testing.T) {
	service := workflowapplication.NewWorkflowApplicationService(workflowapplication.WorkflowDependencies{
		Schema:   workflowAuthoringSchemaStub{},
		Identity: workflowAuthoringIdentityStub{},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"order": {Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}}
		},
	})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}
	var capabilityIndex int
	domain := workflowpolicy.WorkflowAuthoringDomain()
	for index := range domain.Capabilities {
		if domain.Capabilities[index].Key == "workflow.definition" {
			capabilityIndex = index
			break
		}
	}
	capability := domain.Capabilities[capabilityIndex]
	if capability.Key != "workflow.definition" {
		t.Fatalf("Workflow update capability=%s", capability.Key)
	}
	for _, example := range capability.Examples {
		workflowValue, _ := example.Value["payload"].(map[string]any)
		payload, err := json.Marshal(workflowValue)
		if err != nil {
			t.Fatal(err)
		}
		var workflow definitionmodel.WorkflowSchema
		if err := json.Unmarshal(payload, &workflow); err != nil {
			t.Fatal(err)
		}
		report, err := service.ValidateWorkflowDefinition(t.Context(), workflow, principal)
		if err != nil {
			t.Fatalf("example=%s validator error=%v", example.Name, err)
		}
		if len(example.ExpectedErrorCodes) == 0 {
			if !report.Valid || len(report.Issues) != 0 {
				t.Fatalf("example=%s report=%#v", example.Name, report)
			}
			continue
		}
		found := false
		for _, issue := range report.Issues {
			if issue.Code == example.ExpectedErrorCodes[0] {
				found = true
			}
		}
		if report.Valid || !found {
			t.Fatalf("example=%s report=%#v want=%s", example.Name, report, example.ExpectedErrorCodes[0])
		}
	}
}
