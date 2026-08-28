package contract

import (
	"reflect"
	"testing"
)

func TestWorkflowProcessStoreMethodBudget(t *testing.T) {
	contract := reflect.TypeOf((*WorkflowProcessStore)(nil)).Elem()
	if contract.NumMethod() != 14 || contract.NumMethod() > 15 {
		t.Fatalf("workflow process store has %d methods", contract.NumMethod())
	}
}
