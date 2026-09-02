package projection

import (
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

func TestOperationsAuthorizationProjectsOneExactActionPerOperation(t *testing.T) {
	actions, err := OperationsAuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}

	actionByKey := make(map[string]actioncontract.ActionDefinition, len(actions))
	bindingsByInvocation := map[string]string{}
	for _, action := range actions {
		if action.Authorization.Strategy != actioncontract.AuthorizationExactRolePermission || action.Permission == nil || action.Permission.Key != action.Key {
			t.Fatalf("operation Action must use its same-key exact permission: %#v", action)
		}
		if len(action.NonHTTP) != 1 || action.NonHTTP[0].Kind != operationsActionBindingKind {
			t.Fatalf("operation Action must declare one runtime operation binding: %#v", action)
		}
		actionByKey[action.Key] = action
		bindingsByInvocation[action.NonHTTP[0].InvocationKey] = action.Key
	}

	for actionKey, bindings := range OperationsAuthorizationBindings() {
		for _, binding := range bindings {
			if binding.Kind != operationsActionBindingKind {
				t.Fatalf("Action %q has unexpected binding kind %q", actionKey, binding.Kind)
			}
			if previous := bindingsByInvocation[binding.InvocationKey]; previous != "" {
				t.Fatalf("operation %q is bound to both %q and %q", binding.InvocationKey, previous, actionKey)
			}
			bindingsByInvocation[binding.InvocationKey] = actionKey
		}
	}

	for _, operation := range OperationsDefinitions() {
		if actual := bindingsByInvocation[operation.Kind]; actual != operation.ActionKey {
			t.Errorf("operation %q binding=%q want exact Action %q", operation.Kind, actual, operation.ActionKey)
		}
		if operationsOwnedNonHTTPActionKeys[operation.ActionKey] {
			if _, found := actionByKey[operation.ActionKey]; !found {
				t.Errorf("Operations-owned Action %q was not projected", operation.ActionKey)
			}
		}
	}
	if len(bindingsByInvocation) != len(OperationsDefinitions()) {
		t.Fatalf("operation bindings=%d definitions=%d", len(bindingsByInvocation), len(OperationsDefinitions()))
	}
}
