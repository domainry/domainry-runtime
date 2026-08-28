package agent

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestNormalizeGuardedWrite(t *testing.T) {
	t.Parallel()

	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	contracts := []AgentGuardedWriteContract{
		{ObjectKey: "customer", Operation: "create", ActionKey: "customer.create", Endpoint: "/customers"},
		{ObjectKey: "customer", Operation: "update"},
		{ObjectKey: "customer", Operation: "update", ActionKey: "customer.update", Endpoint: "/customers/{id}", RequiresRecord: true},
		{ObjectKey: "customer", Operation: "delete", ActionKey: "customer.delete", Endpoint: "/customers/{id}", RequiresRecord: true},
		{ObjectKey: "ignored", Operation: "create"},
	}
	service := NewAgentProposalApplicationService(nil, AgentProposalDependencies{
		GuardedWrites: func(_ context.Context, got principalmodel.Principal) []AgentGuardedWriteContract {
			if got.UserID != principal.UserID {
				t.Errorf("principal=%#v", got)
			}
			return contracts
		},
	})

	if got := service.NormalizeGuardedWrite(t.Context(), nil, principal); len(got) != 0 {
		t.Fatalf("nil proposal=%v", got)
	}
	existing := map[string]any{"action_binding": map[string]any{"action_key": "existing"}}
	if got := service.NormalizeGuardedWrite(t.Context(), existing, principal); !reflect.DeepEqual(got, existing) {
		t.Fatalf("existing action binding changed: %v", got)
	}

	tests := []struct {
		name         string
		proposed     map[string]any
		wantAction   string
		wantRecordID string
		wantData     map[string]any
	}{
		{name: "create", proposed: map[string]any{"tool_binding": map[string]any{"tool_name": "createRecord", "object_key": "customer", "data": map[string]any{"name": "Ada"}}}, wantAction: "customer.create", wantData: map[string]any{"name": "Ada"}},
		{name: "update patch alias", proposed: map[string]any{"tool_call": map[string]any{"tool": "updateRecord", "objectKey": "customer", "recordId": "customer-1", "patch": map[string]any{"name": "Grace"}}}, wantAction: "customer.update", wantRecordID: "customer-1", wantData: map[string]any{"name": "Grace"}},
		{name: "update data", proposed: map[string]any{"tool_call": map[string]any{"tool": "updateRecord", "objectKey": "customer", "recordId": "customer-1", "data": map[string]any{"name": "Lin"}}}, wantAction: "customer.update", wantRecordID: "customer-1", wantData: map[string]any{"name": "Lin"}},
		{name: "delete", proposed: map[string]any{"crud_binding": map[string]any{"name": "deleteRecord", "object": "customer", "id": "customer-2"}}, wantAction: "customer.delete", wantRecordID: "customer-2", wantData: map[string]any{}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := service.NormalizeGuardedWrite(t.Context(), test.proposed, principal)
			binding := proposalMap(got["action_binding"])
			if binding["action_key"] != test.wantAction || binding["record_id"] != test.wantRecordID || binding["guarded_write"] != true {
				t.Fatalf("binding=%#v", binding)
			}
			if data := proposalMap(binding["data"]); !reflect.DeepEqual(data, test.wantData) {
				t.Fatalf("data=%v want=%v", data, test.wantData)
			}
		})
	}
}

func TestNormalizeGuardedWriteLeavesUnsupportedProposalUntouched(t *testing.T) {
	t.Parallel()

	service := NewAgentProposalApplicationService(nil, AgentProposalDependencies{
		GuardedWrites: func(context.Context, principalmodel.Principal) []AgentGuardedWriteContract {
			return []AgentGuardedWriteContract{{ObjectKey: "customer", Operation: "update", ActionKey: "customer.update", RequiresRecord: true}}
		},
	})
	tests := []map[string]any{
		{"title": "no tool"},
		{"tool": map[string]any{"name": "readRecord", "object": "customer"}},
		{"tool": map[string]any{"name": "updateRecord"}},
		{"tool": map[string]any{"name": "updateRecord", "object": "missing", "id": "1"}},
		{"tool": map[string]any{"name": "updateRecord", "object": "customer"}},
	}
	for index, proposed := range tests {
		if got := service.NormalizeGuardedWrite(t.Context(), proposed, principalmodel.Principal{}); !reflect.DeepEqual(got, proposed) {
			t.Errorf("case %d changed: got=%v want=%v", index, got, proposed)
		}
	}
	if _, ok := (*AgentProposalApplicationService)(nil).guardedWriteContract(t.Context(), principalmodel.Principal{}, "customer", "update"); ok {
		t.Fatal("nil service returned guarded contract")
	}
	if _, ok := NewAgentProposalApplicationService(nil, AgentProposalDependencies{}).guardedWriteContract(t.Context(), principalmodel.Principal{}, "customer", "update"); ok {
		t.Fatal("missing provider returned guarded contract")
	}
	if got := firstProposalString(map[string]any{"first": nil, "second": " <nil> ", "third": " value "}, "first", "second", "third"); got != "value" {
		t.Fatalf("first proposal string=%q", got)
	}
}

