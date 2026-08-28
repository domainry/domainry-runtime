package policy

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestWorkflowRuntimeFinalAuthoringAndPermissionEdges(t *testing.T) {
	capabilities := workflowAuthoringComponentCapabilities()
	if len(capabilities) == 0 || workflowComponentSchemaForKey(capabilities[0].Key).Type == "" {
		t.Fatal("known workflow component schema missing")
	}
	if schema := workflowComponentSchemaForKey("missing"); schema.Type != "" || schema.Ref != "" {
		t.Fatalf("unknown workflow component schema=%#v", schema)
	}
	schema := workflowComponentInputSchema([]capabilitycontract.CapabilityAuthoringParameter{{Key: "enabled", Type: "boolean"}})
	if schema.Properties["enabled"].Type != "boolean" {
		t.Fatalf("boolean parameter schema=%#v", schema)
	}

	global := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workflow.run"}})
	named := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workflow.run.order.approval"}})
	if !WorkflowRunPermissionAllows(global, "anything") || !WorkflowRunPermissionAllows(named, " order.approval ") || WorkflowRunPermissionAllows(principalmodel.Principal{}, "order.approval") {
		t.Fatal("workflow run permission matrix mismatch")
	}
}

func TestWorkflowRuntimeFinalTimerContractEdges(t *testing.T) {
	timer := definitionmodel.WorkflowTimerNodeContract{TimerKey: "timer", Purpose: "resume", DurationSeconds: 1}
	if got := WorkflowTimerNodeContract(definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{Timer: &timer}}); got.TimerKey != "timer" {
		t.Fatalf("timer contract=%#v", got)
	}
	for _, node := range []definitionmodel.WorkflowGraphNode{{}, {Contract: &definitionmodel.WorkflowNodeContract{}}} {
		if got := WorkflowTimerNodeContract(node); got.TimerKey != "" {
			t.Fatalf("empty timer contract=%#v", got)
		}
	}
	if validWorkflowTimerNodeContract("timer", definitionmodel.WorkflowTimerNodeContract{TimerKey: "timer", Purpose: "resume", Timezone: "Invalid/Timezone", DurationSeconds: 1}) {
		t.Fatal("invalid timezone accepted")
	}
	if validWorkflowTimerNodeContract("timer", definitionmodel.WorkflowTimerNodeContract{Purpose: "resume", DurationSeconds: 1}) {
		t.Fatal("blank timer key accepted")
	}
	if validWorkflowTimerNodeContract("timer", definitionmodel.WorkflowTimerNodeContract{TimerKey: "timer", DurationSeconds: 1}) {
		t.Fatal("blank timer purpose accepted")
	}
	if validWorkflowTimerNodeContract("unknown", definitionmodel.WorkflowTimerNodeContract{}) {
		t.Fatal("unknown timer type accepted")
	}

	for _, node := range []definitionmodel.WorkflowGraphNode{
		{ID: "timer", Type: "timer"},
		{ID: "timer", Type: "timer", Contract: &definitionmodel.WorkflowNodeContract{}},
	} {
		graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, node}}
		if WorkflowValidateGraph(graph) == nil {
			t.Fatalf("missing timer contract accepted: %#v", node)
		}
	}
}

func TestWorkflowRuntimeFinalRecordTimeEmptyAndNilEdges(t *testing.T) {
	for _, value := range []any{"", nil} {
		if _, ok := workflowRecordTime(value); ok {
			t.Fatalf("empty time value accepted: %#v", value)
		}
	}
}
