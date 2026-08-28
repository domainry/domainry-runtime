package validation_test

import (
	"encoding/json"
	"testing"

	deploymentcontract "github.com/domainry/domainry-runtime/runtime/domain/deployment/contract"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentvalidation "github.com/domainry/domainry-runtime/runtime/domain/deployment/validation"
)

func TestDeploymentAuthoringExamplesExecuteOwnerValidator(t *testing.T) {
	definition := deploymentcontract.DeploymentFrontendSupportObservationAuthoringCapability()
	for _, example := range definition.Examples {
		raw, err := json.Marshal(example.Value)
		if err != nil {
			t.Fatalf("marshal %s: %v", example.Name, err)
		}
		var manifest deploymentmodel.FrontendCapabilityManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("decode %s: %v", example.Name, err)
		}
		result := deploymentvalidation.ValidateFrontendCapabilityManifest(manifest, "$runtime.contract_version", func() []deploymentmodel.FrontendCapabilityDefinition {
			return []deploymentmodel.FrontendCapabilityDefinition{{Key: "maintenance.reference_impact", FrontendSupportKey: "maintenance.reference-impact.v1", Permissions: []string{"workspace.admin"}}}
		})
		if example.Name == "invalid_with_repair" {
			if !deploymentHasIssue(result, example.ExpectedErrorCodes[0]) {
				t.Fatalf("%s did not produce %s: %#v", example.Name, example.ExpectedErrorCodes[0], result.Issues)
			}
		} else if !result.Valid {
			t.Fatalf("%s rejected by owner validator: %#v", example.Name, result.Issues)
		}
	}
}

func deploymentHasIssue(result deploymentmodel.FrontendCapabilityManifestValidationResult, code string) bool {
	for _, issue := range result.Issues {
		if issue.ErrorCode == code {
			return true
		}
	}
	return false
}
