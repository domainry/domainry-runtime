package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const projectRecordCursorMaximumLength = 16384

// projectRecordCursor is a read position, not an authorization credential.
// Its values are used only through Runtime's current schema validation, field
// query policy and row authorization. Scope/query fingerprints prevent a
// continuation from being accidentally reused by another user or list query.
type projectRecordCursor struct {
	Version int               `json:"v"`
	Scope   string            `json:"scope"`
	Query   string            `json:"query"`
	Page    int               `json:"page"`
	Total   int               `json:"total"`
	Values  []json.RawMessage `json:"values"`
}

func projectRecordCursorSorts(values []runtimeengine.Sort) []recordmodel.RecordSortRule {
	sorts := make([]recordmodel.RecordSortRule, 0, len(values)+1)
	hasID := false
	for _, value := range values {
		field := strings.TrimSpace(value.Field)
		direction := strings.ToLower(strings.TrimSpace(value.Direction))
		if direction != "desc" {
			direction = "asc"
		}
		sorts = append(sorts, recordmodel.RecordSortRule{Field: field, Direction: direction})
		hasID = hasID || field == "id"
	}
	if len(sorts) == 0 {
		return []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}
	}
	if !hasID {
		sorts = append(sorts, recordmodel.RecordSortRule{Field: "id", Direction: "asc"})
	}
	return sorts
}

func projectRecordCursorRequired(sorts []recordmodel.RecordSortRule) bool {
	return len(sorts) != 1 || sorts[0].Field != "id" || sorts[0].Direction != "asc"
}

func projectRecordCursorScope(runtimeID string, principal principalmodel.Principal) string {
	return projectRecordCursorDigest([]string{
		strings.TrimSpace(runtimeID), principal.WorkspaceID, principal.UserID,
		principal.RoleKey, principal.EffectiveAuthorizationRevision(),
	})
}

func projectRecordCursorQuery(objectKey string, query runtimeengine.Query, sorts []recordmodel.RecordSortRule) string {
	return projectRecordCursorDigest(struct {
		ObjectKey    string
		PageSize     int
		Search       string
		SearchFields []string
		Filters      map[string]any
		Sorts        []recordmodel.RecordSortRule
		SelectFields []string
	}{
		ObjectKey: strings.TrimSpace(objectKey), PageSize: query.PageSize,
		Search: strings.TrimSpace(query.Search), SearchFields: append([]string(nil), query.SearchFields...),
		Filters: cloneAnyMap(query.Filters), Sorts: append([]recordmodel.RecordSortRule(nil), sorts...),
		SelectFields: append([]string(nil), query.SelectFields...),
	})
}

func projectRecordCursorDigest(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func decodeProjectRecordCursor(raw, runtimeID, objectKey string, query runtimeengine.Query, principal principalmodel.Principal, sorts []recordmodel.RecordSortRule) (projectRecordCursor, error) {
	var cursor projectRecordCursor
	if raw == "" || len(raw) > projectRecordCursorMaximumLength {
		return cursor, projectRecordCursorInvalid(nil)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, projectRecordCursorInvalid(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF ||
		cursor.Version != 1 || cursor.Scope != projectRecordCursorScope(runtimeID, principal) ||
		cursor.Query != projectRecordCursorQuery(objectKey, query, sorts) || len(cursor.Values) != len(sorts) ||
		cursor.Page < 2 || cursor.Page > 1000000 || cursor.Total < 0 || query.Page > 0 && query.Page != cursor.Page {
		return projectRecordCursor{}, projectRecordCursorInvalid(nil)
	}
	return cursor, nil
}

func encodeProjectRecordCursor(runtimeID, objectKey string, query runtimeengine.Query, principal principalmodel.Principal, sorts []recordmodel.RecordSortRule, total, nextPage int, last recordmodel.Record) (string, error) {
	cursor := projectRecordCursor{
		Version: 1, Scope: projectRecordCursorScope(runtimeID, principal), Query: projectRecordCursorQuery(objectKey, query, sorts),
		Page: nextPage, Total: total,
	}
	for _, rule := range sorts {
		value, present := last.QuerySortValues[rule.Field]
		if rule.Field == "id" {
			value, present = last.ID, strings.TrimSpace(last.ID) != ""
		}
		if !present {
			return "", runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.project_engine.pagination_cursor_unavailable", nil, nil)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return "", runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.project_engine.pagination_cursor_unavailable", nil, err)
		}
		cursor.Values = append(cursor.Values, raw)
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.project_engine.pagination_cursor_unavailable", nil, err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > projectRecordCursorMaximumLength {
		return "", runtimeengine.NewError(runtimeengine.ErrorUnavailable, "backend.project_engine.pagination_cursor_unavailable", nil, nil)
	}
	return encoded, nil
}

func projectRecordCursorFilter(cursor projectRecordCursor, sorts []recordmodel.RecordSortRule) (*recordmodel.RecordFilterExpression, error) {
	prefix := []recordmodel.RecordFilterExpression{}
	alternatives := []recordmodel.RecordFilterExpression{}
	for index, rule := range sorts {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(cursor.Values[index]))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil, projectRecordCursorInvalid(nil)
		}
		if rule.Field == "id" {
			if id, ok := value.(string); !ok || strings.TrimSpace(id) == "" {
				return nil, projectRecordCursorInvalid(nil)
			}
		}
		if value != nil {
			operator := "gt"
			if rule.Direction == "desc" {
				operator = "lt"
			}
			comparison := recordmodel.RecordFilterExpression{Operator: operator, Field: rule.Field, Value: value}
			if rule.Field != "id" {
				comparison = recordmodel.RecordFilterExpression{Operator: "or", Children: []recordmodel.RecordFilterExpression{
					comparison,
					{Operator: "is_null", Field: rule.Field},
				}}
			}
			terms := append(append([]recordmodel.RecordFilterExpression(nil), prefix...), comparison)
			alternatives = append(alternatives, *projectRecordCursorLogical("and", terms))
			prefix = append(prefix, recordmodel.RecordFilterExpression{Operator: "eq", Field: rule.Field, Value: value})
		} else {
			prefix = append(prefix, recordmodel.RecordFilterExpression{Operator: "is_null", Field: rule.Field})
		}
	}
	return projectRecordCursorLogical("or", alternatives), nil
}

func projectRecordCursorLogical(operator string, nodes []recordmodel.RecordFilterExpression) *recordmodel.RecordFilterExpression {
	if len(nodes) == 0 {
		return nil
	}
	if len(nodes) == 1 {
		return &nodes[0]
	}
	return &recordmodel.RecordFilterExpression{Operator: operator, Children: nodes}
}

func projectRecordCursorInvalid(cause error) error {
	return runtimeengine.NewError(runtimeengine.ErrorBadRequest, "backend.project_engine.pagination_cursor_invalid", nil, cause)
}
