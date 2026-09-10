package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type businessRelationsWebModel struct{}

func (businessRelationsWebModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected text-only relation request")
}
func (businessRelationsWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "business-relations", Fingerprint: "business-relations-v1"}
}
func (businessRelationsWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	tool := func(name, id string, args any) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: name, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	catalog := func(object string) (agentsdk.ConversationStepResult, error) {
		return tool("business_catalog", "catalog-"+object, agentsdk.ConversationBusinessCatalogQuery{Kind: "relations", ObjectKey: object})
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "tool" {
		return catalog("customer")
	}
	evidence := map[string]agentsdk.ConversationBusinessEvidence{}
	var orders []agentsdk.ConversationBusinessRecord
	for _, message := range in.Messages {
		if message.Role != "tool" {
			continue
		}
		var result agentsdk.ConversationToolResult
		var e agentsdk.ConversationBusinessEvidence
		if json.Unmarshal([]byte(message.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &e) != nil {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("relation tool did not complete")
		}
		evidence[message.ToolCallID] = e
		if strings.HasPrefix(message.ToolCallID, "orders-") {
			var page agentsdk.ConversationBusinessRelatedPage
			if err := json.Unmarshal(e.Data, &page); err != nil {
				return agentsdk.ConversationStepResult{}, err
			}
			orders = append(orders, page.Items...)
		}
	}
	relation := func(object, target, direction string) (string, error) {
		var page agentsdk.ConversationBusinessCatalogPage
		if err := json.Unmarshal(evidence["catalog-"+object].Data, &page); err != nil {
			return "", err
		}
		for _, rel := range page.Relations {
			if rel.ObjectKey == target && rel.Direction == direction {
				return rel.Key, nil
			}
		}
		return "", fmt.Errorf("expected relationship was not published")
	}
	queryOrders := func(page int, cursor string) (agentsdk.ConversationStepResult, error) {
		var parent agentsdk.ConversationBusinessRecordPage
		if json.Unmarshal(evidence["customer"].Data, &parent) != nil || len(parent.Items) != 1 {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("source customer missing")
		}
		key, err := relation("customer", "order", "reverse")
		if err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		return tool("query_related_records", fmt.Sprintf("orders-%d", page), agentsdk.ConversationBusinessRelatedQuery{ObjectKey: parent.ObjectKey, RecordID: parent.Items[0].ID, RelationKey: key, Fields: []string{"name", "amount"}, Sort: []agentsdk.ConversationBusinessSort{{Field: "amount", Direction: "asc"}}, PageSize: 1, Cursor: cursor})
	}
	name := func(record agentsdk.ConversationBusinessRecord) string {
		var s string
		_ = json.Unmarshal(record.Data["name"], &s)
		return s
	}
	switch last.ToolCallID {
	case "catalog-customer":
		return tool("query_records", "customer", agentsdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, Filters: []agentsdk.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(`"Acme"`)}}})
	case "customer":
		return queryOrders(1, "")
	case "catalog-order":
		key, err := relation("order", "project", "forward")
		if err != nil || len(orders) == 0 {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("no actual order/project relationship: %w", err)
		}
		return tool("query_related_records", "project", agentsdk.ConversationBusinessRelatedQuery{ObjectKey: "order", RecordID: orders[0].ID, RelationKey: key, Fields: []string{"name"}})
	case "project":
		return catalog("project")
	case "catalog-project":
		var page agentsdk.ConversationBusinessRelatedPage
		if json.Unmarshal(evidence["project"].Data, &page) != nil || len(page.Items) != 1 {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("actual project missing")
		}
		key, err := relation("project", "customer", "forward")
		if err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		return tool("query_related_records", "back-customer", agentsdk.ConversationBusinessRelatedQuery{ObjectKey: page.ObjectKey, RecordID: page.Items[0].ID, RelationKey: key, Fields: []string{"name"}})
	case "back-customer":
		var project, customer agentsdk.ConversationBusinessRelatedPage
		if json.Unmarshal(evidence["project"].Data, &project) != nil || json.Unmarshal(evidence["back-customer"].Data, &customer) != nil || len(project.Items) != 1 || len(customer.Items) != 1 {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("actual relation targets missing")
		}
		text := "| 订单 | 金额 |\n| --- | --- |\n"
		for _, order := range orders {
			text += "| " + name(order) + " | " + string(order.Data["amount"]) + " |\n"
		}
		text += "\n第一条订单关联项目：" + name(project.Items[0]) + "；从项目查回客户：" + name(customer.Items[0]) + "。"
		if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: text}); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
	default:
		var page agentsdk.ConversationBusinessRelatedPage
		if json.Unmarshal(evidence[last.ToolCallID].Data, &page) != nil {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("invalid order page")
		}
		if page.HasNext {
			return queryOrders(page.Page+1, page.NextCursor)
		}
		return catalog("order")
	}
}
