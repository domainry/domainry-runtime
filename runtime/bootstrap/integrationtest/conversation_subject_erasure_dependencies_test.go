package integrationtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	lifecycle "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

func multipleOwnerLifecycleDataExchangeFactory() dataexchange.Factory {
	return dataexchangemodule.NewFactory(dataexchangemodule.Options{})
}

func multipleOwnerLifecycleRoles(manifest map[string]any) {
	for _, value := range manifest["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "a_results_reader" && role["key"] != "headquarters_admin" {
			continue
		}
		for _, key := range []string{lifecycle.ActionLifecycleSubjectRequestsCreate, lifecycle.ActionLifecycleSubjectRequestsRead, lifecycle.ActionLifecycleSubjectRequestsVerify, lifecycle.ActionLifecycleSubjectRequestsPreview, lifecycle.ActionLifecycleSubjectRequestsApprove, lifecycle.ActionLifecycleSubjectRequestsExecute, lifecycle.ActionLifecycleSubjectExportsDownload} {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": "all"})
		}
	}
}

func TestSubjectErasureRechecksMultipleOwnerProfessionalDependenciesThroughRealRuntime(t *testing.T) {
	verifyMultipleOwnerDependencyRuntime(t, multipleOwnerIdentityErasure)
}

func verifyMultiOwnerSubjectErasure(t *testing.T, f *businessWebFixture, a, b, c *businessBrowser, source agent.Conversation, root, final agent.ConversationDelegationDetail, gate *inactiveIdentityModelGate, read func(...int), profile func(*businessBrowser, string, []string, []string) agent.ConversationAgent) {
	t.Helper()
	call := func(browser *businessBrowser, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, businessWebOrigin+path, strings.NewReader(string(raw)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+browser.cookies["domainry_agent_access"].Value)
		request.Header.Set("X-Workspace-ID", f.cfg.IdentityWorkspaceID)
		request.Header.Set("Idempotency-Key", fmt.Sprintf("multi-owner-erasure-%d", time.Now().UnixNano()))
		request.Header.Set("X-Operation-Reason", "C05 fixture subject erasure")
		request.Header.Set("X-Operation-Confirmation", "confirmed")
		response := httptest.NewRecorder()
		f.runtime.Routes().ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("actual Lifecycle %s %s status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	decode := func(response *httptest.ResponseRecorder) lifecyclemodel.SubjectRequest {
		t.Helper()
		var out lifecyclemodel.SubjectRequest
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil || out.ID == "" {
			t.Fatal("actual Lifecycle request response", response.Body.String(), err)
		}
		return out
	}
	if len(root.Requirements.Sources) != 1 {
		t.Fatal("original professional report source root", root.Requirements.Sources)
	}
	originalReportPath := "/agent/conversations/" + source.ID + "/runs/" + root.Requirements.Sources[0].RunID
	originalReport := append([]byte(nil), a.call("GET", originalReportPath, nil, 200).Body.Bytes()...)
	assertOriginalReport := func() {
		t.Helper()
		if string(a.call("GET", originalReportPath, nil, 200).Body.Bytes()) != string(originalReport) {
			t.Fatal("erasing the other owner changed the original professional report")
		}
	}
	// Governed exports inspect durable owner records. Ordinary collaboration
	// responses must hide those contents after source access is withdrawn.
	acceptedRecords := func() map[string]json.RawMessage {
		t.Helper()
		created := decode(call(a, "POST", "/lifecycle/subjects", map[string]any{"kind": "export", "subject_type": "user", "subject_id": "dependency_reader", "reason": "Verify the original accepted downstream owner records"}, http.StatusAccepted))
		path := "/lifecycle/subjects/" + created.ID
		decode(call(a, "POST", path+"/verify", map[string]any{"second_factor_ref": "C05-fixture-verified-subject"}, 200))
		decode(call(a, "POST", path+"/preview", map[string]any{}, 200))
		decode(call(c, "POST", path+"/approve", map[string]any{}, 200))
		completed := decode(call(a, "POST", path+"/execute", map[string]any{}, 200))
		if completed.Status != lifecyclemodel.SubjectRequestSucceeded {
			t.Fatal("actual downstream owner export did not complete", completed)
		}
		var exported struct {
			Data struct {
				Agent struct {
					Records map[string][]json.RawMessage `json:"records"`
				} `json:"agent"`
			} `json:"data"`
		}
		if err := json.Unmarshal(call(a, "GET", path+"/download", nil, 200).Body.Bytes(), &exported); err != nil {
			t.Fatal(err)
		}
		out := map[string]json.RawMessage{}
		for _, table := range []string{"_agent_delegations", "_agent_conversation_tasks", "_agent_conversation_runs"} {
			for _, raw := range exported.Data.Agent.Records[table] {
				var record struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					t.Fatal(err)
				}
				if record.ID == final.ID || record.ID == final.Task.ID || record.ID == final.Task.ExecutionRunID {
					out[table] = raw
				}
			}
			if len(out[table]) == 0 {
				t.Fatal("governed export lacks accepted original owner record", table)
			}
		}
		return out
	}
	originalRecords := acceptedRecords()
	worker := profile(b, "subject-erasure-worker", []string{"admin"}, []string{"delegation_get"})
	var pending agent.ConversationDelegationDetail
	if err := json.Unmarshal(a.call("POST", "/agent/delegations", agent.ConversationDelegationCreate{ClientID: "subject-erasure-pending", ConversationID: source.ID, AgentID: worker.ID, Purpose: "Stop the admitted execution when its receiving subject is erased", Brief: agent.ConversationTaskBrief{Version: 1, Goal: "Inspect delegation", Deliverable: "Original delegation detail", CompletionConditions: []string{"Read only under a current execution identity"}}, Input: inactiveIdentityModelMarker}, 200).Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	gate.call = agent.ConversationToolCall{ID: "erased-identity-effect", Name: "delegation_get", Arguments: fmt.Sprintf(`{"id":%q}`, pending.ID)}
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("subject erasure fixture did not admit the actual held model request")
	}
	created := decode(call(a, "POST", "/lifecycle/subjects", map[string]any{"kind": "erase", "subject_type": "user", "subject_id": "professional_source", "reason": "Erase the actual professional producer and receiving Agent owner"}, http.StatusAccepted))
	path := "/lifecycle/subjects/" + created.ID
	decode(call(a, "POST", path+"/verify", map[string]any{"second_factor_ref": "C05-fixture-verified-subject"}, 200))
	preview := decode(call(a, "POST", path+"/preview", map[string]any{}, 200))
	var owners map[string]json.RawMessage
	if err := json.Unmarshal(preview.ImpactPreview, &owners); err != nil || len(owners["agent"]) == 0 || len(owners["identity"]) == 0 {
		t.Fatal("Runtime did not bind both Agent and Identity subject owners", owners, err)
	}
	call(a, "POST", path+"/approve", map[string]any{}, 409)
	approved := decode(call(c, "POST", path+"/approve", map[string]any{}, 200))
	if approved.RequestedBy != "admin" || approved.ApprovedBy != "dependency_reader" || approved.Status != lifecyclemodel.SubjectRequestApproved {
		t.Fatal("actual independent subject approval", approved)
	}
	completed := decode(call(a, "POST", path+"/execute", map[string]any{}, 200))
	if completed.Status != lifecyclemodel.SubjectRequestSucceeded || completed.ExecutionAttempt != 1 || completed.LastError != "" {
		t.Fatal("actual owner-orchestrated erasure did not complete", completed)
	}
	gate.release()
	b.call("GET", "/agent/delegations/"+pending.ID, nil, 401)
	b.call("POST", "/auth/login", map[string]any{"login": "professional@example.com", "password": businessWebPassword}, 403)
	var stopped agent.ConversationDelegationDetail
	if err := json.Unmarshal(a.call("GET", "/agent/delegations/"+pending.ID, nil, 200).Body.Bytes(), &stopped); err != nil || !stopped.SubjectExited || stopped.Status != "cancelled" || !stopped.ContractOmitted || stopped.Task != nil || stopped.Delivery != nil {
		t.Fatal("actual subject erasure did not stop the counterpart delegation", stopped, err)
	}
	var survivingRoot agent.ConversationDelegationDetail
	if err := json.Unmarshal(a.call("GET", "/agent/delegations/"+root.ID, nil, 200).Body.Bytes(), &survivingRoot); err != nil || !survivingRoot.SubjectExited {
		t.Fatal("counterpart original subject-exit marker missing", survivingRoot, err)
	}
	// The erased upstream's immutable agreement is needed to validate the
	// admitted dependency graph, so neither wrapped page can be re-authorized.
	read(403)
	assertOriginalReport()
	assertFinal := func() {
		t.Helper()
		var current agent.ConversationDelegationDetail
		if err := json.Unmarshal(c.call("GET", "/agent/delegations/"+final.ID, nil, 200).Body.Bytes(), &current); err != nil || current.Status != "needs_update" || !current.ContractOmitted {
			t.Fatal("erasing an upstream subject failed to mark the dependent delivery unavailable and needing update", current, err)
		}
		if current.ContractOmitted && (current.Task != nil || current.Delivery != nil) {
			t.Fatal("unavailable downstream contract disclosed source-derived contents", current)
		}
	}
	assertFinal()
	var directory agent.ConversationAgentPage
	if err := json.Unmarshal(a.call("GET", "/agent/agents", nil, 200).Body.Bytes(), &directory); err != nil {
		t.Fatal(err)
	}
	for _, item := range directory.Items {
		if item.OwnerUserID == "professional_source" {
			t.Fatal("erased subject still publishes an Agent configuration", item.ID)
		}
	}
	f.close()
	f.open()
	a.session()
	c.session()
	read(403)
	assertOriginalReport()
	assertFinal()
	stored := decode(call(a, "GET", path, nil, 200))
	if stored.Status != lifecyclemodel.SubjectRequestSucceeded || stored.ExecutionAttempt != completed.ExecutionAttempt {
		t.Fatal("restart changed the original completed Lifecycle receipt", stored)
	}
	for table, raw := range acceptedRecords() {
		if table == "_agent_delegations" {
			var original, current agent.ConversationDelegation
			if err := json.Unmarshal(originalRecords[table], &original); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &current); err != nil {
				t.Fatal(err)
			}
			if current.Status != "needs_update" || len(current.PendingChanges) != 1 || current.PendingChanges[0].SourceDelegationID != root.ID || current.AgreementRevision != original.AgreementRevision || sharedProfessionalJSON(current.Delivery) != sharedProfessionalJSON(original.Delivery) || sharedProfessionalJSON(current.Verification) != sharedProfessionalJSON(original.Verification) {
				t.Fatal("dependency invalidation rewrote the original accepted delivery or lost its change provenance")
			}
			continue
		}
		if string(raw) != string(originalRecords[table]) {
			t.Fatal("subject erasure or restart changed the exact accepted downstream owner record", table)
		}
	}
	t.Log("Actual Runtime Lifecycle HTTP request, verification, owner previews, independent approval, Identity/Agent erasure, admitted counterpart cancellation, unavailable producer pages, unaffected original report, accepted downstream preservation, erased Agent directory and restart verified")
}
