package action

import (
	"fmt"
	"math"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

func projectStoreOrganizationSnapshotOutput(principal principalmodel.Principal, actionKey string, contract definitionmodel.ActionOutputField, value any, objectForKey func(string) (definitionmodel.ObjectSchema, bool)) (map[string]any, error) {
	fail := func(detail string) (map[string]any, error) {
		return nil, apperror.New(apperror.KindInternal, "backend.action.store_organization_snapshot_output_invalid", nil, map[string]string{"action": actionKey, "field": contract.Key, "detail": detail})
	}
	page, ok := value.(map[string]any)
	if !ok || !snapshotExactKeys(page, []string{"items"}, []string{"next_cursor"}) {
		return fail("page")
	}
	if cursor, exists := page["next_cursor"]; exists {
		text, ok := cursor.(string)
		if !ok || len(text) > 2048 {
			return fail("next_cursor")
		}
	}
	items, ok := page["items"].([]any)
	if !ok || len(items) > runtimeext.StoreOrganizationCatalogMaximumPageSize {
		return fail("items")
	}
	objects := make(map[string]definitionmodel.ObjectSchema, len(contract.StoreOrganizationSnapshotObjectKeys))
	if objectForKey == nil || len(contract.StoreOrganizationSnapshotObjectKeys) == 0 {
		return fail("contract")
	}
	for _, raw := range contract.StoreOrganizationSnapshotObjectKeys {
		objectKey := strings.TrimSpace(raw)
		object, exists := objectForKey(objectKey)
		if objectKey == "" || !exists {
			return fail("contract_object")
		}
		objects[objectKey] = object
	}
	seenReferences := map[string]bool{}
	projectedItems := make([]any, 0, len(items))
	itemRequired := append([]string{"organization"}, contract.StoreOrganizationSnapshotObjectKeys...)
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok || !snapshotExactKeys(item, itemRequired, nil) {
			return fail(fmt.Sprintf("items[%d]", index))
		}
		organization, ok := item["organization"].(map[string]any)
		if !ok || !snapshotExactKeys(organization, []string{"reference", "code", "name", "status", "sort_order", "version"}, nil) {
			return fail(fmt.Sprintf("items[%d].organization", index))
		}
		reference, ok := organization["reference"].(string)
		if !ok || strings.TrimSpace(reference) == "" || seenReferences[reference] || !snapshotNonEmptyString(organization["code"]) || !snapshotNonEmptyString(organization["name"]) || !snapshotNonEmptyString(organization["status"]) || !snapshotInteger(organization["sort_order"]) || !snapshotPositiveInteger(organization["version"]) {
			return fail(fmt.Sprintf("items[%d].organization_values", index))
		}
		seenReferences[reference] = true
		projectedItem := make(map[string]any, len(item))
		projectedItem["organization"] = cloneSnapshotMap(organization)
		for _, objectKey := range contract.StoreOrganizationSnapshotObjectKeys {
			record, ok := item[objectKey].(map[string]any)
			if !ok || !snapshotExactKeys(record, []string{"record_id", "revision", "data"}, nil) || !snapshotNonEmptyString(record["record_id"]) || !snapshotNonEmptyString(record["revision"]) {
				return fail(fmt.Sprintf("items[%d].%s", index, objectKey))
			}
			data, ok := record["data"].(map[string]any)
			if !ok {
				return fail(fmt.Sprintf("items[%d].%s.data", index, objectKey))
			}
			object := objects[objectKey]
			fieldCatalog := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
			for _, field := range object.Fields {
				if strings.TrimSpace(field.DisabledAt) == "" {
					fieldCatalog[strings.TrimSpace(field.Key)] = field
				}
			}
			for _, field := range fieldCatalog {
				if field.Required {
					if _, exists := data[field.Key]; !exists {
						return fail(fmt.Sprintf("items[%d].%s.data.%s", index, objectKey, field.Key))
					}
				}
			}
			projectedData := make(map[string]any, len(data))
			for fieldKey, fieldValue := range data {
				field, exists := fieldCatalog[fieldKey]
				if !exists {
					return fail(fmt.Sprintf("items[%d].%s.data.%s", index, objectKey, fieldKey))
				}
				if !recordpolicy.RecordCanReadObjectFieldForPrincipal(principal, object, field) {
					continue
				}
				if recordpolicy.RecordFieldReadMaskedForPrincipal(principal, objectKey, fieldKey) {
					fieldValue = recordpolicy.RecordMaskFieldValue(field, fieldValue)
				}
				projectedData[fieldKey] = fieldValue
			}
			projectedItem[objectKey] = map[string]any{"record_id": record["record_id"], "revision": record["revision"], "data": projectedData}
		}
		projectedItems = append(projectedItems, projectedItem)
	}
	result := map[string]any{"items": projectedItems}
	if cursor, exists := page["next_cursor"]; exists {
		result["next_cursor"] = cursor
	}
	return result, nil
}

func snapshotExactKeys(value map[string]any, required, optional []string) bool {
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = true
		if _, exists := value[key]; !exists {
			return false
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range value {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func snapshotNonEmptyString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func snapshotInteger(value any) bool {
	switch typed := value.(type) {
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0) && math.Trunc(typed) == typed
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func snapshotPositiveInteger(value any) bool {
	if !snapshotInteger(value) {
		return false
	}
	switch typed := value.(type) {
	case float64:
		return typed > 0
	case int:
		return typed > 0
	case int8:
		return typed > 0
	case int16:
		return typed > 0
	case int32:
		return typed > 0
	case int64:
		return typed > 0
	case uint:
		return typed > 0
	case uint8:
		return typed > 0
	case uint16:
		return typed > 0
	case uint32:
		return typed > 0
	case uint64:
		return typed > 0
	default:
		return false
	}
}

func cloneSnapshotMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
