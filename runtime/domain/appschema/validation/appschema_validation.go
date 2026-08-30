package validation

import (
	"fmt"
	"sort"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func ApplicationSchemaFirstDefinitionIssueError(issues []appschemamodel.ApplicationDefinitionValidationIssue) error {
	if len(issues) == 0 {
		return nil
	}
	issue := issues[0]
	params := make(map[string]string, len(issue.Params)+1)
	for key, value := range issue.Params {
		params[key] = value
	}
	if strings.TrimSpace(params["field"]) == "" && strings.TrimSpace(issue.FieldPath) != "" {
		params["field"] = issue.FieldPath
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		values = append(values, key, params[key])
	}
	return badRequest(issue.ErrorCode, values...)
}

func ApplicationSchemaStringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func ApplicationSchemaNormalizedDefinitionValue(value any) string {
	if value == nil {
		return ""
	}
	result := strings.TrimSpace(fmt.Sprint(value))
	if result == "<nil>" {
		return ""
	}
	return result
}
