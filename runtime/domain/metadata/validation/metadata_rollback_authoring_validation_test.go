package validation_test

import (
	"encoding/json"
	"testing"

	metadatacontract "github.com/domainry/domainry-runtime/runtime/domain/metadata/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
)

func TestMetadataRollbackExamplesExecuteOwnerValidator(t *testing.T) {
	for _, example := range metadatacontract.MetadataRollbackAuthoringCapability().Examples {
		raw, err := json.Marshal(example.Value)
		if err != nil {
			t.Fatalf("marshal %s: %v", example.Name, err)
		}
		var envelope struct {
			ResourceType string `json:"resource_type"`
			ResourceKey  string `json:"resource_key"`
			metadatamodel.MetadataDefinitionRollbackRequest
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("decode %s: %v", example.Name, err)
		}
		code := metadatavalidation.MetadataRollbackRequestErrorCode(envelope.MetadataDefinitionRollbackRequest)
		if example.Name == "invalid_with_repair" {
			if code != example.ExpectedErrorCodes[0] {
				t.Fatalf("%s code=%q want=%q", example.Name, code, example.ExpectedErrorCodes[0])
			}
		} else if code != "" || envelope.ResourceType == "" || envelope.ResourceKey == "" {
			t.Fatalf("%s rejected by owner validator: code=%q envelope=%#v", example.Name, code, envelope)
		}
	}
}
