package report

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type reportObjectSQLCursor struct {
	Version int                          `json:"v"`
	Values  []reportObjectSQLCursorValue `json:"values"`
}

type reportObjectSQLCursorValue struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

func encodeReportObjectSQLCursor(values []any) (string, error) {
	cursor := reportObjectSQLCursor{Version: 1, Values: make([]reportObjectSQLCursorValue, len(values))}
	for index, value := range values {
		normalized, err := normalizeReportObjectSQLCursorValue(value)
		if err != nil {
			return "", err
		}
		cursor.Values[index] = normalized
	}
	content, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(content), nil
}

func decodeReportObjectSQLCursor(value string, expectedValues int) ([]any, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	content, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode report object SQL cursor: %w", err)
	}
	var cursor reportObjectSQLCursor
	if err := json.Unmarshal(content, &cursor); err != nil || cursor.Version != 1 || len(cursor.Values) != expectedValues {
		return nil, fmt.Errorf("invalid report object SQL cursor")
	}
	values := make([]any, len(cursor.Values))
	for index, encoded := range cursor.Values {
		decoded, decodeErr := decodeReportObjectSQLCursorValue(encoded)
		if decodeErr != nil {
			return nil, decodeErr
		}
		values[index] = decoded
	}
	return values, nil
}

func normalizeReportObjectSQLCursorValue(value any) (reportObjectSQLCursorValue, error) {
	switch typed := value.(type) {
	case nil:
		return reportObjectSQLCursorValue{Kind: "null"}, nil
	case int64:
		return reportObjectSQLCursorValue{Kind: "integer", Value: strconv.FormatInt(typed, 10)}, nil
	case int:
		return reportObjectSQLCursorValue{Kind: "integer", Value: strconv.Itoa(typed)}, nil
	case float64:
		return reportObjectSQLCursorValue{Kind: "float", Value: strconv.FormatFloat(typed, 'g', -1, 64)}, nil
	case bool:
		return reportObjectSQLCursorValue{Kind: "boolean", Value: strconv.FormatBool(typed)}, nil
	case []byte:
		return reportObjectSQLCursorValue{Kind: "text", Value: string(typed)}, nil
	case string:
		return reportObjectSQLCursorValue{Kind: "text", Value: typed}, nil
	case time.Time:
		return reportObjectSQLCursorValue{Kind: "time", Value: typed.UTC().Format(time.RFC3339Nano)}, nil
	case sql.RawBytes:
		return reportObjectSQLCursorValue{Kind: "text", Value: string(typed)}, nil
	default:
		return reportObjectSQLCursorValue{}, fmt.Errorf("unsupported report object SQL cursor value %T", value)
	}
}

func decodeReportObjectSQLCursorValue(value reportObjectSQLCursorValue) (any, error) {
	switch value.Kind {
	case "null":
		return nil, nil
	case "integer":
		return strconv.ParseInt(value.Value, 10, 64)
	case "float":
		return strconv.ParseFloat(value.Value, 64)
	case "boolean":
		return strconv.ParseBool(value.Value)
	case "text":
		return value.Value, nil
	case "time":
		return time.Parse(time.RFC3339Nano, value.Value)
	default:
		return nil, fmt.Errorf("invalid report object SQL cursor value kind %q", value.Kind)
	}
}
