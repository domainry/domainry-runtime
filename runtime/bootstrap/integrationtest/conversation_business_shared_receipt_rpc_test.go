package integrationtest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
)

func sharedBusinessReceiptRoles(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		for _, value := range role["permissions"].([]any) {
			permission := value.(map[string]any)
			if permission["permission_key"] == "order.read" {
				permission["data_scope"] = "all"
			}
		}
		for _, key := range []string{"action.receipt.customer.register.read", "workflow.receipt.agent_review.read"} {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": "all"})
		}
		for _, key := range []string{"shared_business_reader", "shared_business_field_denied", "shared_business_data_denied", "shared_business_receipt_owner"} {
			raw, _ := json.Marshal(role)
			var clone map[string]any
			_ = json.Unmarshal(raw, &clone)
			clone["key"], clone["name"] = key, key
			permissions := []any{}
			for _, value := range clone["permissions"].([]any) {
				p := value.(map[string]any)
				permission := p["permission_key"].(string)
				if strings.HasPrefix(permission, agent.ConversationToolActionPrefix) || strings.HasPrefix(permission, "workflow.") && strings.HasSuffix(permission, ".run") || strings.HasPrefix(permission, "customer.") && permission != "customer.read" || key == "shared_business_data_denied" && permission == "customer.read" {
					continue
				}
				if key == "shared_business_receipt_owner" && strings.Contains(permission, ".receipt.") {
					p["data_scope"] = "owner"
				}
				permissions = append(permissions, p)
			}
			clone["permissions"] = permissions
			if key == "shared_business_field_denied" {
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

func TestCrossUserBusinessOriginalReceiptsThroughRealOwnerRPCAndRestart(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, independentBusinessReadRoles, sharedResultUserRoles, sharedBusinessReceiptRoles)
	admin := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	admin.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	admin.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	admin.assign("headquarters_admin")
	admin.session()
	professional := createSharedResultProducer(t, f, admin)
	reader := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	producer := reader
	producer.UserID, producer.RoleKey = "professional_source", "headquarters_admin"
	c, server := openAnalysisRPC(t, f, reader)
	defer func() { server.Close() }()
	evidence := []agent.ConversationBusinessEvidence{}
	appendEvidence := func(operation string, input, output any, seal bool) {
		t.Helper()
		raw, _ := json.Marshal(input)
		data, _ := json.Marshal(output)
		e := agent.ConversationBusinessEvidence{Version: 1, Operation: operation, Source: c.BusinessSourceIdentity(), ScopeSHA256: rpcDigest([]string{c.BusinessSourceIdentity(), producer.RuntimeID, producer.WorkspaceID, producer.UserID}), Input: raw, Data: data}
		if seal {
			var err error
			e.HostProof, err = c.SealBusinessEvidence(t.Context(), e, producer)
			if err != nil || e.HostProof == "" {
				t.Fatal("source proof absent", operation, err)
			}
		}
		evidence = append(evidence, e)
	}
	cq := agent.ConversationBusinessCatalogQuery{ObjectKey: "customer"}
	catalog, err := c.BusinessCatalog(t.Context(), cq, producer)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence("business_catalog", cq, catalog, false)
	q := agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "balance"}, PageSize: 1, Sort: []agent.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}
	first, err := c.QueryBusinessRecords(t.Context(), q, producer)
	if err != nil || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	appendEvidence("query_records", q, first, true)
	q.Cursor = first.NextCursor
	next, err := c.QueryBusinessRecords(t.Context(), q, producer)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence("query_records", q, next, true)
	get := agent.ConversationBusinessGet{ObjectKey: "customer", RecordID: first.Items[0].ID, Fields: q.Fields}
	row, err := c.GetBusinessRecord(t.Context(), get, producer)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence("get_record", get, row, true)
	relations, err := c.BusinessCatalog(t.Context(), agent.ConversationBusinessCatalogQuery{Kind: "relations", ObjectKey: "customer"}, producer)
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
	related, err := c.QueryRelatedBusinessRecords(t.Context(), rq, producer)
	if err != nil || len(related.Items) != 1 {
		t.Fatal(related, err)
	}
	appendEvidence("query_related_records", rq, related, true)
	ac, err := c.BusinessCatalog(t.Context(), agent.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "customer", ActionKey: "customer.register"}, producer)
	if err != nil || len(ac.Actions) != 1 {
		t.Fatal(ac, err)
	}
	action := agent.ConversationBusinessAction{ActionKey: "customer.register", ObjectKey: "customer", Version: ac.Actions[0].ExecutionVersion, Data: json.RawMessage(`{"name":"Cross-user original action receipt","owner":"professional_source"}`)}
	rawAction, _ := json.Marshal(action)
	created, err := c.InvokeBusinessAction(t.Context(), agent.ConversationBusinessActionRequest{Authority: producer, Action: action, Arguments: string(rawAction), ConversationID: "shared-business-conversation", RunID: "original-producer-run", CallID: "create", IdempotencyKey: "shared-business-create-once", Confirmation: rpcConfirmation(producer, "invoke_action", string(rawAction))})
	if err != nil || created.Status != "completed" || len(created.CreatedRecords) != 1 {
		t.Fatal(created, err)
	}
	appendEvidence("invoke_action", action, created, false)
	wc, err := c.BusinessCatalog(t.Context(), agent.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "agent_review"}, producer)
	if err != nil || len(wc.Workflows) != 1 {
		t.Fatal(wc, err)
	}
	start := agent.ConversationWorkflowStart{WorkflowKey: "agent_review", Version: wc.Workflows[0].ExecutionVersion, Data: json.RawMessage(`{"reason":"Cross-user original start receipt"}`)}
	rawStart, _ := json.Marshal(start)
	started, err := c.StartBusinessWorkflow(t.Context(), agent.ConversationWorkflowStartRequest{Authority: producer, Start: start, Arguments: string(rawStart), ConversationID: "shared-business-conversation", RunID: "original-producer-run", CallID: "start", IdempotencyKey: "shared-business-start-once", Confirmation: rpcConfirmation(producer, "workflow_start", string(rawStart))})
	if err != nil || started.Status != "accepted" {
		t.Fatal(started, err)
	}
	appendEvidence("workflow_start", start, started, false)
	wq := agent.ConversationWorkflowGet{WorkflowKey: start.WorkflowKey, ProcessID: started.ProcessID}
	state, err := c.GetBusinessWorkflow(t.Context(), wq, producer)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence("workflow_get", wq, state, true)
	admin.assign("shared_business_reader")
	admin.session()
	assertRead := func() {
		t.Helper()
		for _, e := range evidence {
			before := string(e.Data)
			if err := c.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil || string(e.Data) != before {
				t.Fatal("actual reader denied original producer receipt", e.Operation, err)
			}
			if err := c.AuthorizeBusinessResultRead(t.Context(), e, reader); err == nil {
				t.Fatal("ordinary read acquired original actor receipt", e.Operation)
			}
		}
		if _, err := c.QueryBusinessRecords(t.Context(), q, reader); err == nil {
			t.Fatal("original cursor granted producing execution")
		}
	}
	assertRead()
	admin.assign("shared_business_receipt_owner")
	admin.session()
	for _, e := range evidence[5:7] {
		if err := c.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
			t.Fatal("self-only actual reader receipt scope ignored original actor", e.Operation)
		}
	}
	admin.assign("shared_business_field_denied")
	admin.session()
	if err := c.AuthorizeSharedBusinessResultRead(t.Context(), evidence[3], reader, producer); err == nil {
		t.Fatal("current reader field withdrawal bypassed")
	}
	admin.assign("shared_business_data_denied")
	admin.session()
	for _, e := range evidence[:6] {
		if err := c.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err == nil {
			t.Fatal("current reader record withdrawal bypassed", e.Operation)
		}
	}
	// This fixture's process has no linked customer record. Current process
	// participation and its own receipt permission still allow these two IDs.
	for _, e := range evidence[6:] {
		if err := c.AuthorizeSharedBusinessResultRead(t.Context(), e, reader, producer); err != nil {
			t.Fatal("unlinked process incorrectly depended on customer grant", e.Operation, err)
		}
	}
	admin.assign("shared_business_reader")
	admin.session()
	assertRead()
	assignSharedResultProducer(t, f, admin, "shared_business_data_denied")
	professional.session()
	if err := c.AuthorizeSharedBusinessResultRead(t.Context(), evidence[3], reader, producer); err == nil {
		t.Fatal("removed original role retained signed receipt access")
	}
	assignSharedResultProducer(t, f, admin, "headquarters_admin")
	professional.session()
	assertRead()
	server.Close()
	f.close()
	f.open()
	c, server = openAnalysisRPC(t, f, reader)
	assertRead()
}
