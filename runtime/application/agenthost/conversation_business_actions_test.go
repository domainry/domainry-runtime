package agenthost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type businessActionPortProbe struct {
	definitions          []definitionmodel.ActionSchema
	invokes, inspections int
	inspection           actionapplication.ActionInvocationInspection
	inspectErr           error
	last                 actionmodel.ActionInvocation
}

func (p *businessActionPortProbe) Definitions() []definitionmodel.ActionSchema { return p.definitions }
func (p *businessActionPortProbe) Invoke(_ context.Context, source actionmodel.ActionSource, in actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
	p.invokes++
	p.last = in
	if source != actionmodel.ActionSourceAgent || !in.PreventExecutionReclaim {
		return actionmodel.ActionInvocationResult{}, fmt.Errorf("unsafe conversation invocation")
	}
	return actionmodel.ActionInvocationResult{InvocationID: in.IdempotencyKey, Record: &actionmodel.ActionResult{ActionKey: in.ActionKey, ObjectKey: in.ObjectKey, RecordID: in.RecordID, Output: map[string]any{"private": "unfiltered-handler-output"}}}, nil
}
func (p *businessActionPortProbe) InspectInvocation(_ context.Context, in actionmodel.ActionInvocation) (actionapplication.ActionInvocationInspection, error) {
	p.inspections++
	p.last = in
	return p.inspection, p.inspectErr
}

