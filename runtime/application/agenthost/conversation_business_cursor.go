package agenthost

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// A cursor is a bounded read position, never an authorization credential.
// Its values come only from readable, unmasked sort fields. Even a modified
// cursor goes through current Identity, schema/value validation and row policy.
// Scope/query fingerprints prevent accidental cross-user/query reuse.
type conversationBusinessCursor struct {
	Version int               `json:"v"`
	Scope   string            `json:"scope"`
	Query   string            `json:"query"`
	Page    int               `json:"page"`
	Values  []json.RawMessage `json:"values"`
}

func businessCursorQuery(q agentsdk.ConversationBusinessQuery, relationScope ...string) string {
	q.Cursor = ""
	q.Page = 0
	if len(relationScope) > 0 && relationScope[0] != "" {
		return conversationBusinessDigest([]any{q, relationScope[0]})
	}
	return conversationBusinessDigest(q)
}
func (h *ConversationBusinessHost) cursorScope(a agentsdk.ConversationAuthority) string {
	return conversationBusinessDigest([]string{h.source, a.RuntimeID, a.WorkspaceID, a.UserID})
}
func (h *ConversationBusinessHost) decodeCursor(q agentsdk.ConversationBusinessQuery, a agentsdk.ConversationAuthority, sort []recordmodel.RecordSortRule, relationScope ...string) (conversationBusinessCursor, error) {
	var cursor conversationBusinessCursor
	if q.Cursor == "" || len(q.Cursor) > 16384 {
		return cursor, conversationBusinessError("bad_request")
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	if err != nil {
		return cursor, conversationBusinessError("bad_request")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Scope != h.cursorScope(a) || cursor.Query != businessCursorQuery(q, relationScope...) || len(cursor.Values) != len(sort) || cursor.Page < 2 || cursor.Page > 1000000 || q.Page != 0 && q.Page != cursor.Page {
		return cursor, conversationBusinessError("bad_request")
	}
	return cursor, nil
}
func (h *ConversationBusinessHost) encodeCursor(q agentsdk.ConversationBusinessQuery, a agentsdk.ConversationAuthority, sort []recordmodel.RecordSortRule, last recordmodel.Record, relationScope ...string) (string, error) {
	cursor := conversationBusinessCursor{Version: 1, Scope: h.cursorScope(a), Query: businessCursorQuery(q, relationScope...), Page: q.Page + 1}
	for _, rule := range sort {
		value, present := last.QuerySortValues[rule.Field]
		if rule.Field == "id" {
			value = last.ID
			present = true
		}
		if !present {
			return "", conversationBusinessError("unavailable")
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return "", conversationBusinessError("unavailable")
		}
		cursor.Values = append(cursor.Values, raw)
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", conversationBusinessError("unavailable")
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > 16384 {
		return "", conversationBusinessError("bad_request")
	}
	return encoded, nil
}

func businessLogical(operator string, nodes []recordmodel.RecordFilterExpression) *recordmodel.RecordFilterExpression {
	if len(nodes) == 0 {
		return nil
	}
	if len(nodes) == 1 {
		return &nodes[0]
	}
	return &recordmodel.RecordFilterExpression{Operator: operator, Children: nodes}
}

// Lexicographic keyset ordering, with a final unique ID tie-breaker. Runtime
// applies NULLS LAST to the matching SQL ordering on supported dialects.
func businessCursorFilter(cursor conversationBusinessCursor, sort []recordmodel.RecordSortRule) (*recordmodel.RecordFilterExpression, error) {
	prefix := []recordmodel.RecordFilterExpression{}
	alternatives := []recordmodel.RecordFilterExpression{}
	for i, rule := range sort {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(cursor.Values[i]))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
			return nil, conversationBusinessError("bad_request")
		}
		if rule.Field == "id" {
			if id, ok := value.(string); !ok || id == "" {
				return nil, conversationBusinessError("bad_request")
			}
		}
		if value != nil {
			op := "gt"
			if rule.Direction == "desc" {
				op = "lt"
			}
			comparison := recordmodel.RecordFilterExpression{Operator: op, Field: rule.Field, Value: value}
			if rule.Field != "id" {
				comparison = recordmodel.RecordFilterExpression{Operator: "or", Children: []recordmodel.RecordFilterExpression{comparison, {Operator: "is_null", Field: rule.Field}}}
			}
			terms := append(append([]recordmodel.RecordFilterExpression(nil), prefix...), comparison)
			alternatives = append(alternatives, *businessLogical("and", terms))
			prefix = append(prefix, recordmodel.RecordFilterExpression{Operator: "eq", Field: rule.Field, Value: value})
		} else {
			prefix = append(prefix, recordmodel.RecordFilterExpression{Operator: "is_null", Field: rule.Field})
		}
	}
	return businessLogical("or", alternatives), nil
}
