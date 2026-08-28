package datamigration

import (
	"fmt"
	"strconv"
	"strings"
)

func primaryKeyPosition(columns []ColumnInventory, name string) int {
	for _, column := range columns {
		if column.Name == name {
			return column.PrimaryKey
		}
	}
	return 0
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func ParseEngine(value string) (Engine, error) {
	switch Engine(strings.ToLower(strings.TrimSpace(value))) {
	case EngineSQLite:
		return EngineSQLite, nil
	case EnginePostgres, "postgresql", "pgx":
		return EnginePostgres, nil
	case EngineMySQL:
		return EngineMySQL, nil
	default:
		return "", fmt.Errorf("unsupported database engine %q", value)
	}
}

func mysqlQuote(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func parseSQLiteBoolean(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case int64:
		return typed != 0, typed == 0 || typed == 1
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}
