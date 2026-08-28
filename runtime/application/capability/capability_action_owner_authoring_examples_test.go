package capability

import (
	"encoding/json"
	"testing"

	actionauthoring "github.com/domainry/domainry-runtime/runtime/domain/action/projection"
	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionAuthoringExamplesExecuteOwnerValidator(t *testing.T) {
	capability := actionauthoring.ActionDefinitionAuthoringCapability()
	for _, example := range capability.Examples {
		payload, err := json.Marshal(example.Value["payload"])
		if err != nil {
			t.Fatal(err)
		}
		var action definitionmodel.ActionSchema
		if err := json.Unmarshal(payload, &action); err != nil {
			t.Fatal(err)
		}
		issues := actionvalidation.ActionValidateDefinitionIssuesWithObjects(action, []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}}}})
		if len(example.ExpectedErrorCodes) == 0 {
			if len(issues) != 0 {
				t.Fatalf("example=%s issues=%#v", example.Name, issues)
			}
			continue
		}
		found := false
		for _, issue := range issues {
			found = found || issue.ErrorCode == example.ExpectedErrorCodes[0]
		}
		if !found {
			t.Fatalf("example=%s issues=%#v want=%s", example.Name, issues, example.ExpectedErrorCodes[0])
		}
	}
}
