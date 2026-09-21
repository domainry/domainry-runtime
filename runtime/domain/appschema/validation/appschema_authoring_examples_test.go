package validation

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	metadataauthoring "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	businesscalendarpolicy "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestMetadataAuthoringExamplesExecuteOwnerValidators(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "number"}, {Key: "status"}, {Key: "created_at"}}}, {Key: "customer"}}
	capabilities := metadataauthoring.ApplicationSchemaAuthoringCapabilities()
	for _, capability := range capabilities {
		for _, example := range capability.Examples {
			request, payload := metadataExampleRequest(t, example.Value)
			var err error
			switch capability.Key {
			case "schema.object":
				_, err = ApplicationSchemaValidateObjectDefinition("order", payload)
			case "schema.field", "schema.relation":
				var routeOwned map[string]any
				if json.Unmarshal(payload, &routeOwned) == nil {
					if _, declared := routeOwned["key"]; !declared {
						routeOwned["key"] = "field"
						payload, _ = json.Marshal(routeOwned)
					}
				}
				request.Payload = payload
				_, err = ApplicationSchemaNormalizeFieldMutation(request, objects, metadataauthoring.ApplicationSchemaAuthoringFieldTypes(), nil, 0)
			case "schema.dictionary":
				err = ApplicationSchemaValidateDictionaryDefinition("order_status", payload)
			case "schema.business_calendar":
				var calendar businesscalendarmodel.BusinessCalendarSchema
				if decodeErr := json.Unmarshal(payload, &calendar); decodeErr != nil {
					err = decodeErr
				} else {
					calendar.Key = "workday"
					if validationErr := businesscalendarpolicy.Validate(calendar); validationErr != nil {
						err = badRequest(businesscalendarpolicy.ValidationCode(validationErr))
					}
				}
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

func metadataExampleRequest(t *testing.T, value map[string]any) (appschemamodel.ApplicationDefinitionUpsertRequest, json.RawMessage) {
	t.Helper()
	request := appschemamodel.ApplicationDefinitionUpsertRequest{}
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
