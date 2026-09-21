package policy

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestWorkflowNodeContractProjection(t *testing.T) {
	approval := definitionmodel.WorkflowApprovalNodeContract{Mode: "all"}
	action := definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.approve"}
	cc := definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "order.notify"}
	node := definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{
		Approval: &approval,
		Action:   &action,
		CC:       &cc,
	}}
	if got := WorkflowApprovalNodeContract(node); !reflect.DeepEqual(got, approval) {
		t.Fatalf("approval contract = %+v, want %+v", got, approval)
	}
	if got := WorkflowBusinessActionNodeContract(node); !reflect.DeepEqual(got, action) {
		t.Fatalf("action contract = %+v, want %+v", got, action)
	}
	if got := WorkflowCCNodeContract(node); !reflect.DeepEqual(got, cc) {
		t.Fatalf("cc contract = %+v, want %+v", got, cc)
	}

	for name, node := range map[string]definitionmodel.WorkflowGraphNode{
		"missing contract": {},
		"empty contract":   {Contract: &definitionmodel.WorkflowNodeContract{}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := WorkflowApprovalNodeContract(node); !reflect.DeepEqual(got, definitionmodel.WorkflowApprovalNodeContract{}) {
				t.Fatalf("approval contract = %+v", got)
			}
			if got := WorkflowBusinessActionNodeContract(node); !reflect.DeepEqual(got, definitionmodel.WorkflowBusinessActionNodeContract{}) {
				t.Fatalf("action contract = %+v", got)
			}
			if got := WorkflowCCNodeContract(node); !reflect.DeepEqual(got, definitionmodel.WorkflowCCNodeContract{}) {
				t.Fatalf("cc contract = %+v", got)
			}
		})
	}
}

func TestWorkflowAssigneeResolverValidationMatrix(t *testing.T) {
	tests := []struct {
		name     string
		resolver definitionmodel.WorkflowAssigneeResolver
		want     bool
	}{
		{name: "users", resolver: definitionmodel.WorkflowAssigneeResolver{Type: " users ", UserIDs: []string{"", " manager ", "manager"}}, want: true},
		{name: "users empty", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "users", UserIDs: []string{"", "  "}}},
		{name: "variable user", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "variable_user", Field: " owner_id "}, want: true},
		{name: "record user field", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "record_user_field", Field: " owner_id "}, want: true},
		{name: "record user field empty", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "record_user_field"}},
		{name: "manager", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "manager", UserField: " owner_id "}, want: true},
		{name: "manager field empty", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "manager"}},
		{name: "manager of", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "manager_of", UserField: " owner_id "}, want: true},
		{name: "initiator manager", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "initiator_manager"}, want: true},
		{name: "role", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "role", RoleKey: " approver "}, want: true},
		{name: "role empty", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "role"}},
		{name: "project", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "project", ResolverKey: " regional_approver "}, want: true},
		{name: "project empty", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "project"}},
		{name: "relation user", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "relation_user", RelationPath: []string{" department ", " owner "}}, want: true},
		{name: "relation user empty", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "relation_user"}},
		{name: "relation user too deep", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "relation_user", RelationPath: []string{"a", "b", "c", "d", "e", "f"}}},
		{name: "relation role", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "relation_role", RelationPath: []string{" department "}, RoleField: " approval_role "}, want: true},
		{name: "relation role empty field", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "relation_role", RelationPath: []string{"department"}}},
		{name: "manager chain initiator", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "manager_chain", Source: "initiator", MaxDepth: 3}, want: true},
		{name: "manager chain variable", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "manager_chain", Source: "variable", Field: "owner", MaxDepth: 3}, want: true},
		{name: "manager chain missing field", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "manager_chain", Source: "record", MaxDepth: 3}},
		{name: "manager chain invalid depth", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "manager_chain", Source: "initiator", MaxDepth: 21}},
		{name: "unknown", resolver: definitionmodel.WorkflowAssigneeResolver{Type: "team"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := WorkflowAssigneeResolverIsValid(test.resolver); got != test.want {
				t.Fatalf("valid = %v, want %v", got, test.want)
			}
		})
	}
}
