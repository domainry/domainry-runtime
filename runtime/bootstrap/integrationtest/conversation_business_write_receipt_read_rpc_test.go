package integrationtest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
)

func independentBusinessWriteReceiptRoles(m map[string]any) {
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		for _, key := range []string{"write_receipt_reader", "write_receipt_denied", "write_receipt_record_denied"} {
			raw, _ := json.Marshal(role)
			var clone map[string]any
			_ = json.Unmarshal(raw, &clone)
			clone["key"], clone["name"] = key, key
			permissions := []any{}
			for _, value := range clone["permissions"].([]any) {
				p := value.(map[string]any)
				permission := p["permission_key"].(string)
				if strings.HasPrefix(permission, agent.ConversationToolActionPrefix) || strings.HasPrefix(permission, "workflow.") && strings.HasSuffix(permission, ".run") || strings.HasPrefix(permission, "customer.") && permission != "customer.read" && permission != "customer.export" || key == "write_receipt_record_denied" && permission == "customer.read" {
					continue
				}
				permissions = append(permissions, p)
			}
			if key != "write_receipt_denied" {
				for _, permission := range []string{"action.receipt.customer.register.read", "workflow.receipt.agent_review.read"} {
					permissions = append(permissions, map[string]any{"permission_key": permission, "data_scope": "owner"})
				}
			}
			clone["permissions"] = permissions
			m["roles"] = append(m["roles"].([]any), clone)
		}
		return
	}
}

