package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// The deterministic variant consumes the same saved business evidence as a
// real model. It obtains every cursor, record ID and value from tool results.
type businessWebModel struct{}

func (businessWebModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected text-only business request")
}
func (businessWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "business-web", Fingerprint: "business-web-v1"}
}
func (businessWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	tool := func(name, id string, args any) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: name, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	query := func(page int, cursor string) (agentsdk.ConversationStepResult, error) {
		return tool("query_records", fmt.Sprintf("records-%d", page), agentsdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "balance"}, PageSize: 1, Cursor: cursor, Sort: []agentsdk.ConversationBusinessSort{{Field: "name", Direction: "asc"}}})
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "tool" {
		return tool("business_catalog", "objects", map[string]any{"object_key": "customer"})
	}
	var records []agentsdk.ConversationBusinessRecord
	var lastEvidence agentsdk.ConversationBusinessEvidence
	for _, message := range in.Messages {
		if message.Role != "tool" {
			continue
		}
		var result agentsdk.ConversationToolResult
		var evidence agentsdk.ConversationBusinessEvidence
		if json.Unmarshal([]byte(message.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &evidence) != nil {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("business tool did not complete")
		}
		if strings.HasPrefix(message.ToolCallID, "records-") {
			var page agentsdk.ConversationBusinessRecordPage
			if err := json.Unmarshal(evidence.Data, &page); err != nil {
				return agentsdk.ConversationStepResult{}, err
			}
			records = append(records, page.Items...)
		}
		if message.ToolCallID == last.ToolCallID {
			lastEvidence = evidence
		}
	}
	switch last.ToolCallID {
	case "objects":
		return tool("business_catalog", "actions", map[string]any{"kind": "actions", "object_key": "customer"})
	case "actions":
		return query(1, "")
	case "detail":
		var rows []string
		for _, record := range records {
			var name string
			_ = json.Unmarshal(record.Data["name"], &name)
			rows = append(rows, name+"："+string(record.Data["balance"]))
		}
		text := "已逐页读取可访问的客户，并读取第一条详情：\n" + strings.Join(rows, "\n")
		if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: text}); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
	default:
		var page agentsdk.ConversationBusinessRecordPage
		if err := json.Unmarshal(lastEvidence.Data, &page); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		if page.HasNext {
			if page.NextCursor == "" {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("missing next cursor")
			}
			return query(page.Page+1, page.NextCursor)
		}
		if len(records) == 0 {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("no actual record to read")
		}
		return tool("get_record", "detail", agentsdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: records[0].ID, Fields: []string{"name", "balance"}})
	}
}
