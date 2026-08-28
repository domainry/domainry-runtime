package validation

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadataauthoring "github.com/domainry/domainry-runtime/runtime/domain/metadata/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestMetadataAuthoringExamplesExecuteOwnerValidators(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "number"}, {Key: "status"}, {Key: "created_at"}}}, {Key: "customer"}}
	capabilities := append(metadataauthoring.MetadataAuthoringCapabilities(), metadataauthoring.MetadataViewAuthoringCapabilities()...)
	for _, capability := range capabilities {
		for _, example := range capability.Examples {
			request, payload := metadataExampleRequest(t, example.Value)
			var err error
			switch capability.Key {
			case "schema.object":
				_, err = MetadataValidateObjectDefinition("order", payload)
			case "schema.field", "schema.relation":
				request.Payload = payload
				_, err = MetadataNormalizeFieldMutation(request, objects, metadataauthoring.MetadataAuthoringFieldTypes(), nil, 0)
			case "schema.dictionary":
				err = MetadataValidateDictionaryDefinition("order_status", payload)
			case "view.definition":
				_, err = MetadataValidateViewDefinition("order_list", payload, objects)
			}
			if len(example.ExpectedErrorCodes) == 0 {
				if err != nil {
					t.Fatalf("capability=%s example=%s err=%v", capability.Key, example.Name, err)
				}
				continue
			}
			if got := apperror.CodeOf(err); got != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%q want=%q err=%v", capability.Key, example.Name, got, example.ExpectedErrorCodes[0], err)
			}
		}
	}
}

func metadataExampleRequest(t *testing.T, value map[string]any) (metadatamodel.MetadataDefinitionUpsertRequest, json.RawMessage) {
	t.Helper()
	request := metadatamodel.MetadataDefinitionUpsertRequest{}
	if objectKey, ok := value["object_key"].(string); ok {
		request.ObjectKey = objectKey
	}
	payloadValue := value["payload"]
	if payloadValue == nil {
		payloadValue = value
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		t.Fatal(err)
	}
	return request, payload
}
