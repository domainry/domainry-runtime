package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"fmt"
	"regexp"
	"strings"
)

var businessFieldReferencePattern = regexp.MustCompile(`\$(?:record|candidate|before)\.([a-z][a-z0-9_]*)`)

func addExpressionFieldReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, fromType, fromKey, objectKey string, value any, path string) {
	if objectKey == "" || value == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		for _, match := range businessFieldReferencePattern.FindAllStringSubmatch(typed, -1) {
			builder.Edge(fromType, fromKey, "field", objectKey+"."+match[1], "reads_field", path)
		}
	case map[string]any:
		for key, child := range typed {
			addExpressionFieldReferences(builder, fromType, fromKey, objectKey, child, path+"."+key)
		}
	case []any:
		for index, child := range typed {
			addExpressionFieldReferences(builder, fromType, fromKey, objectKey, child, fmt.Sprintf("%s[%d]", path, index))
		}
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if strings.HasPrefix(text, "$") {
			addExpressionFieldReferences(builder, fromType, fromKey, objectKey, text, path)
		}
	}
}
