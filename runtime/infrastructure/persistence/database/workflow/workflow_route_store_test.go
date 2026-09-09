package workflow_test

import (
	"path/filepath"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowRouteStoreInsertsListsAndCompareAndSets(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "route.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	routes := workflowpersistence.NewWorkflowRouteStore(store)
	steps := []workflowmodel.WorkflowRouteStep{
		{ID: "step_1", ProcessID: "process_1", NodeID: "approval", StepNo: 1, StepKey: "s1", Title: "Review", Mode: "any", Status: "pending", AssigneeSnapshot: []workflowmodel.WorkflowRouteAssignee{{UserID: "user_a", DisplayName: "A", RoleKey: "approver"}}, CreatedAt: "t0", UpdatedAt: "t0"},
		{ID: "step_2", ProcessID: "process_1", NodeID: "approval", StepNo: 2, StepKey: "s2", Mode: "quorum", RequiredApprovals: 2, Status: "configurable", CreatedAt: "t0", UpdatedAt: "t0"},
	}
	if err := routes.InsertRouteSteps(t.Context(), "workspace-primary", steps); err != nil {
		t.Fatal(err)
	}
	if err := routes.InsertRouteSteps(t.Context(), "", steps); err == nil {
		t.Fatal("a blank workspace was accepted")
	}
	listed, err := routes.ListRouteSteps(t.Context(), "workspace-primary", "process_1")
	if err != nil || len(listed) != 2 || listed[0].StepNo != 1 || listed[1].StepNo != 2 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	if len(listed[0].AssigneeSnapshot) != 1 || listed[0].AssigneeSnapshot[0].UserID != "user_a" || listed[0].AssigneeSnapshot[0].RoleKey != "approver" {
		t.Fatalf("assignee snapshot=%#v", listed[0].AssigneeSnapshot)
	}
	if listed[1].RequiredApprovals != 2 || len(listed[1].AssigneeSnapshot) != 0 {
		t.Fatalf("deferred step=%#v", listed[1])
	}
	if _, err := routes.ListRouteSteps(t.Context(), "workspace-primary", " "); err == nil {
		t.Fatal("a blank process was accepted")
	}
	configured := listed[1]
	configured.Status, configured.ConfiguredBy, configured.ConfigureSource = "pending", "user_a", "decision"
	configured.AssigneeSnapshot = []workflowmodel.WorkflowRouteAssignee{{UserID: "user_b"}}
	ok, err := routes.UpdateRouteStepCAS(t.Context(), "workspace-primary", configured, "configurable")
	if err != nil || !ok {
		t.Fatalf("configure ok=%v err=%v", ok, err)
	}
	ok, err = routes.UpdateRouteStepCAS(t.Context(), "workspace-primary", configured, "configurable")
	if err != nil || ok {
		t.Fatalf("second configure ok=%v err=%v", ok, err)
	}
	listed, err = routes.ListRouteSteps(t.Context(), "workspace-primary", "process_1")
	if err != nil || listed[1].Status != "pending" || listed[1].ConfiguredBy != "user_a" || len(listed[1].AssigneeSnapshot) != 1 {
		t.Fatalf("configured step=%#v err=%v", listed[1], err)
	}
	if listed, err := routes.ListRouteSteps(t.Context(), "workspace-other", "process_1"); err != nil || len(listed) != 0 {
		t.Fatalf("cross-workspace steps=%#v err=%v", listed, err)
	}
}
