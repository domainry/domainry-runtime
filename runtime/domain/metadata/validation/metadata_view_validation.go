package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

var metadataViewConfigKeys = map[string]bool{
	"business_view": true, "columns": true, "end_field": true, "filters": true, "group_by": true,
	"lane_field": true, "page_size": true, "route": true, "search_fields": true, "sort": true,
	"start_field": true, "title_field": true,
}

func MetadataValidateViewDefinition(resourceKey string, payload json.RawMessage, objects []definitionmodel.ObjectSchema) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var view definitionmodel.ViewSchema
	if err := decoder.Decode(&view); err != nil {
		return nil, badRequest("backend.metadata.view_definition_invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, badRequest("backend.metadata.view_definition_invalid")
	}
	resourceKey, view.Key = strings.TrimSpace(resourceKey), strings.TrimSpace(view.Key)
	if resourceKey == "" {
		resourceKey = view.Key
	}
	view.Name, view.ObjectKey, view.Type = strings.TrimSpace(view.Name), strings.TrimSpace(view.ObjectKey), strings.TrimSpace(view.Type)
	if view.Key == "" || view.Key != resourceKey {
		return nil, badRequest("backend.metadata.view_key_mismatch", "view", resourceKey)
	}
	if view.Name == "" {
		return nil, badRequest("backend.metadata.view_name_required", "view", resourceKey)
	}
	object, found := metadataViewObject(objects, view.ObjectKey)
	if view.ObjectKey == "" {
		return nil, badRequest("backend.metadata.view_object_required", "view", resourceKey)
	}
	if !found {
		return nil, badRequest("backend.metadata.view_object_not_found", "view", resourceKey, "object", view.ObjectKey)
	}
	if view.Type == "" {
		return nil, badRequest("backend.metadata.view_type_required", "view", resourceKey)
	}
	if view.Config == nil {
		view.Config = map[string]any{}
	}
	if err := metadataValidateViewConfig(view.Config, object); err != nil {
		return nil, err
	}
	// The view was decoded from JSON and only normalized with strings/maps whose
	// values came from that payload, so marshaling cannot introduce a new type error.
	normalized, _ := json.Marshal(view)
	return normalized, nil
}

func metadataValidateViewConfig(config map[string]any, object definitionmodel.ObjectSchema) error {
	for key := range config {
		if !metadataViewConfigKeys[key] {
			return badRequest("backend.metadata.view_config_unknown", "config_key", key)
		}
	}
	for _, key := range []string{"columns", "search_fields"} {
		values, ok := config[key]
		if !ok {
			continue
		}
		items, ok := values.([]any)
		if !ok {
			return badRequest("backend.metadata.view_config_invalid", "config_key", key)
		}
		for _, item := range items {
			if err := metadataValidateViewField(object, strings.TrimSpace(fmt.Sprint(item)), key); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"end_field", "group_by", "lane_field", "start_field", "title_field"} {
		if value, ok := config[key]; ok {
			if err := metadataValidateViewField(object, strings.TrimSpace(fmt.Sprint(value)), key); err != nil {
				return err
			}
		}
	}
	if value, ok := config["page_size"]; ok {
		number, valid := value.(float64)
		if !valid || number < 1 || number > 200 || number != float64(int(number)) {
			return badRequest("backend.metadata.view_page_size_invalid")
		}
	}
	if value, ok := config["sort"]; ok {
		items, valid := value.([]any)
		if !valid {
			return badRequest("backend.metadata.view_config_invalid", "config_key", "sort")
		}
		for _, item := range items {
			field := ""
			switch typed := item.(type) {
			case string:
				field = strings.TrimPrefix(strings.TrimSpace(typed), "-")
			case map[string]any:
				field = strings.TrimSpace(fmt.Sprint(typed["field"]))
				direction := strings.ToLower(strings.TrimSpace(fmt.Sprint(typed["direction"])))
				if direction != "" && direction != "asc" && direction != "desc" {
					return badRequest("backend.metadata.view_config_invalid", "config_key", "sort.direction")
				}
			default:
				return badRequest("backend.metadata.view_config_invalid", "config_key", "sort")
			}
			if err := metadataValidateViewField(object, field, "sort"); err != nil {
				return err
			}
		}
	}
	if value, ok := config["filters"]; ok {
		items, valid := value.([]any)
		if !valid {
			return badRequest("backend.metadata.view_config_invalid", "config_key", "filters")
		}
		for _, item := range items {
			filter, valid := item.(map[string]any)
			if !valid {
				return badRequest("backend.metadata.view_config_invalid", "config_key", "filters")
			}
			if err := metadataValidateViewField(object, strings.TrimSpace(fmt.Sprint(filter["field"])), "filters"); err != nil {
				return err
			}
			source := strings.TrimSpace(fmt.Sprint(filter["source"]))
			if source != "" && source != "current_user" {
				return badRequest("backend.metadata.view_config_invalid", "config_key", "filters.source")
			}
		}
	}
	return nil
}

func metadataValidateViewField(object definitionmodel.ObjectSchema, fieldKey, configKey string) error {
	if fieldKey == "id" {
		return nil
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey && fieldKey != "" {
			return nil
		}
	}
	return badRequest("backend.metadata.view_field_not_found", "object", object.Key, "field", fieldKey, "config_key", configKey)
}

func metadataViewObject(objects []definitionmodel.ObjectSchema, key string) (definitionmodel.ObjectSchema, bool) {
	for _, object := range objects {
		if strings.TrimSpace(object.Key) == strings.TrimSpace(key) && strings.TrimSpace(key) != "" {
			return object, true
		}
	}
	return definitionmodel.ObjectSchema{}, false
}
