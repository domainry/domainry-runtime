package integrationtest

import (
	"path/filepath"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimerprojection "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/projection"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowWaitDurationUsesDurableRecordTimerAndResumesAfterFire(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-timer.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	identityStore := newIntegrationTestIdentityDirectory()
	if err := metadataStore(store).SyncManifest(t.Context(), recordTimerInstallationScope(), manifestmodel.ManifestSchema{Objects: recordtimerprojection.RecordTimerSystemObjects()}); err != nil {
		t.Fatal(err)
	}
	workflow := definitionmodel.WorkflowSchema{Key: "delayed_follow_up", Name: "Delayed Follow Up", Enabled: true, Trigger: map[string]any{"type": "manual"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Condition: map[string]any{}, ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "always"}, Action: map[string]any{"type": "workflow_graph"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger", Name: "Started"},
			{ID: "wait", Type: "wait_duration", Name: "Wait", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{DurationSeconds: 1, Timezone: "UTC"}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "trigger-wait", Source: "trigger", Target: "wait"}},
	}}
	service := newWorkflowProcessTestService(t, store, workflow, identityStore)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "operator", Permissions: []string{"workflow." + workflow.Key + ".run"}})
	process, err := runWorkflowProcess(t, store, service, workflow.Key, map[string]any{"object_key": "case", "record_id": "case-1"}, principal)
	if err != nil || process.Status != "waiting" || len(process.CurrentNodeIDs) != 1 || process.CurrentNodeIDs[0] != "wait" {
		t.Fatalf("waiting process=%#v err=%v", process, err)
	}
	timerObject := recordTimerRuntimeObjectByKey(t, recordtimerprojection.RecordTimerSystemObjects(), "record_timer")
	timers, err := recordLegacyStore(store).ListRecords(t.Context(), "workspace-primary", timerObject, recordmodel.RecordListQuery{Page: 1, PageSize: 10})
	if err != nil || timers.Total != 1 || timers.Items[0].Data["status"] != "scheduled" {
		t.Fatalf("durable workflow timer page=%#v err=%v", timers, err)
	}
	now := time.Now().UTC().Add(2 * time.Second)
	processed, err := service.Applications().RecordTimers.ProcessDueRecordTimers(t.Context(), "workspace-primary", now, 10, recordTimerRuntimeSystemScope())
	if err != nil || processed != 1 {
		t.Fatalf("process workflow timer count=%d err=%v", processed, err)
	}
	completed, found, err := workflowProcessStore(store).GetProcess(t.Context(), "workspace-primary", process.ID)
	if err != nil || !found || completed.Status != "completed" || completed.CompletedAt == "" {
		t.Fatalf("completed process=%#v found=%v err=%v", completed, found, err)
	}
	timer, found, err := recordLegacyStore(store).GetRecord(t.Context(), "workspace-primary", timerObject, timers.Items[0].ID)
	if err != nil || !found || timer.Data["status"] != "fired" || timer.Data["fired_at"] == "" {
		t.Fatalf("fired timer=%#v found=%v err=%v", timer, found, err)
	}
	nodes, err := workflowProcessStore(store).ListNodes(t.Context(), "workspace-primary", process.ID)
	if err != nil || len(nodes) != 2 || nodes[1].Status != "success" || nodes[1].Output["resumed"] != true {
		t.Fatalf("timer node evidence=%#v err=%v", nodes, err)
	}
}

func TestWorkflowApprovalDeadlineUsesDurableRecordTimerForEscalation(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-approval-timer.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	identityStore := newIntegrationTestIdentityDirectory()
	if err := metadataStore(store).SyncManifest(t.Context(), recordTimerInstallationScope(), manifestmodel.ManifestSchema{Objects: recordtimerprojection.RecordTimerSystemObjects()}); err != nil {
		t.Fatal(err)
	}
	workflow := definitionmodel.WorkflowSchema{Key: "approval_deadline", Name: "Approval Deadline", Enabled: true, Trigger: map[string]any{"type": "manual"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Condition: map[string]any{}, ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "always"}, Action: map[string]any{"type": "workflow_graph"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger"},
			{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
				Mode: "any", DueSeconds: 1, EscalationSeconds: 1,
				Resolvers:           []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}},
				EscalationResolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"manager"}}},
			}}},
		}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "trigger-approval", Source: "trigger", Target: "approval"}},
	}}
	service := newWorkflowProcessTestService(t, store, workflow, identityStore)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "operator", Permissions: []string{"workflow." + workflow.Key + ".run"}})
	process, err := runWorkflowProcess(t, store, service, workflow.Key, map[string]any{"object_key": "request", "record_id": "request-1"}, principal)
	if err != nil || process.Status != "waiting" {
		t.Fatalf("approval process=%#v err=%v", process, err)
	}
	timerObject := recordTimerRuntimeObjectByKey(t, recordtimerprojection.RecordTimerSystemObjects(), "record_timer")
	timers, err := recordLegacyStore(store).ListRecords(t.Context(), "workspace-primary", timerObject, recordmodel.RecordListQuery{Page: 1, PageSize: 10})
	if err != nil || timers.Total != 1 || timers.Items[0].Data["target_key"] != "approval_deadline" || timers.Items[0].Data["purpose"] != "approval_escalation" {
		t.Fatalf("approval timers=%#v err=%v", timers, err)
	}
	processed, err := service.Applications().RecordTimers.ProcessDueRecordTimers(t.Context(), "workspace-primary", time.Now().UTC().Add(3*time.Second), 10, recordTimerRuntimeSystemScope())
	if err != nil || processed != 1 {
		t.Fatalf("process approval timer count=%d err=%v", processed, err)
	}
	tasks, err := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, "", "open", 10)
	if err != nil || len(tasks) != 1 || tasks[0].AssigneeUserID != "manager" {
		t.Fatalf("escalated approval tasks=%#v err=%v", tasks, err)
	}
	timer, found, err := recordLegacyStore(store).GetRecord(t.Context(), "workspace-primary", timerObject, timers.Items[0].ID)
	if err != nil || !found || timer.Data["status"] != "fired" {
		t.Fatalf("approval timer=%#v found=%v err=%v", timer, found, err)
	}
}