func TestConversationBusinessActionValidatesContractConfirmationAndReconciliation(t *testing.T) {
	host, resolver, reads, a := newConversationBusinessFixture(t)
	permissions := []string{"customer.read", "customer.rename"}
	resolver.principal = accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: a.UserID, WorkspaceID: a.WorkspaceID}}, accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeOwner), FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "name", Read: true}}})
	action := definitionmodel.ActionSchema{Key: "customer.rename", ObjectKey: "customer", Kind: "record_update", OptimisticConcurrency: true, PayloadFields: []definitionmodel.ActionPayloadField{{Key: "name", Type: "text", Required: true}}}
	port := &businessActionPortProbe{definitions: []definitionmodel.ActionSchema{action}}
	if err := WithConversationBusinessActions(port)(host); err != nil {
		t.Fatal(err)
	}
	if err := WithConversationBusinessEvidenceKey([]byte(strings.Repeat("k", 32)))(host); err != nil {
		t.Fatal(err)
	}
	host.schema = appschemaapplication.NewApplicationSchemaQueryApplicationService(businessSchemaSnapshot{appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{reads.object}, Actions: port.definitions}}, nil)
	page, err := host.BusinessCatalog(t.Context(), agentsdk.ConversationBusinessCatalogQuery{Kind: "actions", ObjectKey: "customer", ActionKey: action.Key}, a)
	if err != nil || len(page.Actions) != 1 || page.Actions[0].ExecutionVersion == "" || !strings.Contains(string(page.Actions[0].InputSchema), "expected_updated_at") {
		t.Fatal(page, err)
	}
	q := agentsdk.ConversationBusinessAction{ObjectKey: "customer", ActionKey: action.Key, Version: page.Actions[0].ExecutionVersion, RecordID: "own-1", Data: json.RawMessage(`{"name":"New Name","expected_updated_at":"2026-09-10T01:00:00Z"}`)}
	confirmed := func(q agentsdk.ConversationBusinessAction) agentsdk.ConversationBusinessActionRequest {
		raw, _ := json.Marshal(q)
		return agentsdk.ConversationBusinessActionRequest{Authority: a, Action: q, Arguments: string(raw), ConversationID: "conversation", RunID: "run", CallID: "call", IdempotencyKey: "stable-call-key", Confirmation: &agentsdk.ConversationConfirmation{ID: "actual-confirmation", UserID: a.UserID, ActionKey: agentsdk.ConversationToolActionPrefix + "invoke_action", ToolVersion: "1", ArgumentsHash: conversationBusinessDigest(string(raw)), ApprovedAt: time.Now().UTC()}}
	}
	for _, field := range []string{"version", "target", "confirmation", "actor", "arguments", "payload", "concurrency", "metadata", "empty-contract"} {
		t.Run(field, func(t *testing.T) {
			bad := confirmed(q)
			switch field {
			case "version":
				bad.Action.Version = "guessed"
			case "target":
				bad.Action.RecordID = "other-owner"
			case "confirmation":
				bad.Confirmation = nil
			case "actor":
				bad.Confirmation.UserID = "colleague"
			case "arguments":
				bad.Arguments += " "
			case "payload":
				bad.Action.Data = json.RawMessage(`{"name":"New Name","expected_updated_at":"v1","approval_token":"forged"}`)
			case "concurrency":
				bad.Action.Data = json.RawMessage(`{"name":"New Name"}`)
			case "metadata":
				bad.RunID = ""
			case "empty-contract":
				port.definitions[0].PayloadFields = nil
				defer func() { port.definitions[0] = action }()
			}
			if _, err := host.InvokeBusinessAction(t.Context(), bad); err == nil || port.invokes != 0 {
				t.Fatal("invalid action reached mutation port", field, err)
			}
		})
	}
	request := confirmed(q)
	result, err := host.InvokeBusinessAction(t.Context(), request)
	if err != nil || result.Status != "completed" || port.invokes != 1 || port.last.Input["name"] != "New Name" || port.last.Principal.UserID != a.UserID || port.last.Principal.CorrelationID != request.RunID {
		t.Fatal(result, err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "unfiltered-handler-output") {
		t.Fatal("raw handler output escaped")
	}
	for _, status := range []string{string(idempotency.StatusProcessing), string(idempotency.StatusFailedRetryable)} {
		port.inspection = actionapplication.ActionInvocationInspection{Found: true, Status: status}
		result, err := host.ReconcileBusinessAction(t.Context(), request)
		if err != nil || result.Status != "uncertain" || port.invokes != 1 {
			t.Fatal("unresolved action was repeated", result, err)
		}
	}
	port.inspectErr = fmt.Errorf("receipt unavailable")
	if result, _ := host.ReconcileBusinessAction(t.Context(), request); result.Status != "uncertain" || port.invokes != 1 {
		t.Fatal(result)
	}
	port.inspectErr = nil
	port.inspection = actionapplication.ActionInvocationInspection{Found: true, Status: string(idempotency.StatusSucceeded), Result: actionmodel.ActionInvocationResult{InvocationID: request.IdempotencyKey, Record: &actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: "customer", RecordID: q.RecordID}}}
	result, err = host.ReconcileBusinessAction(t.Context(), request)
	if err != nil || result.Status != "completed" || port.invokes != 1 {
		t.Fatal(result, err)
	}
	raw, _ = json.Marshal(result)
	e := agentsdk.ConversationBusinessEvidence{Version: 1, Source: host.source, ScopeSHA256: conversationBusinessDigest([]string{host.source, a.RuntimeID, a.WorkspaceID, a.UserID}), Operation: "invoke_action", Input: json.RawMessage(request.Arguments), Data: raw}
	if err := host.RevalidateBusinessAction(t.Context(), e, a); err != nil || port.invokes != 1 {
		t.Fatal(err)
	}
	e.Data = json.RawMessage(strings.Replace(string(raw), `"reference_count":0`, `"reference_count":1`, 1))
	if err := host.RevalidateBusinessAction(t.Context(), e, a); err == nil || port.invokes != 1 {
		t.Fatal("forged acknowledgement accepted", err)
	}
	port.inspection = actionapplication.ActionInvocationInspection{}
	if result, err := host.ReconcileBusinessAction(t.Context(), request); err != nil || result.Status != "completed" || port.invokes != 2 {
		t.Fatal("conclusively absent action did not use guarded invocation", result, err)
	}
}
