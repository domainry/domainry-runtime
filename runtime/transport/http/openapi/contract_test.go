package openapi

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

import (
	"fmt"
	"strings"
	"testing"
)

func TestOpenAPIOperationsShareSecurityResponseAndOperationIDContract(t *testing.T) {
	spec := Build(appschemamodel.ApplicationSchemaSnapshot{})
	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	walkOpenAPIRefs(t, spec, schemas)
	paths, _ := spec["paths"].(map[string]any)
	seen := map[string]string{}
	for path, rawPath := range paths {
		pathSpec, _ := rawPath.(map[string]any)
		for method, rawOperation := range pathSpec {
			if !isHTTPMethod(method) {
				continue
			}
			op, ok := rawOperation.(map[string]any)
			if !ok {
				t.Errorf("%s %s operation is not an object", method, path)
				continue
			}
			operationID, _ := op["operationId"].(string)
			if strings.TrimSpace(operationID) == "" {
				t.Errorf("%s %s has no operationId", method, path)
			} else if previous := seen[operationID]; previous != "" {
				t.Errorf("operationId %q reused by %s and %s %s", operationID, previous, method, path)
			} else {
				seen[operationID] = fmt.Sprintf("%s %s", method, path)
			}
			if _, ok := op["security"].([]map[string]any); !ok {
				t.Errorf("%s %s has no explicit security declaration", method, path)
			}
			responses, _ := op["responses"].(map[string]any)
			if responses["200"] == nil && responses["201"] == nil && responses["202"] == nil && responses["204"] == nil {
				t.Errorf("%s %s has no success response", method, path)
			}
			defaultResponse, _ := responses["default"].(map[string]any)
			content, _ := defaultResponse["content"].(map[string]any)
			jsonContent, _ := content["application/json"].(map[string]any)
			schema, _ := jsonContent["schema"].(map[string]any)
			if schema["$ref"] != "#/components/schemas/Error" {
				t.Errorf("%s %s default error schema = %v", method, path, schema["$ref"])
			}
		}
	}
}

func walkOpenAPIRefs(t *testing.T, value any, schemas map[string]any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if ref, _ := typed["$ref"].(string); ref != "" {
			const prefix = "#/components/schemas/"
			if !strings.HasPrefix(ref, prefix) || schemas[strings.TrimPrefix(ref, prefix)] == nil {
				t.Errorf("unresolved schema ref %q", ref)
			}
		}
		for _, child := range typed {
			walkOpenAPIRefs(t, child, schemas)
		}
	case []any:
		for _, child := range typed {
			walkOpenAPIRefs(t, child, schemas)
		}
	case []map[string]any:
		for _, child := range typed {
			walkOpenAPIRefs(t, child, schemas)
		}
	}
}

func isHTTPMethod(value string) bool {
	switch strings.ToUpper(value) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}
