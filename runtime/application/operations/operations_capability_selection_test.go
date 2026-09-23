package operations

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestOperationsServiceRejectsDefinitionsExcludedByComposition(t *testing.T) {
	all := operationsprojection.OperationsDefinitions()
	selected := make([]operationsmodel.OperationsDefinition, 0, len(all))
	for _, definition := range all {
		if definition.Owner != "workflow" && definition.Owner != "automation" {
			selected = append(selected, definition)
		}
	}
	service := NewOperationsApplicationService(nil, nil, nil, nil, selected)
	for _, definition := range service.Definitions() {
		if definition.Owner == "workflow" || definition.Owner == "automation" {
			t.Fatalf("unselected operation definition remains published: %#v", definition)
		}
	}

	_, _, err := service.Submit(t.Context(), OperationsSubmitRequest{
		Kind: "automation.rule.enable", ResourceType: "automation_rule",
	}, "request", principalmodel.Principal{})
	if apperror.CodeOf(err) != "backend.operations.kind_not_registered" {
		t.Fatalf("unselected operation error=%v", err)
	}
}
