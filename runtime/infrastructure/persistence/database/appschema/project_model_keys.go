package appschema

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func metadataJoinedKey(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "." + right
}

func validationMetadataKey(objectKey string, index int, validation definitionmodel.ValidationSchema) string {
	parts := []string{objectKey, validation.Type, validation.FieldKey, strings.Join(validation.Fields, "_")}
	out := []string{}
	for _, part := range parts {
		if text := strings.Trim(strings.TrimSpace(part), "."); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return fmt.Sprintf("%s.validation.%d", strings.TrimSpace(objectKey), index+1)
	}
	return strings.Join(out, ".")
}
