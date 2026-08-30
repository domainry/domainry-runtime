package validation_test

import (
	"encoding/json"
	"testing"

	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
)

func TestMetadataRollbackExamplesExecuteOwnerValidator(t *testing.T) {
	for _, example := range appschemacontract.ApplicationSchemaRollbackAuthoringCapability().Examples {
		raw, err := json.Marshal(example.Value)
		if err != nil {
			t.Fatalf("marshal %s: %v", example.Name, err)
		}
		var envelope struct {
			ResourceType string `json:"resource_type"`
			ResourceKey  string `json:"resource_key"`
			appschemamodel.ApplicationDefinitionRollbackRequest
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode %s: %v", example.Name, err)
		}
		code := appschemavalidation.ApplicationSchemaRollbackRequestErrorCode(envelope.ApplicationDefinitionRollbackRequest)
		if example.Name == "invalid_with_repair" {
			if code != example.ExpectedErrorCodes[0] {
				t.Fatalf("%s code=%q want=%q", example.Name, code, example.ExpectedErrorCodes[0])
			}
		} else if code != "" || envelope.ResourceType == "" || envelope.ResourceKey == "" {
			t.Fatalf("%s rejected by owner validator: code=%q envelope=%#v", example.Name, code, envelope)
		}
	}
}