func TestAgentProposalExecuteActionOutcomes(t *testing.T) {
	t.Parallel()

	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}, RequestID: "request-a"}
	if got := NewAgentProposalApplicationService(nil, AgentProposalDependencies{}).execute(t.Context(), AgentProposal{Status: "draft"}, principal); got != nil {
		t.Fatalf("draft execution=%v", got)
	}
	service := NewAgentProposalApplicationService(nil, AgentProposalDependencies{})
	if got := service.execute(t.Context(), approvedActionProposal(map[string]any{"action_key": "customer.update"}), principal); got["reason"] != "binding_incomplete" {
		t.Fatalf("missing action object=%v", got)
	}
	if got := service.execute(t.Context(), approvedActionProposal(map[string]any{"object_key": "customer"}), principal); got["reason"] != "binding_incomplete" {
		t.Fatalf("incomplete action=%v", got)
	}
	if got := service.execute(t.Context(), approvedActionProposal(map[string]any{"object_key": "customer", "action_key": "customer.update"}), principal); got["reason"] != "action_executor_unavailable" {
		t.Fatalf("missing executor=%v", got)
	}
	missingID := approvedActionProposal(map[string]any{"object_key": "customer", "action_key": "customer.update"})
	missingID.ProposalID = ""
	if got := service.execute(t.Context(), missingID, principal); got["reason"] != "proposal_id_required" {
		t.Fatalf("missing proposal id=%v", got)
	}

	var invocation AgentActionInvocation
	service.dependencies.InvokeAction = func(_ context.Context, got AgentActionInvocation) (AgentActionInvocationResult, error) {
		invocation = got
		return AgentActionInvocationResult{Record: map[string]any{"id": got.RecordID}, Object: map[string]any{"created": true}}, nil
	}
	record := service.execute(t.Context(), approvedActionProposal(map[string]any{"object_key": "customer", "record_id": "customer-1", "action_key": "customer.update", "data": map[string]any{"name": "Ada"}}), principal)
	if record["status"] != "applied" || record["kind"] != "action" || invocation.Input["name"] != "Ada" || invocation.Principal.UserID != principal.UserID || invocation.RequestID != "request-a" || invocation.IdempotencyKey != "agent-proposal:proposal-action" {
		t.Fatalf("record action=%v invocation=%#v", record, invocation)
	}
	object := service.execute(t.Context(), approvedActionProposal(map[string]any{"object_key": "customer", "action_key": "customer.create"}), principal)
	if object["status"] != "applied" || object["kind"] != "object_action" || !reflect.DeepEqual(object["result"], map[string]any{"created": true}) {
		t.Fatalf("object action=%v", object)
	}

	service.dependencies.InvokeAction = func(context.Context, AgentActionInvocation) (AgentActionInvocationResult, error) {
		return AgentActionInvocationResult{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "record.conflict"}
	}
	failed := service.execute(t.Context(), approvedActionProposal(map[string]any{"object_key": "customer", "record_id": "customer-1", "action_key": "customer.update"}), principal)
	if failed["status"] != "failed" || failed["kind"] != "action" || failed["error_code"] != "record.conflict" {
		t.Fatalf("failed action=%v", failed)
	}
	failedObject := service.execute(t.Context(), approvedActionProposal(map[string]any{"object_key": "customer", "action_key": "customer.create"}), principal)
	if failedObject["kind"] != "object_action" {
		t.Fatalf("failed object action=%v", failedObject)
	}
}

