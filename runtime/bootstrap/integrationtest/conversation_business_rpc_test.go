package integrationtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
)

func businessRPCManifest(m map[string]any) {
	businessRelationsManifest(m)
	businessActionWebManifest(m)
	businessWorkflowWebManifest(m)
	// The existing independent scenario builders each grant interaction access.
	// Compose one canonical role permission per key in this combined fixture.
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		seen := map[string]bool{}
		permissions := []any{}
		for _, p := range role["permissions"].([]any) {
			key := p.(map[string]any)["permission_key"].(string)
			if !seen[key] {
				permissions = append(permissions, p)
				seen[key] = true
			}
		}
		role["permissions"] = permissions
	}
}
func rpcDigest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func rpcConfirmation(a sdk.ConversationAuthority, key, arguments string) *sdk.ConversationConfirmation {
	return &sdk.ConversationConfirmation{ID: "rpc-confirmation-" + key, UserID: a.UserID, ActionKey: sdk.ConversationToolActionPrefix + key, ToolVersion: "1", ArgumentsHash: rpcDigest(arguments), ApprovedAt: time.Now().UTC()}
}

func TestConversationBusinessRPCRealOwnersAndRestart(t *testing.T) {
	f := newBusinessWebFixture(t, businessRPCManifest)
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	a := sdk.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	var calls atomic.Int32
	var drop atomic.Bool
	openService := func() (*businessrpc.Client, *httptest.Server) {
		t.Helper()
		source, err := bootstrap.ConversationBusinessSource(f.runtime)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := bootstrap.ConversationBusinessHandler(f.runtime, "isolated-business-service-secret")
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == businessrpc.CallPath {
				calls.Add(1)
				if drop.Swap(false) {
					out := httptest.NewRecorder()
					handler.ServeHTTP(out, r)
					if out.Code != 200 {
						t.Errorf("drop must follow actual successful business dispatch: %d %s", out.Code, out.Body.String())
					}
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				}
			}
			handler.ServeHTTP(w, r)
		}))
		c, err := businessrpc.Open(t.Context(), businessrpc.ClientOptions{BaseURL: server.URL, Token: "isolated-business-service-secret", Scope: businessrpc.Scope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityIssuer: f.identity.Descriptor().Issuer}, ExpectedSourceIdentity: source.BusinessSourceIdentity(), ExpectedContractSHA256: businessrpc.ContractSHA256()})
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		return c, server
	}
	c, server := openService()
	defer func() { server.Close() }()
	ctx := t.Context()
	catalog, err := c.BusinessCatalog(ctx, sdk.ConversationBusinessCatalogQuery{ObjectKey: "customer"}, a)
	if err != nil || len(catalog.Items) != 1 {
		t.Fatal(catalog, err)
	}
	balance := false
	for _, field := range catalog.Items[0].Fields {
		balance = balance || field.Key == "balance"
	}
	if !balance {
		t.Fatal("expected authorized balance field")
	}
	q := sdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name", "balance"}, PageSize: 1, Sort: []sdk.ConversationBusinessSort{{Field: "name", Direction: "asc"}}}
	first, err := c.QueryBusinessRecords(ctx, q, a)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" || string(first.Items[0].Data["name"]) != `"Acme"` {
		t.Fatal(first, err)
	}
	q.Cursor = first.NextCursor
	second, err := c.QueryBusinessRecords(ctx, q, a)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatal("cursor page", second, err)
	}
	get := sdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: first.Items[0].ID, Fields: []string{"name", "balance"}}
	record, err := c.GetBusinessRecord(ctx, get, a)
	if err != nil || len(record.Data) != 2 {
		t.Fatal(record, err)
	}
	input, _ := json.Marshal(get)
	data, _ := json.Marshal(record)
	evidence := sdk.ConversationBusinessEvidence{Version: 1, Source: c.BusinessSourceIdentity(), ScopeSHA256: rpcDigest([]string{c.BusinessSourceIdentity(), a.RuntimeID, a.WorkspaceID, a.UserID}), Operation: "get_record", Input: input, Data: data}
	evidence.HostProof, err = c.SealBusinessEvidence(ctx, evidence, a)
	if err != nil || evidence.HostProof == "" {
		t.Fatal("actual source proof", err)
	}
	if err = c.RevalidateBusiness(ctx, evidence, a); err != nil {
		t.Fatal(err)
	}
	relations, err := c.BusinessCatalog(ctx, sdk.ConversationBusinessCatalogQuery{Kind: "relations", ObjectKey: "customer"}, a)
	if err != nil || len(relations.Relations) == 0 {
		t.Fatal(relations, err)
	}
	relation := ""
	for _, r := range relations.Relations {
		if r.ObjectKey == "order" {
			relation = r.Key
			break
		}
	}
	if relation == "" {
		t.Fatal("order relation missing")
	}
	related, err := c.QueryRelatedBusinessRecords(ctx, sdk.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: record.ID, RelationKey: relation, Fields: []string{"name", "amount"}, PageSize: 1}, a)
	if err != nil || len(related.Items) != 1 {
		t.Fatal(related, err)
	}
	b.assign("business_field_restricted")
	restricted, err := c.BusinessCatalog(ctx, sdk.ConversationBusinessCatalogQuery{ObjectKey: "customer"}, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range restricted.Items {
		for _, field := range object.Fields {
			if field.Key == "balance" {
				t.Fatal("revoked field exposed")
			}
		}
	}
	if _, err = c.GetBusinessRecord(ctx, get, a); err == nil {
		t.Fatal("revoked field read")
	}
	if err = c.RevalidateBusiness(ctx, evidence, a); err == nil {
		t.Fatal("old proof granted revoked field")
	}
	b.assign("business_restricted")
	if _, err = c.GetBusinessRecord(ctx, sdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: record.ID, Fields: []string{"name"}}, a); err == nil {
		t.Fatal("object revoke ignored")
	}
	b.assign("headquarters_admin")
	if err = c.RevalidateBusiness(ctx, evidence, a); err != nil {
		t.Fatal("restored proof", err)
	}
	// Exact write metadata originates in the trusted test host. The product's
	// actual user confirmation is exercised by the later independent Web E2E.
	actions, err := c.BusinessCatalog(ctx, sdk.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "customer", ActionKey: "customer.register"}, a)
	if err != nil || len(actions.Actions) != 1 {
		t.Fatal(actions, err)
	}
	action := sdk.ConversationBusinessAction{ObjectKey: "customer", ActionKey: actions.Actions[0].Key, Version: actions.Actions[0].ExecutionVersion, Data: json.RawMessage(`{"name":"RPC Created","owner":"admin"}`)}
	allowed, err := c.AuthorizeBusinessAction(ctx, action, a)
	if err != nil || !allowed.Granted {
		t.Fatal(allowed, err)
	}
	raw, _ := json.Marshal(action)
	request := sdk.ConversationBusinessActionRequest{Authority: a, Action: action, Arguments: string(raw), ConversationID: "rpc-conversation", RunID: "rpc-action-run", CallID: "rpc-create", IdempotencyKey: "rpc-create-once", Confirmation: rpcConfirmation(a, "invoke_action", string(raw))}
	before := calls.Load()
	drop.Store(true)
	uncertain, err := c.InvokeBusinessAction(ctx, request)
	if err != nil || uncertain.Status != "uncertain" || calls.Load() != before+1 {
		t.Fatal("lost write response retried", uncertain, err)
	}
	receipt, err := c.ReconcileBusinessAction(ctx, request)
	if err != nil || receipt.Status != "completed" || len(receipt.CreatedRecords) != 1 {
		t.Fatal(receipt, err)
	}
	createdQuery := sdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, Filters: []sdk.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(`"RPC Created"`)}}, PageSize: 25}
	created, err := c.QueryBusinessRecords(ctx, createdQuery, a)
	if err != nil || len(created.Items) != 1 {
		t.Fatal("actual unique effect", created, err)
	}
	actionData, _ := json.Marshal(receipt)
	actionEvidence := sdk.ConversationBusinessEvidence{Version: 1, Source: c.BusinessSourceIdentity(), ScopeSHA256: evidence.ScopeSHA256, Operation: "invoke_action", Input: raw, Data: actionData}
	if err = c.RevalidateBusinessAction(ctx, actionEvidence, a); err != nil {
		t.Fatal(err)
	}
	workflows, err := c.BusinessCatalog(ctx, sdk.ConversationBusinessCatalogQuery{Kind: "workflows", WorkflowKey: "agent_review"}, a)
	if err != nil || len(workflows.Workflows) != 1 {
		t.Fatal(workflows, err)
	}
	start := sdk.ConversationWorkflowStart{WorkflowKey: "agent_review", Version: workflows.Workflows[0].ExecutionVersion, Data: json.RawMessage(`{"reason":"RPC source acceptance"}`)}
	granted, err := c.AuthorizeWorkflowStart(ctx, start, a)
	if err != nil || !granted.Granted {
		t.Fatal(granted, err)
	}
	raw, _ = json.Marshal(start)
	startRequest := sdk.ConversationWorkflowStartRequest{Authority: a, Start: start, Arguments: string(raw), ConversationID: "rpc-conversation", RunID: "rpc-workflow-run", CallID: "rpc-start", IdempotencyKey: "rpc-workflow-once", Confirmation: rpcConfirmation(a, "workflow_start", string(raw))}
	accepted, err := c.StartBusinessWorkflow(ctx, startRequest)
	if err != nil || accepted.Status != "accepted" || accepted.ProcessID == "" {
		t.Fatal(accepted, err)
	}
	state, err := c.GetBusinessWorkflow(ctx, sdk.ConversationWorkflowGet{WorkflowKey: start.WorkflowKey, ProcessID: accepted.ProcessID}, a)
	if err != nil || state.Terminal || state.ProcessID != accepted.ProcessID {
		t.Fatal("accepted not complete", state, err)
	}
	workflowData, _ := json.Marshal(accepted)
	workflowEvidence := sdk.ConversationBusinessEvidence{Version: 1, Source: c.BusinessSourceIdentity(), ScopeSHA256: evidence.ScopeSHA256, Operation: "workflow_start", Input: raw, Data: workflowData}
	if err = c.RevalidateBusinessWorkflow(ctx, workflowEvidence, a); err != nil {
		t.Fatal(err)
	}
	// Reopen all real bindings and databases, then attach a fresh service listener.
	server.Close()
	f.close()
	f.open()
	c, server = openService()
	if err = c.RevalidateBusiness(ctx, evidence, a); err != nil {
		t.Fatal("restart source proof", err)
	}
	replayed, err := c.ReconcileBusinessAction(ctx, request)
	if err != nil || replayed.InvocationID != receipt.InvocationID {
		t.Fatal("restart action receipt", replayed, err)
	}
	created, err = c.QueryBusinessRecords(ctx, createdQuery, a)
	if err != nil || len(created.Items) != 1 {
		t.Fatal("restart duplicated action", created, err)
	}
	workflowReplay, err := c.ReconcileBusinessWorkflow(ctx, startRequest)
	if err != nil || workflowReplay.ProcessID != accepted.ProcessID {
		t.Fatal("restart duplicated workflow", workflowReplay, err)
	}
	if err = c.RevalidateBusinessAction(ctx, actionEvidence, a); err != nil {
		t.Fatal(err)
	}
	if err = c.RevalidateBusinessWorkflow(ctx, workflowEvidence, a); err != nil {
		t.Fatal(err)
	}
	absent := a
	absent.UserID = "absent-user"
	if _, err = c.GetBusinessRecord(ctx, get, absent); err == nil {
		t.Fatal("service token impersonated unknown identity")
	}
	bad := a
	bad.WorkspaceID = "other-workspace"
	prior := calls.Load()
	if _, err = c.GetBusinessRecord(ctx, get, bad); err == nil || calls.Load() != prior {
		t.Fatal("foreign workspace dispatched")
	}
	wrong := request
	wrong.Action.Data = json.RawMessage(`{"name":"Unexpected","owner":"admin"}`)
	wrong.IdempotencyKey = "unconfirmed-other"
	denied, err := c.InvokeBusinessAction(ctx, wrong)
	if err != nil || denied.Status != "uncertain" {
		t.Fatal("missing trustworthy write receipt", denied, err)
	}
	unexpected, err := c.QueryBusinessRecords(ctx, sdk.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, Filters: []sdk.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(`"Unexpected"`)}}}, a)
	if err != nil || len(unexpected.Items) != 0 {
		t.Fatal("changed confirmation produced effect", unexpected, err)
	}
	if strings.Contains(evidence.HostProof, "isolated-business-service-secret") {
		t.Fatal("credential in proof")
	}
	t.Logf("actual Runtime/Identity/Record/Action/Workflow + SQLite, %d service HTTP calls; one lost-response Action effect, one nonterminal workflow; source/receipt checks and original-key reconciliation survive complete restart", calls.Load())
}
