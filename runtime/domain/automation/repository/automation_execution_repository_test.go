package repository

import (
	"reflect"
	"testing"
)

func TestAutomationExecutionRepositoryBudget(t *testing.T) {
	contract := reflect.TypeOf((*AutomationExecutionRepository)(nil)).Elem()
	if contract.NumMethod() != 2 || contract.NumMethod() > 5 {
		t.Fatalf("automation execution repository has %d methods", contract.NumMethod())
	}
}