func TestAgentProposalExecuteWorkflowOutcomes(t *testing.T) {
	t.Parallel()

	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-a"}}
	service := NewAgentProposalApplicationService(nil, AgentProposalDependencies{})
	if got := service.execute(t.Context(), AgentProposal{Status: "approved", Proposed: map[string]any{"workflow_binding": map[string]any{"payload": map[string]any{}}}}, principal); got["reason"] != "binding_incomplete" {
		t.Fatalf("incomplete workflow=%v", got)
	}
	proposal := AgentProposal{Status: "approved", Proposed: map[string]any{"workflow_binding": map[string]any{"workflow_key": "approval", "payload": map[string]any{"record_id": "1"}}}}
	if got := service.execute(t.Context(), proposal, principal); got["reason"] != "workflow_runner_unavailable" {
		t.Fatalf("missing workflow runner=%v", got)
	}
	service.dependencies.RunWorkflow = func(_ context.Context, key string, payload map[string]any, got principalmodel.Principal) (any, error) {
		if key != "approval" || payload["record_id"] != "1" || got.UserID != principal.UserID {
			t.Fatalf("workflow args key=%q payload=%v principal=%#v", key, payload, got)
		}
		return map[string]any{"execution_id": "run-1"}, nil
	}
	applied := service.execute(t.Context(), proposal, principal)
	if applied["status"] != "applied" || !reflect.DeepEqual(applied["result"], map[string]any{"execution_id": "run-1"}) {
		t.Fatalf("applied workflow=%v", applied)
	}
	service.dependencies.RunWorkflow = func(context.Context, string, map[string]any, principalmodel.Principal) (any, error) {
		return nil, &apperror.AppError{Kind: apperror.KindInternal, Code: "workflow.failed"}
	}
	failed := service.execute(t.Context(), proposal, principal)
	if failed["status"] != "failed" || failed["error_code"] != "workflow.failed" {
		t.Fatalf("failed workflow=%v", failed)
	}
	if got := service.execute(t.Context(), AgentProposal{Status: "approved", Proposed: map[string]any{}}, principal); got["reason"] != "no_binding" {
		t.Fatalf("no binding=%v", got)
	}
}

