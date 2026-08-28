package policy

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

func ActionMapPayloadField(raw map[string]any) definitionmodel.FieldSchema {
	key := ActionNormalizedValue(raw["key"])
	if key == "" {
		return definitionmodel.FieldSchema{}
	}
	fieldType := ActionNormalizedValue(raw["type"])
	if fieldType == "" {
		fieldType = "text"
	}
	config := make(map[string]any, len(raw))
	for key, value := range raw {
		config[key] = value
	}
	for _, key := range []string{"key", "name", "type", "required", "default", "default_value"} {
		delete(config, key)
	}
	name := ActionNormalizedValue(raw["name"])
	if name == "" {
		name = key
	}
	required, _ := raw["required"].(bool)
	defaultValue := raw["default"]
	if defaultValue == nil {
		defaultValue = raw["default_value"]
	}
	return definitionmodel.FieldSchema{Key: key, Name: name, Type: fieldType, Config: config, Required: required, Default: defaultValue, DefaultValue: raw["default_value"]}
}
