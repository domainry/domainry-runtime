package workflow

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type validationSchemaStub struct{}

func (validationSchemaStub) WorkflowSchemaSnapshot(context.Context, principalmodel.Principal) WorkflowSchemaSnapshot {
	return WorkflowSchemaSnapshot{}
}
func (validationSchemaStub) ConnectorAdapterExists(context.Context, string) bool { return false }

func TestWorkflowValidationLocatesNodeAndEdgeFields(t *testing.T) {
	service := NewWorkflowApplicationService(WorkflowDependencies{Schema: validationSchemaStub{}, ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{}
	}})
	resolverWorkflow := definitionmodel.WorkflowSchema{Key: "approval", TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}, {ID: "approve", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "magic"}}}}}}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "to-approve", Source: "start", Target: "approve"}}}}
	report, err := service.ValidateWorkflowDefinition(t.Context(), resolverWorkflow, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}})
	if err != nil {
		t.Fatal(err)
	}
	issue := findWorkflowValidationIssue(report.Issues, "backend.workflow.approval_resolver_invalid")
	if issue == nil || issue.NodeID != "approve" || issue.FieldPath != "graph.nodes[approve].contract.approval.resolvers" || issue.CapabilityKey != "workflow.assignee_resolver" || issue.ContractVersion == "" {
		t.Fatalf("resolver issue lacks machine location: %#v", report.Issues)
	}
	edgeWorkflow := resolverWorkflow
	edgeWorkflow.Graph = &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}, {ID: "end", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notify", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "role", RoleKey: "manager"}}}}}}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "broken-edge", Source: "start", Target: "missing"}}}
	report, err = service.ValidateWorkflowDefinition(t.Context(), edgeWorkflow, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}})
	if err != nil {
		t.Fatal(err)
	}
	issue = findWorkflowValidationIssue(report.Issues, "backend.workflow.graph_edge_invalid")
	if issue == nil || issue.EdgeID != "broken-edge" || issue.FieldPath != "graph.edges[broken-edge]" {
		t.Fatalf("edge issue lacks machine location: %#v", report.Issues)
	}
}

func findWorkflowValidationIssue(issues []workflowmodel.WorkflowValidationIssue, code string) *workflowmodel.WorkflowValidationIssue {
	for index := range issues {
		if issues[index].Code == code {
			return &issues[index]
		}
	}
	return nil
}