func TestAgentProposalDecisionExecutesOnlyApproval(t *testing.T) {
	t.Parallel()

	state := NewAgentApplicationService(NewAgentMemoryStateRepository())
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "admin"})
	store := func(id string) {
		t.Helper()
		_, err := state.StoreProposal(t.Context(), AgentProposal{ProposalID: id, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Proposed: map[string]any{"action_binding": map[string]any{"object_key": "customer", "record_id": "customer-1", "action_key": "customer.update"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	store("approved")
	store("rejected")
	invocations, lifecycleCalls := 0, 0
	service := NewAgentProposalApplicationService(state, AgentProposalDependencies{
		ResolvePrincipalRole: func(_ context.Context, userID, roleKey string) (principalmodel.Principal, error) {
			if userID != principal.UserID || roleKey != principal.RoleKey {
				t.Fatalf("approval identity=%q/%q", userID, roleKey)
			}
			refreshed := principal
			refreshed.AuthorizationRevision = "auth-rev-2"
			return refreshed, nil
		},
		InvokeAction: func(context.Context, AgentActionInvocation) (AgentActionInvocationResult, error) {
			invocations++
			return AgentActionInvocationResult{Record: map[string]any{"id": "customer-1"}}, nil
		},
		ResolveLifecycle: func(_ context.Context, proposal AgentProposal, actor principalmodel.Principal) error {
			lifecycleCalls++
			if proposal.DecisionActor != actor.UserID {
				t.Fatalf("decision actor=%q lifecycle actor=%q", proposal.DecisionActor, actor.UserID)
			}
			return nil
		},
	})
	approved, err := service.Decide(t.Context(), "approved", "approved", "safe", map[string]any{"review": true}, principal)
	if err != nil || approved.Execution["status"] != "applied" || invocations != 1 {
		t.Fatalf("approved=%#v invocations=%d err=%v", approved, invocations, err)
	}
	rejected, err := service.Decide(t.Context(), "rejected", "rejected", "unsafe", nil, principal)
	if err != nil || rejected.Status != "rejected" || rejected.Execution != nil || invocations != 1 {
		t.Fatalf("rejected=%#v invocations=%d err=%v", rejected, invocations, err)
	}
	if replay, err := service.Decide(t.Context(), "approved", "approved", "safe", nil, principal); err != nil || replay.Status != "approved" || invocations != 1 || lifecycleCalls != 3 {
		t.Fatalf("replay=%#v invocations=%d lifecycle=%d err=%v", replay, invocations, lifecycleCalls, err)
	}
	if _, err := service.Decide(t.Context(), "rejected", "approved", "changed", nil, principal); apperror.CodeOf(err) != "agent_dialog.proposal_already_decided" {
		t.Fatalf("changed decision err=%v", err)
	}
	if _, err := service.Decide(t.Context(), "approved", "unknown", "", nil, principal); apperror.CodeOf(err) != "agent_dialog.proposal_decision_invalid" {
		t.Fatalf("invalid decision err=%v", err)
	}
	if _, err := service.Decide(t.Context(), "missing", "approved", "", nil, principal); apperror.CodeOf(err) != "agent_dialog.proposal_not_found" {
		t.Fatalf("missing decision error=%v", err)
	}
	store("revoked")
	service.dependencies.ResolvePrincipalRole = func(context.Context, string, string) (principalmodel.Principal, error) {
		return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, AuthorizationRevision: "auth-rev-3"}}, accessfixture.Of(principal)), nil
	}
	if _, err := service.Decide(t.Context(), "revoked", "approved", "", nil, principal); apperror.CodeOf(err) != "agent.authorization.approval_principal_revoked" || invocations != 1 {
		t.Fatalf("revoked approval err=%v invocations=%d", err, invocations)
	}
}

func TestAgentProposalValueHelpersAndBadRequest(t *testing.T) {
	t.Parallel()

	source := map[string]any{"value": "x"}
	cloned := proposalMap(source)
	cloned["value"] = "y"
	if source["value"] != "x" || len(proposalMap([]any{})) != 0 {
		t.Fatalf("proposal map clone source=%v cloned=%v", source, cloned)
	}
	if proposalString(nil) != "" || proposalString(" value ") != "value" || proposalString(42) != "42" {
		t.Fatal("proposal string normalization mismatch")
	}
	if err := badRequest("agent.invalid"); apperror.KindOf(err) != apperror.KindBadRequest || apperror.CodeOf(err) != "agent.invalid" {
		t.Fatalf("bad request=%v", err)
	}
	if apperror.CodeOf(errors.New("plain")) == "" {
		t.Fatal("plain errors must have a stable code")
	}
}

func TestAgentProposalConcurrentConflictingDecisionsHaveOneWinner(t *testing.T) {
	state := NewAgentApplicationService(NewAgentMemoryStateRepository())
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "operator"})
	if _, err := state.StoreProposal(t.Context(), AgentProposal{ProposalID: "contended", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Proposed: map[string]any{"action_binding": map[string]any{"object_key": "customer", "record_id": "one", "action_key": "customer.update"}}}); err != nil {
		t.Fatal(err)
	}
	var actions atomic.Int64
	service := NewAgentProposalApplicationService(state, AgentProposalDependencies{
		ResolvePrincipalRole: func(context.Context, string, string) (principalmodel.Principal, error) { return principal, nil },
		InvokeAction: func(context.Context, AgentActionInvocation) (AgentActionInvocationResult, error) {
			actions.Add(1)
			return AgentActionInvocationResult{}, nil
		},
	})
	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	var wait sync.WaitGroup
	for _, decision := range []string{"approved", "rejected"} {
		wait.Add(1)
		go func(decision string) {
			defer wait.Done()
			<-start
			_, err := service.Decide(t.Context(), "contended", decision, "reviewed", nil, principal)
			errorsSeen <- err
		}(decision)
	}
	close(start)
	wait.Wait()
	close(errorsSeen)
	successes := 0
	for err := range errorsSeen {
		if err == nil {
			successes++
		} else if apperror.CodeOf(err) != "agent_dialog.proposal_decision_conflict" && apperror.CodeOf(err) != "agent_dialog.proposal_already_decided" {
			t.Fatalf("unexpected decision error=%v", err)
		}
	}
	if successes != 1 || actions.Load() > 1 {
		t.Fatalf("successes=%d actions=%d", successes, actions.Load())
	}
}

func approvedActionProposal(binding map[string]any) AgentProposal {
	return AgentProposal{ProposalID: "proposal-action", Status: "approved", Proposed: map[string]any{"action_binding": binding}}
}
