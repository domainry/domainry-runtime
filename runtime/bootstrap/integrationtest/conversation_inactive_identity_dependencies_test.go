package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
)

const inactiveIdentityModelMarker = "Hold this admitted task until its execution identity changes."

type inactiveIdentityModelGate struct {
	entered, released      chan struct{}
	enterOnce, releaseOnce sync.Once
	call                   agent.ConversationToolCall
}

func newInactiveIdentityModelGate() *inactiveIdentityModelGate {
	return &inactiveIdentityModelGate{entered: make(chan struct{}), released: make(chan struct{})}
}

func (g *inactiveIdentityModelGate) release() { g.releaseOnce.Do(func() { close(g.released) }) }

type inactiveIdentityDependencyModel struct {
	multipleOwnerDependencyModel
	gate *inactiveIdentityModelGate
}

func (m inactiveIdentityDependencyModel) StreamConversationStep(ctx context.Context, in agent.ConversationStepRequest, emit func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	for _, message := range in.Messages {
		if message.Role != "user" || !strings.Contains(message.Content, inactiveIdentityModelMarker) {
			continue
		}
		m.gate.enterOnce.Do(func() { close(m.gate.entered) })
		select {
		case <-ctx.Done():
			return agent.ConversationStepResult{}, ctx.Err()
		case <-m.gate.released:
		}
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{m.gate.call}}}, nil
	}
	return m.multipleOwnerDependencyModel.StreamConversationStep(ctx, in, emit)
}

func TestInactiveIdentityRechecksMultipleOwnerProfessionalDependenciesThroughRealRuntime(t *testing.T) {
	verifyMultipleOwnerDependencyRuntime(t, multipleOwnerIdentityInactive)
}

func verifyInactiveMultiOwnerIdentities(t *testing.T, f *businessWebFixture, a, b, c *businessBrowser, source agent.Conversation, receiving agent.ConversationAgent, final agent.ConversationDelegationDetail, gate *inactiveIdentityModelGate, read func(...int), profile func(*businessBrowser, string, []string, []string) agent.ConversationAgent) {
	t.Helper()
	status := func(user, email, state string, retained ...string) {
		t.Helper()
		assignments := []any{}
		for _, key := range retained {
			assignments = append(assignments, map[string]any{"role_id": key})
		}
		managedIdentityRequest(t, sharedResultIdentityHandler(f), a.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/"+user+"/account-and-roles", map[string]any{"user": map[string]any{"name": user, "email": email, "status": state}, "assignments": assignments}, fmt.Sprintf("inactive-identity-%d", time.Now().UnixNano()), 200)
	}
	login := func(browser *businessBrowser, email string) {
		t.Helper()
		browser.call("POST", "/auth/login", map[string]any{"login": email, "password": businessWebPassword}, 200)
		browser.session()
	}
	status("professional_source", "professional@example.com", "disabled", "a_results_reader", "b_old_analysis", "headquarters_admin")
	read(200, 403)
	b.call("GET", "/agent/delegations/"+final.ID, nil, 401)
	status("professional_source", "professional@example.com", "active", "a_results_reader", "b_old_analysis", "headquarters_admin")
	login(b, "professional@example.com")
	read(200)
	status("dependency_reader", "dependency@example.com", "disabled", "a_results_reader", "headquarters_admin")
	c.call("GET", "/agent/delegations/"+final.ID, nil, 401)
	var matches agent.ConversationAgentMatchPage
	if err := json.Unmarshal(b.call("POST", "/agent/agents/matches", agent.ConversationAgentMatchRequest{}, 200).Body.Bytes(), &matches); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range matches.Items {
		if candidate.AgentID == receiving.ID && candidate.CanAccept {
			t.Fatal("disabled receiving identity still admitted new work", candidate)
		}
	}
	var admissionSource agent.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "inactive-receiver-source", Title: "Inactive receiver admission"}, 200).Body.Bytes(), &admissionSource); err != nil {
		t.Fatal(err)
	}
	b.call("POST", "/agent/delegations", agent.ConversationDelegationCreate{ClientID: "inactive-receiver-admission", ConversationID: admissionSource.ID, AgentID: receiving.ID, Purpose: "Verify current receiving identity", Brief: agent.ConversationTaskBrief{Version: 1, Goal: "Inspect current delegation", Deliverable: "Verified delegation", CompletionConditions: []string{"Read under an active receiving identity"}}}, 403)
	status("dependency_reader", "dependency@example.com", "active", "a_results_reader", "headquarters_admin")
	login(c, "dependency@example.com")
	read(200)
	worker := profile(b, "inactive-identity-worker", []string{"admin"}, []string{"delegation_get"})
	var pending agent.ConversationDelegationDetail
	if err := json.Unmarshal(a.call("POST", "/agent/delegations", agent.ConversationDelegationCreate{ClientID: "inactive-identity-pending", ConversationID: source.ID, AgentID: worker.ID, Purpose: "Check current execution identity before invoking a tool", Brief: agent.ConversationTaskBrief{Version: 1, Goal: "Check current identity", Deliverable: "Recorded delegation detail", CompletionConditions: []string{"Read the delegation only under the current active execution identity"}}, Input: inactiveIdentityModelMarker}, 200).Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	gate.call = agent.ConversationToolCall{ID: "inactive-identity-effect", Name: "delegation_get", Arguments: fmt.Sprintf(`{"id":%q}`, pending.ID)}
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("actual delegated worker did not enter the held model request")
	}
	status("professional_source", "professional@example.com", "disabled", "a_results_reader", "b_old_analysis", "headquarters_admin")
	gate.release()
	for deadline := time.Now().Add(30 * time.Second); ; {
		var current agent.ConversationDelegationDetail
		if err := json.Unmarshal(a.call("GET", "/agent/delegations/"+pending.ID, nil, 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		if current.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disabled delegated worker did not stop", current.Status)
		}
		time.Sleep(250 * time.Millisecond)
	}
	status("professional_source", "professional@example.com", "active", "a_results_reader", "b_old_analysis", "headquarters_admin")
	login(b, "professional@example.com")
	var failed agent.ConversationDelegationDetail
	if err := json.Unmarshal(b.call("GET", "/agent/delegations/"+pending.ID, nil, 200).Body.Bytes(), &failed); err != nil || failed.Task == nil || failed.Task.Status != "failed" {
		t.Fatal("restoring identity resurrected the stopped execution", failed.Status, err)
	}
	var run agent.ConversationRun
	if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+failed.ConversationID+"/runs/"+failed.Task.ExecutionRunID, nil, 200).Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Status == "completed" || call.ResultReference != nil {
				t.Fatal("a model response executed its tool after the execution identity was disabled", call)
			}
		}
	}
	read(200)
	f.close()
	f.open()
	a.session()
	login(b, "professional@example.com")
	login(c, "dependency@example.com")
	read(200)
	t.Log("Actual Identity producer/publisher and receiver disable/restore, current source revalidation, new receiving admission denial, in-flight model response stopped before tool execution, accepted original delivery preserved and restart verified")
}
