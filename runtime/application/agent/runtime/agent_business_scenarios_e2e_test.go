package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestResumeScreeningScenarioUsesHRScopeAndProposalOnly(t *testing.T) {
	authorization, initiator := resumeScreeningAuthorizationFixture()
	now := time.Date(2026, 8, 4, 18, 0, 0, 0, time.UTC)
	credentials := NewAgentTaskCredentialApplicationService([]byte(strings.Repeat("r", 32)), agentTaskClock{now: now}, agentCredentialIDStub{})
	token, err := credentials.Issue(t.Context(), AgentTaskCredentialClaims{WorkspaceID: initiator.WorkspaceID, ProcessID: "resume-process", TaskRunID: "resume-run", Principal: agentPrincipalReference(initiator), AllowedTools: []string{AgentToolQueryRecords, AgentToolInvokeAction}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	run, owner, _ := runningAgentTaskRun(now)
	run.ID, run.WorkspaceID, run.ProcessID, run.NodeInstanceID, run.TaskKey, run.TaskVersion = "resume-run", initiator.WorkspaceID, "resume-process", "screen-node", "resume.screen", "1.0.0"
	run.Identity = agentsdk.ExecutionIdentity{Mode: agentsdk.AgentTaskIdentityInherit, Initiator: agentPrincipalReference(initiator), Execution: agentPrincipalReference(initiator)}
	run.Lease.Owner, run.Lease.FencingToken = string(owner), 1
	repository := &agentTaskRunRepositoryStub{current: run}
	queries, proposals, ledger := &agentToolQueryStub{}, &agentToolProposalStub{}, &agentToolLedgerStub{}
	gateway := NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: authorization, Credentials: credentials, Queries: queries, Proposals: proposals, Risk: agentToolRiskStub{proposal: true}, Ledger: ledger, TaskRuns: NewAgentTaskRunApplicationService(repository, agentTaskClock{now: now})})
	request := AgentToolInvocationRequest{Credential: token, WorkspaceID: initiator.WorkspaceID, ProcessID: run.ProcessID, TaskRunID: run.ID, Owner: owner, FencingToken: 1, Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, TaskKey: run.TaskKey, TaskVersion: run.TaskVersion, NodeAllowedObjects: []string{"candidate", "resume"}, NodeAllowedActions: []string{"candidate.reject"}, NodeAllowedOutcomes: []string{"success", "manual_review", "rejected", "error"}}
	request.Tool, request.Input = AgentToolQueryRecords, map[string]any{"object_key": "candidate"}
	if _, err := gateway.Invoke(t.Context(), request); err != nil || strings.Join(queries.fields, ",") != "experience,name" || queries.principal.UserID != initiator.UserID {
		t.Fatalf("HR-scoped query fields=%v principal=%#v err=%v", queries.fields, queries.principal, err)
	}
	request.Tool, request.IdempotencyKey = AgentToolInvokeAction, "reject-candidate-1"
	request.Input = map[string]any{"action_key": "candidate.reject", "object_key": "candidate", "record_id": "candidate-1", "data": map[string]any{"reason": "requirements_not_met"}}
	result, err := gateway.Invoke(t.Context(), request)
	if err != nil || result.Status != "proposal_required" || proposals.calls != 1 || proposals.last.Principal.UserID != initiator.UserID || repository.current.Status != agentmodel.AgentTaskRunWaitingApproval {
		t.Fatalf("resume rejection result=%#v proposal=%#v run=%#v err=%v", result, proposals.last, repository.current, err)
	}
}

func TestCustomerPreprocessingScenarioUsesServiceIdentityAndStrictRedactedOutput(t *testing.T) {
	authorization, initiator, schema, directory := agentAuthorizationFixture()
	schema.full.AgentTasks[0].OutputSchema = supportPreprocessingOutputSchema()
	schema.full.AgentTasks[0].ExecutionLimits.MaxOutputBytes = 4096
	filtered := schema.filtered["operator:operator"]
	filtered.AgentTasks[0].OutputSchema = supportPreprocessingOutputSchema()
	filtered.AgentTasks[0].ExecutionLimits.MaxOutputBytes = 4096
	schema.filtered["operator:operator"] = filtered
	servicePrincipal := directory.principals["agent_customer:agent_service"]
	serviceFiltered := schema.filtered["operator:operator"]
	schema.filtered[servicePrincipal.UserID+":"+servicePrincipal.RoleKey] = serviceFiltered
	execution, identity, err := authorization.ResolveExecutionIdentity(t.Context(), AgentExecutionIdentityRequest{Initiator: initiator, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "customer_service"}, ExpectedRotationVersion: 3})
	if err != nil || execution.UserID != "agent_customer" || identity.Initiator.UserID != initiator.UserID || identity.Execution.UserID == identity.Initiator.UserID {
		t.Fatalf("service identity execution=%#v identity=%#v err=%v", execution, identity, err)
	}
	valid := map[string]any{"redacted_summary": "Customer requests order help", "category": "order_change", "sentiment": "negative", "priority": "high"}
	if err := validateAgentTaskOutput(supportPreprocessingOutputSchema(), valid, 4096); err != nil {
		t.Fatalf("valid support output rejected: %v", err)
	}
	withPII := map[string]any{"redacted_summary": "safe", "category": "refund", "sentiment": "neutral", "priority": "normal", "raw_phone": "+86-secret"}
	if err := validateAgentTaskOutput(supportPreprocessingOutputSchema(), withPII, 4096); err == nil {
		t.Fatal("undeclared raw PII escaped strict output schema")
	}
}

