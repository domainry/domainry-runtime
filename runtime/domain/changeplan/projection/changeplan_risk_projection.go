package projection

import (
	"encoding/json"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
)

func businessFieldTypeChanged(item changeplanmodel.BusinessSystemChangeItem) bool {
	return changeplanpolicy.ChangePlanExpectedChangeKind(item.Operation, businessReferenceResourceType(item.ResourceType), item.Before, item.After) == "destructive"
}

func businessFieldBecameRequired(item changeplanmodel.BusinessSystemChangeItem) bool {
	if businessReferenceResourceType(item.ResourceType) != "field" {
		return false
	}
	before, after := businessChangeJSONMap(item.Before), businessChangeJSONMap(item.After)
	beforeRequired, beforeOK := before["required"].(bool)
	afterRequired, afterOK := after["required"].(bool)
	return beforeOK && afterOK && !beforeRequired && afterRequired
}

func businessChangeJSONMap(payload json.RawMessage) map[string]any {
	values := map[string]any{}
	_ = json.Unmarshal(payload, &values)
	return values
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
