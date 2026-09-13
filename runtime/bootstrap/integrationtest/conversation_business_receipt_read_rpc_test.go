package integrationtest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
)

func independentBusinessReadRoles(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		for _, value := range role["permissions"].([]any) {
			p := value.(map[string]any)
			if p["permission_key"] == "customer.read" {
				p["data_scope"] = "all"
			}
		}
		for _, key := range []string{"business_result_reader", "business_result_field_denied", "business_result_data_denied"} {
			raw, _ := json.Marshal(role)
			var clone map[string]any
			_ = json.Unmarshal(raw, &clone)
			clone["key"], clone["name"] = key, key
			permissions := []any{}
			for _, value := range clone["permissions"].([]any) {
				p := value.(map[string]any)
				permission := p["permission_key"].(string)
				if strings.HasPrefix(permission, agent.ConversationToolActionPrefix) || strings.HasPrefix(permission, "workflow.") && strings.HasSuffix(permission, ".run") || key == "business_result_data_denied" && permission == "customer.read" {
					continue
				}
				permissions = append(permissions, p)
			}
			clone["permissions"] = permissions
			if key == "business_result_field_denied" {
				for _, value := range clone["field_permissions"].([]any) {
					field := value.(map[string]any)
					if field["object_key"] == "customer" && field["field_key"] == "balance" {
						field["read"] = false
					}
				}
			}
			m["roles"] = append(m["roles"].([]any), clone)
		}
		return
	}
}

func TestBusinessIndependentReceiptReadThroughRealOwnerRPC(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, independentBusinessReadRoles)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	c, server := openAnalysisRPC(t, f, a)
	defer func() { server.Close() }()
	evidence := []agent.ConversationBusinessEvidence{}
	appendEvidence := func(operation string, input, output any, seal bool) {
		t.Helper()
		raw, _ := json.Marshal(input)
		data, _ := json.Marshal(output)
		e := agent.ConversationBusinessEvidence{Version: 1, Operation: operation, Source: c.BusinessSourceIdentity(), ScopeSHA256: rpcDigest([]string{c.BusinessSourceIdentity(), a.RuntimeID, a.WorkspaceID, a.UserID}), Input: raw, Data: data}
		if seal {
			var err error
			e.HostProof, err = c.SealBusinessEvidence(t.Context(), e, a)
			if err != nil || e.HostProof == "" {
				t.Fatal("source proof absent", operation, err)
			}
		}
		evidence = append(evidence, e)
	}
	cq := agent.ConversationBusinessCatalogQuery{ObjectKey: "customer"}
	catalog, err := c.BusinessCatalog(t.Context(), cq, a)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence("business_catalog", cq, catalog, false)
	q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "balance"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}
	page, err := c.QueryBusinessRecords(t.Context(), q, a)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	appendEvidence("query_records", q, page, true)
	q.Cursor = page.NextCursor
	second, err := c.QueryBusinessRecords(t.Context(), q, a)
	if err != nil || len(second.Items) != 1 {
		t.Fatal(second, err)
	}
	appendEvidence("query_records", q, second, true)
	get := agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: page.Items[0].ID, Fields: q.Fields}
	row, err := c.GetBusinessRecord(t.Context(), get, a)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence("get_record", get, row, true)
	relations, err := c.BusinessCatalog(t.Context(), agent.ConversationBusinessCatalogQuery{Kind: "relations", ObjectKey: "customer"}, a)
	if err != nil {
		t.Fatal(err)
	}
	rq := agent.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: row.ID, Fields: []string{"name", "amount"}, PageSize: 1}
	for _, r := range relations.Relations {
		if r.ObjectKey == "order" {
			rq.RelationKey = r.Key
			break
		}
	}
	related, err := c.QueryRelatedBusinessRecords(t.Context(), rq, a)
	if err != nil || len(related.Items) != 1 {
		t.Fatal(related, err)
	}
	appendEvidence("query_related_records", rq, related, true)
	workflows, err := c.BusinessCatalog(t.Context(), agent.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "agent_review"}, a)
	if err != nil || len(workflows.Workflows) != 1 {
		t.Fatal(workflows, err)
	}
	start := agent.ConversationWorkflowStart{WorkflowKey: "agent_review", Version: workflows.Workflows[0].ExecutionVersion, Data: json.RawMessage(`{"reason":"independent process receipt reading"}`)}
	raw, _ := json.Marshal(start)
	started, err := c.StartBusinessWorkflow(t.Context(), agent.ConversationWorkflowStartRequest{Authority: a, Start: start, Arguments: string(raw), ConversationID: "receipt-conversation", RunID: "receipt-run", CallID: "start", IdempotencyKey: "receipt-process-once", Confirmation: rpcConfirmation(a, "workflow_start", string(raw))})
	if err != nil || started.Status != "accepted" {
		t.Fatal(started, err)
	}
	wq := agent.ConversationWorkflowGet{WorkflowKey: start.WorkflowKey, ProcessID: started.ProcessID}
	state, err := c.GetBusinessWorkflow(t.Context(), wq, a)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence("workflow_get", wq, state, true)

	b.assign("business_result_reader")
	b.session()
	assertRead := func() {
		t.Helper()
		for _, e := range evidence {
			if err := c.AuthorizeBusinessResultRead(t.Context(), e, a); err != nil {
				t.Fatal("current source read denied without execution", e.Operation, err)
			}
			if e.Operation == "workflow_get" {
				if err := c.RevalidateBusinessWorkflow(t.Context(), e, a); err == nil {
					t.Fatal("ordinary workflow revalidation escaped tool grant")
				}
			} else if err := c.RevalidateBusiness(t.Context(), e, a); err == nil {
				t.Fatal("ordinary revalidation escaped tool grant", e.Operation)
			}
		}
	}
	assertRead()
	changed := evidence[3]
	changed.Data = json.RawMessage(`{"id":"forged","data":{"balance":999999}}`)
	if err := c.AuthorizeBusinessResultRead(t.Context(), changed, a); err == nil {
		t.Fatal("altered receipt accepted")
	}
	b.assign("business_result_field_denied")
	b.session()
	for _, e := range evidence[:4] {
		if err := c.AuthorizeBusinessResultRead(t.Context(), e, a); err == nil {
			t.Fatal("revoked field remained readable", e.Operation)
		}
	}
	b.assign("business_result_data_denied")
	b.session()
	for _, e := range evidence[:5] {
		if err := c.AuthorizeBusinessResultRead(t.Context(), e, a); err == nil {
			t.Fatal("revoked source records remained readable", e.Operation)
		}
	}
	b.assign("business_result_reader")
	b.session()
	assertRead()
	foreign := a
	foreign.WorkspaceID = "foreign"
	if err := c.AuthorizeBusinessResultRead(t.Context(), evidence[3], foreign); err == nil {
		t.Fatal("cross-workspace receipt accepted")
	}
	server.Close()
	f.close()
	f.open()
	b.cookies = map[string]*http.Cookie{}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	b.session()
	c, server = openAnalysisRPC(t, f, a)
	assertRead()
}
