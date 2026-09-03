package projection

import (
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

func TestAuthorizationActionForWorkflowProducesConcreteSameKeyPermission(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "order.approval", Name: "Order approval", Enabled: true}
	definition, err := AuthorizationActionForWorkflow(workflow, "application:orders")
	if err != nil {
		t.Fatal(err)
	}
	wantKey := workflowcontract.RunActionKey(workflow.Key)
	if definition.Key != wantKey || definition.Permission == nil || definition.Permission.Key != wantKey || definition.Permission.Owner != definition.Owner {
		t.Fatalf("definition=%#v", definition)
	}
	if definition.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || definition.HTTP != nil || len(definition.NonHTTP) != 1 {
		t.Fatalf("authorization=%#v bindings=%#v", definition.Authorization, definition.NonHTTP)
	}
	registry := actioncontract.NewRegistry()
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	resolved, found := registry.ResolveNonHTTP(workflowcontract.RunActionBindingKind, workflow.Key)
	if !found || resolved.Key != wantKey {
		t.Fatalf("resolved=%#v found=%v", resolved, found)
	}
}

func TestAuthorizationActionForWorkflowRejectsMissingIdentity(t *testing.T) {
	if _, err := AuthorizationActionForWorkflow(definitionmodel.WorkflowSchema{Name: "Missing key"}, "application:orders"); err == nil {
		t.Fatal("expected missing Workflow key to fail")
	}
}
