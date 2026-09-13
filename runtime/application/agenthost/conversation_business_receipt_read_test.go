package agenthost

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func withoutBusinessToolGrants(resolver *businessPrincipalResolver) {
	b := *resolver.principal.AccessBundle
	b.FunctionGrants = slices.DeleteFunc(slices.Clone(b.FunctionGrants), func(g identity.FunctionGrant) bool { return g.Resource == "agent.conversation_tools" })
	b.DataPolicies = slices.DeleteFunc(slices.Clone(b.DataPolicies), func(g identity.DataPolicy) bool { return g.Resource == "agent.conversation_tools" })
	resolver.principal.AccessBundle = &b
}

func businessReadEvidence(t *testing.T, h *ConversationBusinessHost, a agent.ConversationAuthority, operation string, input, output any) agent.ConversationBusinessEvidence {
	t.Helper()
	in, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	return agent.ConversationBusinessEvidence{Version: 1, Source: h.source, ScopeSHA256: h.cursorScope(a), Operation: operation, Input: in, Data: data}
}

func TestBusinessReceiptReadUsesCurrentRecordsWithoutAgentToolGrants(t *testing.T) {
	h, resolver, reads, a := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "amount"}, PageSize: 1}
	page, err := h.QueryBusinessRecords(t.Context(), q, a)
	if err != nil {
		t.Fatal(err)
	}
	eQuery := sealedBusinessEvidence(t, h, a, "query_records", q, page)
	get := agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: page.Items[0].ID, Fields: q.Fields}
	row, err := h.GetBusinessRecord(t.Context(), get, a)
	if err != nil {
		t.Fatal(err)
	}
	eGet := sealedBusinessEvidence(t, h, a, "get_record", get, row)
	cq := agent.ConversationBusinessCatalogQuery{ObjectKey: "customer"}
	catalog, err := h.BusinessCatalog(t.Context(), cq, a)
	if err != nil {
		t.Fatal(err)
	}
	eCatalog := businessReadEvidence(t, h, a, "business_catalog", cq, catalog)
	original := resolver.principal
	withoutBusinessToolGrants(resolver)
	for _, tool := range agent.BusinessConversationTools() {
		auth, err := h.AuthorizeConversationTool(t.Context(), agent.ConversationToolRequest{Authority: a, Definition: tool})
		if err != nil || auth.Granted {
			t.Fatal("execution grant survived", err)
		}
	}
	for _, e := range []agent.ConversationBusinessEvidence{eQuery, eGet, eCatalog} {
		if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, a); err != nil {
			t.Fatal("read required producing tool permission", e.Operation, err)
		}
		changed := e
		changed.Data = json.RawMessage(`{"private":"forged"}`)
		if err := agent.AuthorizeBusinessResultRead(t.Context(), h, changed, a); err == nil {
			t.Fatal("changed original accepted")
		}
		foreign := a
		foreign.UserID = "other"
		if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, foreign); err == nil {
			t.Fatal("foreign owner accepted")
		}
	}
	// No new value is substituted into an old proof when the effective policy
	// changed. Reinstating the original policy allows its signed old values.
	mutateBusinessEvidenceRow(t, reads, a, reads.object, get.RecordID, func(r *recordmodel.Record) { r.Data["name"] = "Updated" })
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, eGet, a); err == nil {
		t.Fatal("changed-policy read substituted new value")
	}
	resolver.principal = original
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, eGet, a); err != nil {
		t.Fatal("equivalent original policy lost signed snapshot", err)
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, get.RecordID, func(r *recordmodel.Record) { r.OwnerUserID = "colleague" })
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, eGet, a); err == nil {
		t.Fatal("row owner change retained access")
	}
	mutateBusinessEvidenceRow(t, reads, a, reads.object, get.RecordID, func(r *recordmodel.Record) { r.OwnerUserID = a.UserID })
	b := *resolver.principal.AccessBundle
	b.FieldPolicies = slices.Clone(b.FieldPolicies)
	for i := range b.FieldPolicies {
		if b.FieldPolicies[i].Field == "name" {
			b.FieldPolicies[i].Read = false
		}
	}
	resolver.principal.AccessBundle = &b
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, eGet, a); err == nil {
		t.Fatal("field revoke retained access")
	}
	for _, operation := range []string{"invoke_action", "workflow_start", "unknown"} {
		e := eGet
		e.Operation = operation
		var coded *agent.Error
		if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, a); !errors.As(err, &coded) || coded.Code != agent.BusinessResultReadUnsupportedCode {
			t.Fatal("unsupported write changed policy", err)
		}
	}
}

func TestBusinessReceiptReadRechecksRelationOriginsAndFields(t *testing.T) {
	h, resolver, _, a := newBusinessRelationFixture(t)
	q := agent.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "own-1", RelationKey: "reverse:order:customer", PageSize: 1, Fields: []string{"name"}}
	page, err := h.QueryRelatedBusinessRecords(t.Context(), q, a)
	if err != nil {
		t.Fatal(err)
	}
	e := businessReadEvidence(t, h, a, "query_related_records", q, page)
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, a); err != nil {
		t.Fatal(err)
	}
	for _, denied := range []string{"order.customer", "order.name"} {
		resolver.principal = relationTestPrincipal(a, denied)
		if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, a); err == nil {
			t.Fatal("relation revoke retained access", denied)
		}
	}
}

func TestBusinessReceiptReadRechecksProcessMembershipWithoutToolGrant(t *testing.T) {
	h, resolver, _, a := newConversationBusinessFixture(t)
	enableBusinessEvidence(t, h)
	port := &businessWorkflowPortProbe{process: workflowmodel.WorkflowProcessInstance{ID: "process", WorkflowKey: "review", WorkspaceID: a.WorkspaceID, InitiatorID: a.UserID, Status: "waiting", ObjectKey: "customer", RecordID: "own-1", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "review"}}}
	if err := WithConversationBusinessWorkflows(port)(h); err != nil {
		t.Fatal(err)
	}
	q := agent.ConversationWorkflowGet{WorkflowKey: "review", ProcessID: "process"}
	state, err := h.GetBusinessWorkflow(t.Context(), q, a)
	if err != nil {
		t.Fatal(err)
	}
	e := sealedBusinessEvidence(t, h, a, "workflow_get", q, state)
	withoutBusinessToolGrants(resolver)
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, a); err != nil {
		t.Fatal("process read required tool grant", err)
	}
	changed := e
	changed.HostProof += "0"
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, changed, a); err == nil {
		t.Fatal("invalid signed process accepted")
	}
	port.denied = true
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, a); err == nil {
		t.Fatal("process membership revoke retained access")
	}
	port.denied = false
	port.process.RecordID = "other-owner"
	if err := agent.AuthorizeBusinessResultRead(t.Context(), h, e, a); err == nil {
		t.Fatal("changed process record scope retained access")
	}
	if port.starts != 0 {
		t.Fatal("reading started a workflow")
	}
}
