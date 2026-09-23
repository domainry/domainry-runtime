package transport

import (
	"testing"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

func TestProjectEngineActionOutputExposesBusinessResult(t *testing.T) {
	business := map[string]any{"company": "company-1"}
	result := actionmodel.ActionInvocationResult{
		Output: map[string]any{"action_key": "company.register_company", "data": business},
		Object: &actionmodel.ActionObjectResult{Output: business},
	}
	output := projectEngineActionOutput(result)
	if output["company"] != "company-1" || output["action_key"] != nil || output["data"] != nil {
		t.Fatalf("output=%#v", output)
	}
	output["company"] = "changed"
	if business["company"] != "company-1" {
		t.Fatal("project output aliases Runtime result")
	}
}

func TestProjectEngineActionOutputUsesEnvelopeDataForReceiptReplay(t *testing.T) {
	result := actionmodel.ActionInvocationResult{Output: map[string]any{
		"action_key": "company.register_company",
		"data":       map[string]any{"company": "company-1"},
	}}
	output := projectEngineActionOutput(result)
	if output["company"] != "company-1" || len(output) != 1 {
		t.Fatalf("output=%#v", output)
	}
}
