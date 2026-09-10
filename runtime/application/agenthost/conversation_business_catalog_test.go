package agenthost

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func catalogPrincipal(a agentsdk.ConversationAuthority, permissions []string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: a.UserID, WorkspaceID: a.WorkspaceID}}, accessfixture.Bundle{
		Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll),
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}, {ObjectKey: "request", FieldKey: "subject", Write: true}},
	})
}

func TestConversationBusinessCatalogUsesPublishedInputsAndCurrentOperationPermissions(t *testing.T) {
	host, resolver, reads, a := newConversationBusinessFixture(t)
	permissions := []string{"customer.read", "customer.submit", "request.create", "request.submit", "request.empty", "request.legacy", "workflow.customer.review.run", "workflow.global.review.run", "workflow.disabled.run", "workflow.scheduled.run"}
	resolver.principal = catalogPrincipal(a, permissions)
	two := 2
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{reads.object, {Key: "request", Name: "申请", Fields: []definitionmodel.FieldSchema{{Key: "subject", Type: "text"}}}, {Key: "private", Name: "PRIVATE-OBJECT"}},
		Actions: []definitionmodel.ActionSchema{
			{Key: "customer.submit", ObjectKey: "customer", Label: "提交", PayloadFields: []definitionmodel.ActionPayloadField{
				{Key: "due", Type: "date", Required: true}, {Key: "mode", Type: "select", Options: []string{"draft", "final"}, Required: true, DefaultValue: "draft"},
				{Key: "rows", Type: "object", Repeated: true, Required: true, MaxItems: &two, Fields: []definitionmodel.ActionPayloadField{{Key: "amount", Type: "currency", Required: true}, {Key: "note", Type: "text", SourceObjectKey: "PRIVATE-SOURCE", SourceFieldKey: "PRIVATE-FIELD"}}},
			}},
			{Key: "customer.denied", ObjectKey: "customer", Label: "PRIVATE-ACTION"},
			{Key: "request.submit", ObjectKey: "request", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "subject", Type: "text", Required: true}}},
			{Key: "request.empty", ObjectKey: "request", PayloadFields: []definitionmodel.ActionPayloadField{}},
			{Key: "request.legacy", ObjectKey: "request"},
		},
		Workflows: []definitionmodel.WorkflowSchema{
			{Key: "customer.review", Name: "客户审核", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual", ObjectKey: "customer"}, InputFields: []definitionmodel.WorkflowInputField{{Key: "reason", Type: "text", Required: true}}},
			{Key: "global.review", Name: "全局审核", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, InputFields: []definitionmodel.WorkflowInputField{{Key: "date", Type: "date", Required: true}}},
			{Key: "denied", Name: "PRIVATE-WORKFLOW", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}},
			{Key: "disabled", Name: "PRIVATE-DISABLED", Enabled: false, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}},
			{Key: "scheduled", Name: "PRIVATE-SCHEDULED", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled"}},
		},
	}
	host.schema = appschemaapplication.NewApplicationSchemaQueryApplicationService(businessSchemaSnapshot{snapshot}, nil)
	list, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Limit: 1}, a)
	if err != nil || list.Complete || list.NextCursor != "customer" || len(list.Items) != 1 || len(list.Items[0].Actions) != 0 {
		t.Fatal(list, err)
	}
	second, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{After: list.NextCursor, Limit: 1}, a)
	if err != nil || !second.Complete || len(second.Items) != 1 || second.Items[0].Key != "request" || second.Items[0].Readable == nil || *second.Items[0].Readable {
		t.Fatal(second, err)
	}
	q := agentsdk.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "customer", ActionKey: "customer.submit"}
	detail, err := host.BusinessCatalog(t.Context(), q, a)
	if err != nil || len(detail.Items) != 0 || len(detail.Actions) != 1 {
		t.Fatal(detail, err)
	}
	raw, _ := json.Marshal(detail)
	if strings.Contains(string(raw), "PRIVATE-") || strings.Contains(string(raw), "approval_token") || strings.Contains(string(raw), "initiating_user_id") {
		t.Fatalf("implementation or unauthorized metadata disclosed: %s", raw)
	}
	var schema map[string]any
	if err := json.Unmarshal(detail.Actions[0].InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	if properties["due"].(map[string]any)["format"] != "date" || len(schema["required"].([]any)) != 2 {
		t.Fatal(schema)
	}
	rows := properties["rows"].(map[string]any)
	if rows["maxItems"] != float64(2) || rows["minItems"] != float64(1) || rows["items"].(map[string]any)["additionalProperties"] != false {
		t.Fatal(rows)
	}
	create, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{ObjectKey: "request"}, a)
	if err != nil || len(create.Items) != 1 || len(create.Items[0].Fields) != 0 || create.Items[0].Pagination != "" {
		t.Fatal(create, err)
	}
	actions, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "request", Limit: 1}, a)
	if err != nil || len(actions.Actions) != 1 || actions.Complete || actions.NextCursor != "request.empty" || actions.Actions[0].InputSchema != nil {
		t.Fatal(actions, err)
	}
	secondAction, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "request", After: actions.NextCursor, Limit: 1}, a)
	if err != nil || len(secondAction.Actions) != 1 || secondAction.Actions[0].Key != "request.legacy" {
		t.Fatal(secondAction, err)
	}
	for _, key := range []string{"request.empty", "request.legacy"} {
		operation, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "request", ActionKey: key}, a)
		if err != nil || len(operation.Actions) != 1 {
			t.Fatal(operation, err)
		}
		if (operation.Actions[0].InputSchema == nil) != (key == "request.legacy") {
			t.Fatal("declared empty and unpublished payload contracts conflated")
		}
	}
	if _, err := host.QueryBusinessRecords(t.Context(), agentsdk.ConversationBusinessQuery{ObjectKey: "request"}, a); err == nil || reads.queries != 0 {
		t.Fatal("create-only object became readable")
	}
	if _, err := host.GetBusinessRecord(t.Context(), agentsdk.ConversationBusinessGet{ObjectKey: "request", RecordID: "invented"}, a); err == nil {
		t.Fatal("create-only record read allowed")
	}
	firstWorkflow, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", Limit: 1}, a)
	if err != nil || firstWorkflow.Complete || firstWorkflow.NextCursor != "customer.review" || len(firstWorkflow.Workflows) != 1 || firstWorkflow.Workflows[0].InputSchema != nil {
		t.Fatal(firstWorkflow, err)
	}
	global, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", After: firstWorkflow.NextCursor, Limit: 1}, a)
	if err != nil || !global.Complete || len(global.Workflows) != 1 || global.Workflows[0].Key != "global.review" || len(global.Workflows[0].ObjectKeys) != 0 {
		t.Fatal(global, err)
	}
	associated, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", ObjectKey: "customer"}, a)
	if err != nil || len(associated.Workflows) != 1 || associated.Workflows[0].Key != "customer.review" {
		t.Fatal(associated, err)
	}
	wq := agentsdk.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "global.review"}
	workflow, err := host.BusinessCatalog(t.Context(), wq, a)
	if err != nil || len(workflow.Workflows) != 1 || !strings.Contains(string(workflow.Workflows[0].InputSchema), `"date"`) {
		t.Fatal(workflow, err)
	}
	evidence := func(query agentsdk.ConversationBusinessCatalogQuery, page agentsdk.ConversationBusinessCatalogPage) agentsdk.ConversationBusinessEvidence {
		input, _ := json.Marshal(query)
		data, _ := json.Marshal(page)
		return agentsdk.ConversationBusinessEvidence{Version: 1, Source: host.source, ScopeSHA256: conversationBusinessDigest([]string{host.source, a.RuntimeID, a.WorkspaceID, a.UserID}), Operation: "business_catalog", Input: input, Data: data}
	}
	objectEvidence, workflowEvidence := evidence(q, detail), evidence(wq, workflow)
	for _, e := range []agentsdk.ConversationBusinessEvidence{objectEvidence, workflowEvidence} {
		if err := host.RevalidateBusiness(t.Context(), e, a); err != nil {
			t.Fatal(err)
		}
	}
	resolver.mu.Lock()
	resolver.principal = catalogPrincipal(a, slices.DeleteFunc(slices.Clone(permissions), func(s string) bool { return s == "customer.submit" || s == "workflow.global.review.run" }))
	resolver.mu.Unlock()
	for _, e := range []agentsdk.ConversationBusinessEvidence{objectEvidence, workflowEvidence} {
		if err := host.RevalidateBusiness(t.Context(), e, a); err == nil {
			t.Fatal("revoked operation metadata remained available")
		}
	}
	for _, query := range []agentsdk.ConversationBusinessCatalogQuery{{Kind: "workflows", ActionKey: "customer.submit"}, {WorkflowKey: "global.review"}, {Kind: "guess"}, {Kind: "workflows", WorkflowKey: "global.review", After: "customer.review"}} {
		if _, err := host.BusinessCatalog(t.Context(), query, a); err == nil {
			t.Fatal("invalid selector accepted", query)
		}
	}
}
