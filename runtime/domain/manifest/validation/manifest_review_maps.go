package validation

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func objectMap(manifest manifestmodel.ManifestSchema) map[string]definitionmodel.ObjectSchema {
	out := map[string]definitionmodel.ObjectSchema{}
	for _, object := range manifest.Objects {
		if key := strings.TrimSpace(object.Key); key != "" {
			out[key] = object
		}
	}
	return out
}

func fieldMap(object definitionmodel.ObjectSchema) map[string]definitionmodel.FieldSchema {
	out := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		if key := strings.TrimSpace(field.Key); key != "" {
			out[key] = field
		}
	}
	return out
}

func hasNewString(next []string, previous []string) bool {
	seen := map[string]bool{}
	for _, value := range previous {
		seen[strings.TrimSpace(value)] = true
	}
	for _, value := range next {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			return true
		}
	}
	return false
}
