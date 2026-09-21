package workflow

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestWorkflowDeclarativeRelationAndManagerChainResolvers(t *testing.T) {
	identity := workflowApprovalIdentityStub{
		users: map[string]identitysdk.User{
			"requester": {ID: "requester", ManagerUserID: "manager-1", Status: identitysdk.UserStatusActive},
			"manager-1": {ID: "manager-1", ManagerUserID: "manager-2", Status: identitysdk.UserStatusActive},
			"manager-2": {ID: "manager-2", Status: identitysdk.UserStatusActive},
			"reviewer":  {ID: "reviewer", Status: identitysdk.UserStatusActive},
		},
		identityUsers: []identitysdk.User{{ID: "role-user", Status: identitysdk.UserStatusActive}},
		roles:         []identitysdk.Role{{ID: "finance-role", Key: "finance"}},
		assignments:   map[string][]identitysdk.UserRoleAssignment{"role-user": {{UserID: "role-user", RoleID: "finance-role"}}},
	}
	objects := workflowAssigneeResolverObjects()
	engine := NewWorkflowProcessEngine(WorkflowDependencies{
		Identity: identity,
		RecordReader: workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{
			"record":       {ID: "record", Data: map[string]any{"department": "department-1", "requester": "requester"}},
			"department-1": {ID: "department-1", Data: map[string]any{"reviewer": "reviewer", "approval_role": "finance"}},
		}},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema { return objects },
	})
	process := workflowEngineProcess(nil, nil)
	principal := workflowProcessQueryPrincipal()

	relationUsers, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, definitionmodel.WorkflowAssigneeResolver{Type: "relation_user", RelationPath: []string{"department", "reviewer"}}, principal)
	if err != nil || len(relationUsers) != 1 || relationUsers[0].UserID != "reviewer" {
		t.Fatalf("relation users=%+v err=%v", relationUsers, err)
	}
	userEvidence := relationUsers[0].Evidence.Matches[0]
	if userEvidence.ObjectKey != "department" || userEvidence.RecordID != "department-1" || userEvidence.FieldKey != "reviewer" || len(userEvidence.Facts) != 1 || userEvidence.Facts[0].Value != "department.reviewer" {
		t.Fatalf("relation user evidence=%+v", userEvidence)
	}

	relationRoles, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, definitionmodel.WorkflowAssigneeResolver{Type: "relation_role", RelationPath: []string{"department"}, RoleField: "approval_role"}, principal)
	if err != nil || len(relationRoles) != 1 || relationRoles[0].UserID != "role-user" || relationRoles[0].RoleKey != "finance" {
		t.Fatalf("relation roles=%+v err=%v", relationRoles, err)
	}
	roleEvidence := relationRoles[0].Evidence.Matches[0]
	if roleEvidence.RoleKey != "finance" || roleEvidence.RecordID != "department-1" || roleEvidence.FieldKey != "approval_role" {
		t.Fatalf("relation role evidence=%+v", roleEvidence)
	}

	managerChain, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, definitionmodel.WorkflowAssigneeResolver{Type: "manager_chain", Source: "record", Field: "requester", MaxDepth: 2}, principal)
	if err != nil || len(managerChain) != 2 || managerChain[0].UserID != "manager-1" || managerChain[1].UserID != "manager-2" {
		t.Fatalf("manager chain=%+v err=%v", managerChain, err)
	}
	if managerChain[0].Evidence.Matches[0].SubjectUserID != "requester" || managerChain[0].Evidence.Matches[0].Facts[0].Value != "1" || managerChain[1].Evidence.Matches[0].Facts[0].Value != "2" {
		t.Fatalf("manager evidence=%+v", managerChain)
	}
}

func TestWorkflowManagerChainRejectsCycles(t *testing.T) {
	identity := workflowApprovalIdentityStub{users: map[string]identitysdk.User{
		"requester": {ID: "requester", ManagerUserID: "manager", Status: identitysdk.UserStatusActive},
		"manager":   {ID: "manager", ManagerUserID: "requester", Status: identitysdk.UserStatusActive},
	}}
	engine := NewWorkflowProcessEngine(WorkflowDependencies{Identity: identity})
	process := workflowEngineProcess(nil, nil)
	process.Variables["requester"] = "requester"
	_, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, definitionmodel.WorkflowAssigneeResolver{Type: "manager_chain", Source: "variable", Field: "requester", MaxDepth: 3}, workflowProcessQueryPrincipal())
	if apperror.CodeOf(err) != "backend.workflow.resolver_manager_cycle" {
		t.Fatalf("cycle error=%v", err)
	}
}

func TestWorkflowReferenceValidatorChecksRelationResolverPaths(t *testing.T) {
	objects := workflowAssigneeResolverObjects()
	validator := NewWorkflowReferenceValidator(workflowReferenceSchemaEdgeStub{}, func(context.Context) map[string]definitionmodel.ObjectSchema { return objects }, nil, nil)
	workflow := definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "record_updated", ObjectKey: "order"}}
	valid := []definitionmodel.WorkflowAssigneeResolver{
		{Type: "relation_user", RelationPath: []string{"department", "reviewer"}},
		{Type: "relation_role", RelationPath: []string{"department"}, RoleField: "approval_role"},
		{Type: "manager_chain", Source: "record", Field: "requester", MaxDepth: 2},
	}
	if issues := validator.validateWorkflowResolvers(t.Context(), workflow, "approval", valid, nil, nil); len(issues) != 0 {
		t.Fatalf("valid relation issues=%+v", issues)
	}
	invalid := []definitionmodel.WorkflowAssigneeResolver{
		{Type: "relation_user", RelationPath: []string{"department", "approval_role"}},
		{Type: "relation_role", RelationPath: []string{"department"}, RoleField: "reviewer"},
		{Type: "manager_chain", Source: "record", Field: "status", MaxDepth: 2},
	}
	if issues := validator.validateWorkflowResolvers(t.Context(), workflow, "approval", invalid, nil, nil); len(issues) != 3 {
		t.Fatalf("invalid relation issues=%+v", issues)
	}
}

func workflowAssigneeResolverObjects() map[string]definitionmodel.ObjectSchema {
	return map[string]definitionmodel.ObjectSchema{
		"order": {
			Key: "order",
			Fields: []definitionmodel.FieldSchema{
				{Key: "department", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "department"}},
				{Key: "requester", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "identity_user"}},
				{Key: "status", Type: "text"},
			},
		},
		"department": {
			Key: "department",
			Fields: []definitionmodel.FieldSchema{
				{Key: "reviewer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "identity_user"}},
				{Key: "approval_role", Type: "text"},
			},
		},
	}
}
