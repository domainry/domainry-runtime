package metadata

import (
	"strings"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func quotedColumns(store *database.RuntimeStore, columns []string) []string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return values
}

func dbValue(value any) any {
	switch typed := value.(type) {
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return value
	}
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
