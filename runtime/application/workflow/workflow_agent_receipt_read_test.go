package workflow

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestWorkflowIndependentReceiptReadRequiresCurrentParticipantAndOriginalLedger(t *testing.T) {
	definition := workflowExecutionSchema()
	permission := "workflow.receipt." + definition.Key + ".read"
	principal := accessfixture.Attach(workflowExecutionPrincipal(), accessfixture.Bundle{Permissions: []string{permission}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{permission}, identitysdk.DataScopeOwner)})
	payload := map[string]any{"source": "conversation"}
	key, _ := agentWorkflowInvocationKey(definition.Key, "original", principal)
	fingerprint, _ := workflowExecutionFingerprint(definition, payload, "agent", 1)
	execution := workflowmodel.WorkflowExecution{ID: "execution", ProcessID: "process", WorkspaceID: principal.WorkspaceID, WorkflowKey: definition.Key, ActorID: principal.UserID, Trigger: "agent", IdempotencyKey: key, Status: "waiting", Payload: map[string]any{"private": "original-private-execution"}}
	receipt := workflowmodel.WorkflowExecutionReceipt{WorkspaceID: principal.WorkspaceID, WorkflowKey: definition.Key, IdempotencyKey: key, RequestFingerprint: fingerprint, ExecutionID: execution.ID, Status: string(idempotency.StatusSucceeded), ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}
	probe := &agentWorkflowReceiptProbe{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{execution.ID: execution}}, receipt: receipt, found: true}
	service := newWorkflowExecutionService(&probe.workflowExecutionWorkerStub, definition)
	service.workerRepo = probe
	processes := service.processRepo.(*workflowExecutionProcessStub)
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: principal.WorkspaceID, WorkflowKey: definition.Key, WorkflowName: definition.Name, InitiatorID: principal.UserID, Status: "waiting", DefinitionSnapshot: definition, Variables: map[string]any{"private": "private-process-input"}, Result: map[string]any{"private": "private-process-output"}}
	processes.processes[process.ID] = process
	out, found, err := service.ReadAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", principal)
	if err != nil || !found || out.ExecutionID != execution.ID || out.ProcessID != process.ID {
		t.Fatal(out, found, err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "private") {
		t.Fatal("raw workflow execution escaped", string(raw))
	}
	if _, _, err := service.InspectAgentWorkflowInvocation(t.Context(), definition.Key, payload, "original", principal); err == nil {
		t.Fatal("execution inspection accepted receipt permission")
	}
	if _, err := service.RunAgentWorkflowWithKey(t.Context(), definition.Key, payload, "original", principal); err == nil {
		t.Fatal("receipt reading granted workflow execution")
	}
	for _, change := range []string{"receipt-grant", "guardrail", "participant", "process-workspace", "process-workflow", "execution-actor", "fingerprint", "in-progress", "expiry", "missing", "disabled-definition"} {
		t.Run(change, func(t *testing.T) {
			p := principal
			probe.receipt, probe.found, probe.executions[execution.ID] = receipt, true, execution
			processes.processes[process.ID] = process
			service.registry.(*workflowRegistryStub).items[definition.Key] = definition
			switch change {
			case "receipt-grant":
				p = accessfixture.Attach(principal, accessfixture.Bundle{})
			case "guardrail":
				p = accessfixture.Attach(principal, accessfixture.Bundle{Permissions: []string{permission}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{permission}, identitysdk.DataScopeOwner), Guardrails: []accessfixture.GuardrailFixture{{Key: "receipt-deny", DeniedPermissionKeys: []string{permission}}}})
			case "participant":
				v := process
				v.InitiatorID = "other"
				processes.processes[process.ID] = v
			case "process-workspace":
				v := process
				v.WorkspaceID = "other"
				processes.processes[process.ID] = v
			case "process-workflow":
				v := process
				v.WorkflowKey = "other"
				processes.processes[process.ID] = v
			case "execution-actor":
				v := execution
				v.ActorID = "other"
				probe.executions[execution.ID] = v
			case "fingerprint":
				probe.receipt.RequestFingerprint = "changed"
			case "in-progress":
				probe.receipt.Status = string(idempotency.StatusProcessing)
			case "expiry":
				probe.receipt.ExpiresAt = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
			case "missing":
				probe.found = false
			case "disabled-definition":
				v := definition
				v.Enabled = false
				service.registry.(*workflowRegistryStub).items[definition.Key] = v
			}
			if _, found, err := service.ReadAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", p); err == nil && found {
				t.Fatal("receipt read bypassed current owner policy", change)
			}
		})
	}
	t.Run("shared-original-ledger-and-actual-participant", func(t *testing.T) {
		probe.receipt, probe.found, probe.executions[execution.ID] = receipt, true, execution
		processes.processes[process.ID] = process
		service.registry.(*workflowRegistryStub).items[definition.Key] = definition
		participants := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: *processes}
		service.processRepo = participants
		defer func() { service.processRepo = processes }()
		reader := principal
		reader.UserID = "shared-reader"
		reader = accessfixture.Attach(reader, accessfixture.Bundle{Permissions: []string{permission}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{permission}, identitysdk.DataScopeOwner)})
		metadataReads := probe.reads
		if _, err := service.AgentSharedWorkflowReceiptDefinition(t.Context(), definition.Key, reader, principal); err == nil {
			t.Fatal("self-only workflow metadata authorized another owner")
		}
		participants.tasks = []workflowmodel.WorkflowTask{{ProcessID: process.ID, AssigneeUserID: reader.UserID}}
		if _, found, err := service.ReadSharedAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", reader, principal); err == nil && found {
			t.Fatal("self-only receipt policy authorized original producer")
		}
		reader = accessfixture.Attach(reader, accessfixture.Bundle{Permissions: []string{permission}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{permission}, identitysdk.DataScopeAll)})
		metadataReads = probe.reads
		metadata, err := service.AgentSharedWorkflowReceiptDefinition(t.Context(), definition.Key, reader, principal)
		if err != nil || metadata.Key != definition.Key || probe.reads != metadataReads {
			t.Fatal("workflow metadata required run permission or queried original ledger", metadata, err, probe.reads)
		}
		deniedMetadata := accessfixture.Attach(reader, accessfixture.Bundle{Permissions: []string{permission}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{permission}, identitysdk.DataScopeAll), Guardrails: []accessfixture.GuardrailFixture{{Key: "metadata-deny", DeniedPermissionKeys: []string{permission}}}})
		if _, err := service.AgentSharedWorkflowReceiptDefinition(t.Context(), definition.Key, deniedMetadata, principal); err == nil {
			t.Fatal("metadata ignored receipt guardrail")
		}
		foreign := principal
		foreign.WorkspaceID = "foreign"
		if _, err := service.AgentSharedWorkflowReceiptDefinition(t.Context(), definition.Key, reader, foreign); err == nil {
			t.Fatal("metadata producer crossed workspace")
		}
		out, found, err := service.ReadSharedAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", reader, principal)
		if err != nil || !found || out.ExecutionID != execution.ID || out.ProcessID != process.ID {
			t.Fatal("current participant could not read original producer start ledger", out, found, err)
		}
		if _, found, err := service.ReadAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", reader); err == nil && found {
			t.Fatal("ordinary receipt read acquired foreign actor ledger")
		}
		participants.tasks = nil
		if _, found, err := service.ReadSharedAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", reader, principal); err == nil && found {
			t.Fatal("original producer participation authorized actual reader after withdrawal")
		}
		participants.tasks = []workflowmodel.WorkflowTask{{ProcessID: process.ID, AssigneeUserID: reader.UserID}}
		wrong := principal
		wrong.UserID = "unrelated-actor"
		if _, found, err := service.ReadSharedAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", reader, wrong); err == nil && found {
			t.Fatal("wrong producer acquired ledger")
		}
	})
	if len(probe.claimRequests) != 0 || len(probe.inserted) != 0 || len(probe.updated) != 0 {
		t.Fatal("read touched execution state", probe)
	}
	if _, _, err := service.ReadAgentWorkflowReceipt(t.Context(), definition.Key, payload, "original", principalmodel.Principal{}); err == nil {
		t.Fatal("unknown reader accepted")
	}
}