func TestBusinessWriteReceiptReadThroughRealOwnerRPC(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest, independentBusinessWriteReceiptRoles)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	a := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	c, server := openAnalysisRPC(t, f, a)
	defer func() { server.Close() }()
	ctx := t.Context()
	catalog, err := c.BusinessCatalog(ctx, agent.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "customer", ActionKey: "customer.register"}, a)
	if err != nil || len(catalog.Actions) != 1 {
		t.Fatal(catalog, err)
	}
	action := agent.ConversationBusinessAction{ActionKey: "customer.register", ObjectKey: "customer", Version: catalog.Actions[0].ExecutionVersion, Data: json.RawMessage(`{"name":"Independent write receipt","owner":"admin"}`)}
	rawAction, _ := json.Marshal(action)
	request := agent.ConversationBusinessActionRequest{Authority: a, Action: action, Arguments: string(rawAction), ConversationID: "write-receipt-conversation", RunID: "write-receipt-run", CallID: "create", IdempotencyKey: "independent-create-once", Confirmation: rpcConfirmation(a, "invoke_action", string(rawAction))}
	created, err := c.InvokeBusinessAction(ctx, request)
	if err != nil || created.Status != "completed" || len(created.CreatedRecords) != 1 {
		t.Fatal(created, err)
	}
	wc, err := c.BusinessCatalog(ctx, agent.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "agent_review"}, a)
	if err != nil || len(wc.Workflows) != 1 {
		t.Fatal(wc, err)
	}
	start := agent.ConversationWorkflowStart{WorkflowKey: "agent_review", Version: wc.Workflows[0].ExecutionVersion, Data: json.RawMessage(`{"reason":"Independent start receipt"}`)}
	rawStart, _ := json.Marshal(start)
	startRequest := agent.ConversationWorkflowStartRequest{Authority: a, Start: start, Arguments: string(rawStart), ConversationID: request.ConversationID, RunID: request.RunID, CallID: "start", IdempotencyKey: "independent-start-once", Confirmation: rpcConfirmation(a, "workflow_start", string(rawStart))}
	started, err := c.StartBusinessWorkflow(ctx, startRequest)
	if err != nil || started.Status != "accepted" {
		t.Fatal(started, err)
	}
	evidence := []agent.ConversationBusinessEvidence{}
	for i, result := range []any{created, started} {
		data, _ := json.Marshal(result)
		evidence = append(evidence, agent.ConversationBusinessEvidence{Version: 1, Operation: []string{"invoke_action", "workflow_start"}[i], Source: c.BusinessSourceIdentity(), ScopeSHA256: rpcDigest([]string{c.BusinessSourceIdentity(), a.RuntimeID, a.WorkspaceID, a.UserID}), Input: []json.RawMessage{rawAction, rawStart}[i], Data: data})
	}
	for _, e := range evidence {
		if err := c.AuthorizeBusinessResultRead(ctx, e, a); err == nil {
			t.Fatal("execution role automatically acquired receipt permission", e.Operation)
		}
	}
	selectRole := func(role string) { t.Helper(); b.assign(role); b.session() }
	assertRead := func() {
		t.Helper()
		for _, e := range evidence {
			if err := c.AuthorizeBusinessResultRead(ctx, e, a); err != nil {
				t.Fatal("owned original receipt unreadable without execution", e.Operation, err)
			}
		}
		if err := c.RevalidateBusinessAction(ctx, evidence[0], a); err == nil {
			t.Fatal("ordinary action execution revalidation escaped its grant")
		}
		if err := c.RevalidateBusinessWorkflow(ctx, evidence[1], a); err == nil {
			t.Fatal("ordinary workflow revalidation escaped its grant")
		}
		if auth, err := c.AuthorizeBusinessAction(ctx, action, a); err == nil && auth.Granted {
			t.Fatal("receipt permission authorized an action")
		}
		if auth, err := c.AuthorizeWorkflowStart(ctx, start, a); err == nil && auth.Granted {
			t.Fatal("receipt permission authorized a workflow start")
		}
		// The write RPC intentionally reports unknown on transport/authorization
		// failure; nil Go error is not an execution grant or success receipt.
		if out, _ := c.InvokeBusinessAction(ctx, request); out.Status != "uncertain" {
			t.Fatal("denied action obtained a success receipt", out)
		}
		if out, _ := c.StartBusinessWorkflow(ctx, startRequest); out.Status != "uncertain" {
			t.Fatal("denied workflow obtained a start receipt", out)
		}
	}
	selectRole("write_receipt_reader")
	assertRead()
	for _, e := range evidence {
		for _, change := range []string{"input", "receipt", "version", "scope", "extra", "proof"} {
			bad := e
			switch change {
			case "input":
				bad.Input = json.RawMessage(strings.Replace(string(e.Input), "Independent", "Changed", 1))
			case "receipt":
				bad.Data = json.RawMessage(strings.Replace(string(e.Data), "independent-", "forged-", 1))
			case "version":
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(e.Input, &fields)
				field := "action_version"
				if e.Operation == "workflow_start" {
					field = "workflow_version"
				}
				fields[field] = json.RawMessage(`"stale"`)
				bad.Input, _ = json.Marshal(fields)
			case "scope":
				bad.ScopeSHA256 = "foreign"
			case "extra":
				bad.Data = json.RawMessage(strings.TrimSuffix(string(e.Data), "}") + `,"private":"extra"}`)
			case "proof":
				bad.HostProof = "invented"
			}
			if err := c.AuthorizeBusinessResultRead(ctx, bad, a); err == nil {
				t.Fatal("altered original receipt accepted", e.Operation, change)
			}
		}
	}
	selectRole("write_receipt_denied")
	for _, e := range evidence {
		if err := c.AuthorizeBusinessResultRead(ctx, e, a); err == nil {
			t.Fatal("revoked receipt grant ignored", e.Operation)
		}
	}
	selectRole("write_receipt_record_denied")
	if err := c.AuthorizeBusinessResultRead(ctx, evidence[0], a); err == nil {
		t.Fatal("revoked record read ignored")
	}
	selectRole("write_receipt_reader")
	assertRead()
	server.Close()
	f.close()
	f.open()
	b.cookies = map[string]*http.Cookie{}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": businessWebPassword}, 200)
	b.session()
	c, server = openAnalysisRPC(t, f, a)
	assertRead()
	selectRole("headquarters_admin")
	page, err := c.QueryBusinessRecords(ctx, agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, Filters: []agent.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(`"Independent write receipt"`)}}, PageSize: 25}, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != created.CreatedRecords[0].RecordID {
		t.Fatal("read path changed or duplicated original effect", page, err)
	}
	replayed, err := c.ReconcileBusinessWorkflow(ctx, startRequest)
	if err != nil || rpcDigest(replayed) != rpcDigest(started) {
		t.Fatal("original process receipt changed", replayed, err)
	}
	t.Log("Original action and workflow-start receipts remain exact after independent reading, grant revocation/restoration and restart; execution remains separately denied.")
}