func TestGlobalAgentCrossPageScenarioRestoresStableHandoffAndRejectsOldAuthority(t *testing.T) {
	authorization, principal, trusted, repository := interactiveExecutionFixture(t)
	principal.AuthorizationRevision = trusted.Principal.AuthorizationRevision
	runs := NewAgentInteractiveRunApplicationService(repository, agentTaskClock{now: time.Date(2026, 8, 4, 19, 0, 0, 0, time.UTC)}, agentCredentialIDStub{})
	dispatch := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: time.Now().UTC()}, agentCredentialIDStub{})
	runner := interactiveAgentRunnerFunc(func(_ context.Context, _ agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
		return agentsdk.InteractiveResult{Route: &agentsdk.RouteResult{RouteType: agentsdk.AgentRouteTask, TargetKey: "customer.review", TargetVersion: "1.0.0", Input: map[string]any{"record_id": "customer-1"}, IdempotencyKey: "page-handoff-1"}}, nil
	})
	service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs, Authorize: authorization, Runner: runner, Dispatch: dispatch})
	result, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{SessionID: "session-page", IdempotencyKey: "message-page", Message: "review current record", Context: trusted, Principal: principal})
	if err != nil || result.Result.Handoff == nil || result.Result.Handoff.TaskRunID == "" {
		t.Fatalf("handoff=%#v err=%v", result, err)
	}
	// A new application service over the same durable repository models drawer close,
	// page refresh, or Runtime process restart. Recovery uses only the stable run ID.
	restarted := NewAgentInteractiveRunApplicationService(repository, nil, nil)
	restored, found, err := restarted.Get(t.Context(), result.Run.ID, principal)
	if err != nil || !found || restored.TaskRunID != result.Result.Handoff.TaskRunID {
		t.Fatalf("restored=%#v found=%v err=%v", restored, found, err)
	}
	revoked := principal
	revoked.AuthorizationRevision = "revoked-after-handoff"
	if _, _, err := restarted.Get(t.Context(), result.Run.ID, revoked); err == nil {
		t.Fatal("historical handoff restored data under stale authority")
	}
}

func resumeScreeningAuthorizationFixture() (*AgentAuthorizationApplicationService, principalmodel.Principal) {
	role := accessfixture.Bundle{Key: "hr_recruiter", Permissions: []string{"candidate.read", "resume.read", "candidate.reject"}, RecordScope: "assigned_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "candidate", Scope: "assigned_records", Read: true, Write: true}, {ObjectKey: "resume", Scope: "assigned_records", Read: true}}}
	initiator := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "hr-user", WorkspaceID: "workspace-hr", AuthorizationRevision: "hr-auth-1"}}, role)
	fresh := initiator
	fresh.AuthorizationRevision = "hr-auth-2"
	directory := &agentPrincipalDirectoryStub{principals: map[string]principalmodel.Principal{"hr-user:hr_recruiter": fresh}}
	task := agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: "resume.screen", Version: "1.0.0", AgentKey: "talent_agent", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, AllowedObjects: []string{"candidate", "resume"}, AllowedActions: []string{"candidate.reject"}, AllowedOutcomes: []string{"success", "manual_review", "rejected", "error"}, SideEffectMode: agentsdk.AgentTaskSideEffectProposalOnly, ExecutionLimits: agentsdk.AgentExecutionLimits{MaxToolCalls: 4, MaxInputBytes: 4096, MaxOutputBytes: 4096}, Enabled: true}
	full := appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "hr-full", Objects: []definitionmodel.ObjectSchema{{Key: "candidate", Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "experience"}, {Key: "private_phone"}}}, {Key: "resume"}}, Actions: []definitionmodel.ActionSchema{{Key: "candidate.reject", ObjectKey: "candidate"}}, Skills: []agentsdk.SkillSchema{{Key: "talent_reader", Version: "1.0.0", Name: "Talent reader", AllowedTools: []string{"query_records", "invoke_action"}, AllowedObjects: []string{"candidate", "resume"}}}, Agents: []agentsdk.AgentSchema{{Key: "talent_agent", Version: "1.0.0", Name: "Talent agent", SkillKeys: []string{"talent_reader"}}}, AgentTasks: []agentsdk.AgentTaskDefinition{task}}
	filtered := full
	filtered.SchemaHash = "hr-filtered"
	filtered.Objects = []definitionmodel.ObjectSchema{{Key: "candidate", Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "experience"}}}, {Key: "resume"}}
	schema := &agentSchemaProviderStub{full: full, filtered: map[string]appschemamodel.ApplicationSchemaSnapshot{"hr-user:hr_recruiter": filtered}}
	return NewAgentAuthorizationApplicationService(AgentAuthorizationDependencies{Principals: directory, Schema: schema, Records: agentRecordVisibilityStub{denied: map[string]bool{}}}), initiator
}

func supportPreprocessingOutputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"redacted_summary", "category", "sentiment", "priority"}, "properties": map[string]any{"redacted_summary": map[string]any{"type": "string"}, "category": map[string]any{"type": "string"}, "sentiment": map[string]any{"type": "string"}, "priority": map[string]any{"type": "string"}}, "additionalProperties": false}
}
