package record

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type recordRows interface {
	Columns() ([]string, error)
	Next() bool
	Scan(...any) error
	Err() error
}

func recordsFromRows(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, rows recordRows) ([]recordmodel.Record, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("inspect rows: %w", err)
	}
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	records := []recordmodel.Record{}
	for rows.Next() {
		values := make([]any, len(columns))
		scans := make([]any, len(columns))
		for i := range values {
			scans[i] = &values[i]
		}
		if err := rows.Scan(scans...); err != nil {
			return nil, fmt.Errorf("scan record: %w", err)
		}
		record := recordmodel.Record{Data: map[string]any{}}
		for i, column := range columns {
			value := normalizeDBValue(profile, fields[column], values[i])
			switch column {
			case "id":
				record.ID = fmt.Sprint(value)
			case "created_at":
				record.CreatedAt = recordTimestampValue(value)
			case "updated_at":
				record.UpdatedAt = recordTimestampValue(value)
			case "workspace_id":
				record.WorkspaceID = recordStringValue(value)
			case "deleted":
				record.Deleted = recordDeletedValue(value)
			case "ext_info":
				record.ExtInfo = recordExtInfoValue(value)
			case "create_by":
				record.CreateBy = recordStringValue(value)
			case "update_by":
				record.UpdateBy = recordStringValue(value)
			case "owner_user_id":
				record.OwnerUserID = recordStringValue(value)
			case "owner_org_id":
				record.OwnerOrgID = recordStringValue(value)
			default:
				if !recordvalidation.RecordIsEmptyValue(value) {
					record.Data[column] = value
				}
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}
	return records, nil
}

func recordStringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func recordTimestampValue(value any) string {
	if timestamp, ok := value.(time.Time); ok {
		return timestamp.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprint(value)
}

func recordDeletedValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		return text == "1" || text == "true" || text == "yes" || text == "on"
	}
}

func recordExtInfoValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	var result map[string]any
	if json.Unmarshal([]byte(fmt.Sprint(value)), &result) != nil || len(result) == 0 {
		return nil
	}
	return result
}

func recordExtInfoDBValue(value map[string]any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode record ext_info: %w", err)
	}
	return string(encoded), nil
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

func dbFieldValue(profile persistencedriver.EngineProfile, field definitionmodel.FieldSchema, value any) any {
	if strings.TrimSpace(field.Type) != "currency" && strings.TrimSpace(field.Type) != "percent" {
		return dbValue(value)
	}
	config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
	if err != nil {
		return value
	}
	normalized, err := recordmodel.RecordNormalizeDecimal(value, config)
	if err != nil {
		return value
	}
	if !profile.OrderedDecimalTextStorage() {
		return normalized
	}
	encoded, err := recordmodel.RecordEncodeSQLiteDecimal(normalized, config)
	if err != nil {
		return value
	}
	return encoded
}

func normalizeDBValue(profile persistencedriver.EngineProfile, field definitionmodel.FieldSchema, value any) any {
	switch typed := value.(type) {
	case []byte:
		value = string(typed)
	default:
		value = typed
	}
	switch field.Type {
	case "boolean":
		switch typed := value.(type) {
		case bool:
			return typed
		case int64:
			return typed != 0
		case int:
			return typed != 0
		case float64:
			return typed != 0
		case string:
			text := strings.TrimSpace(strings.ToLower(typed))
			return text == "true" || text == "1" || text == "yes" || text == "on"
		default:
			return value
		}
	case "currency", "percent":
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return value
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if profile.OrderedDecimalTextStorage() {
			if decoded, err := recordmodel.RecordDecodeSQLiteDecimal(text, config); err == nil {
				return decoded
			}
		}
		if normalized, err := recordmodel.RecordNormalizeDecimal(text, config); err == nil {
			return normalized
		}
		return value
	case "integer":
		switch typed := value.(type) {
		case int64:
			return typed
		case int:
			return int64(typed)
		case float64:
			return int64(typed)
		case string:
			var integer int64
			if _, err := fmt.Sscan(strings.TrimSpace(typed), &integer); err == nil {
				return integer
			}
			return typed
		default:
			return value
		}
	case "number":
		switch typed := value.(type) {
		case int64:
			return float64(typed)
		case int:
			return float64(typed)
		case float32:
			return float64(typed)
		case string:
			var number float64
			if _, err := fmt.Sscan(typed, &number); err == nil {
				return number
			}
			return typed
		default:
			return value
		}
	default:
		return value
	}
}

// NormalizeRecordDatabaseValue is shared by persistence adapters that read
// Runtime object columns without going through RecordStore row scanning.
func NormalizeRecordDatabaseValue(profile persistencedriver.EngineProfile, field definitionmodel.FieldSchema, value any) any {
	return normalizeDBValue(profile, field, value)
}

// RecordDatabaseFieldValue encodes a normalized field value for the active
// database representation, including SQLite exact-decimal text ordering.
func RecordDatabaseFieldValue(profile persistencedriver.EngineProfile, field definitionmodel.FieldSchema, value any) any {
	return dbFieldValue(profile, field, value)
}
