package agentdialog

import (
	"fmt"
	"html"
	"strings"
)

func agentDialogTruthy(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func mapFromAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return cloneStringAnyMap(typed)
	}
	return map[string]any{}
}

func agentAnalysisProposalDiffHTML(proposed map[string]any) string {
	var builder strings.Builder
	for _, item := range agentAnalysisAnyList(proposed["diff"]) {
		diff := mapFromAny(item)
		field := strings.TrimSpace(fmt.Sprint(diff["field"]))
		if field == "" {
			continue
		}
		builder.WriteString(`<div data-diff-field="`)
		builder.WriteString(html.EscapeString(field))
		builder.WriteString(`" data-current="`)
		builder.WriteString(html.EscapeString(fmt.Sprint(diff["current"])))
		builder.WriteString(`" data-proposed="`)
		builder.WriteString(html.EscapeString(fmt.Sprint(diff["proposed"])))
		builder.WriteString(`"></div>`)
	}
	return builder.String()
}

func agentAnalysisProposalAffectedHTML(proposed map[string]any) string {
	var builder strings.Builder
	for _, item := range agentAnalysisAnyList(proposed["affected_records"]) {
		record := mapFromAny(item)
		objectKey := strings.TrimSpace(fmt.Sprint(record["object_key"]))
		recordID := strings.TrimSpace(fmt.Sprint(record["record_id"]))
		if objectKey == "" || recordID == "" {
			continue
		}
		label := valueOrDefault(strings.TrimSpace(fmt.Sprint(record["label"])), recordID)
		builder.WriteString(`<a data-evidence-kind="affected_record" data-reference="record:`)
		builder.WriteString(html.EscapeString(objectKey))
		builder.WriteString(`:`)
		builder.WriteString(html.EscapeString(recordID))
		builder.WriteString(`">`)
		builder.WriteString(html.EscapeString(label))
		builder.WriteString(`</a>`)
	}
	return builder.String()
}
