package validation

import (
	"encoding/json"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
)

func businessReferenceResourceType(resourceType string) string {
	return changeplanpolicy.ChangePlanCanonicalResourceType(resourceType)
}

func changePlanReferenceImpact(graph changeplanmodel.ReferenceGraph, resourceType, resourceKey string) changeplanmodel.ReferenceImpact {
	return changeplanpolicy.ChangePlanReferenceImpact(graph, resourceType, resourceKey)
}

func businessChangeJSONValue(value json.RawMessage) any {
	var decoded any
	_ = json.Unmarshal(value, &decoded)
	return decoded
}

func flattenBusinessChangeStrings(value any) map[string]bool {
	result := map[string]bool{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case string:
			result[typed] = true
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return result
}
