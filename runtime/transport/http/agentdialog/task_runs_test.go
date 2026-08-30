package agentdialog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestAgentTaskProjectionShowsGovernedEvidenceAndRedactsSensitiveInternals(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	run := agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "workspace", ProcessID: "process", TaskKey: "screen", TaskVersion: "1", Status: agentmodel.AgentTaskRunWaitingApproval, Input: map[string]any{"secret": "hidden"}, Output: map[string]any{"score": 90}, RawEvidenceRef: "raw-secret", Attempt: 1, MaxAttempts: 3, Revision: 4, CreatedAt: now, UpdatedAt: now,
		Identity: agentsdk.ExecutionIdentity{Mode: "inherit", Initiator: agentsdk.PrincipalReference{UserID: "user", RoleKey: "hr"}, Execution: agentsdk.PrincipalReference{UserID: "user", RoleKey: "hr"}},
		Approval: &agentmodel.AgentTaskApproval{ProposalID: "proposal", Execution: map[string]any{"action_receipt_id": "receipt"}}, Evidence: agentmodel.AgentTaskExecutionEvidence{AuditRefs: []string{"audit"}}, Reconciliation: agentmodel.AgentTaskReconciliation{Required: true, State: "poll_required"},
	}
	raw, err := json.Marshal(projectAgentTaskRun(run))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{"proposal", "receipt", "audit", `"score":90`, `"reconciliation_required":true`} {
		if !strings.Contains(text, expected) {
			t.Errorf("projection missing %s: %s", expected, text)
		}
	}
	for _, forbidden := range []string{"hidden", "raw-secret", "fencing_token", "tool_invocations"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("projection leaked %s: %s", forbidden, text)
		}
	}
}

func TestAgentTaskOperationsRequireExplicitPermissions(t *testing.T) {
	if agentTaskPermission(principalmodel.Principal{}, "agent.task.read") {
		t.Fatal("unknown principal was authorized")
	}
	if agentTaskPermission(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workflow.process.read"}}), "agent.task.read") {
		t.Fatal("workflow permission widened Agent Task access")
	}
	if !agentTaskPermission(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"agent.task.read"}}), "agent.task.read") {
		t.Fatal("declared permission denied")
	}
	if !agentTaskPermission(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}}), "agent.task.operate") {
		t.Fatal("workspace admin denied")
	}
}

func TestAgentTaskAuthorizationRevisionPreventsRestoringOldResult(t *testing.T) {
	run := agentmodel.AgentTaskRun{Identity: agentsdk.ExecutionIdentity{Initiator: agentsdk.PrincipalReference{AuthorizationRevision: "auth-1"}}}
	if !agentTaskAuthorizationStale(run, "auth-2") || agentTaskAuthorizationStale(run, "auth-1") || agentTaskAuthorizationStale(run, "") {
		t.Fatal("task result authorization revision boundary mismatch")
	}
}
