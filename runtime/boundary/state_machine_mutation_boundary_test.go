package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateMachineOwnsOnlyPureTransitionPlanning(t *testing.T) {
	root := runtimeRoot(t)
	effectPath := filepath.Join(root, "application", "record", "record_state_machine_effect_application_service.go")
	raw, err := os.ReadFile(effectPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, required := range []string{"ApplySelfEffects", "ApplySelfPatch"} {
		if !strings.Contains(content, required) {
			t.Errorf("state machine reducer missing %q", required)
		}
	}
	for _, forbidden := range []string{"Repository", "CreateRecord", "UpdateRecord", "ExecuteWorkflow", "CommitBatch", "ApplyEffects", "Audit:"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("state machine reducer retains external effect/commit owner %q", forbidden)
		}
	}

	updateRaw, err := os.ReadFile(filepath.Join(root, "application", "record", "record_update_application_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	update := string(updateRaw)
	selfPatch := strings.Index(update, "ApplySelfEffects(ctx")
	plan := strings.Index(update, "MutationKernel.Plan(ctx")
	if selfPatch < 0 || plan < 0 || selfPatch >= plan {
		t.Fatal("state machine self patch must be lowered before the canonical MutationPlan")
	}

	validationRaw, err := os.ReadFile(filepath.Join(root, "domain", "record", "validation", "record_state_machine_validation.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(validationRaw), "backend.transition.effect_requires_action") {
		t.Fatal("state machine validator must redirect cross-object effects to Business Action")
	}
}

func TestPipelineTransitionDelegatesRecordSemanticsToCanonicalPlanner(t *testing.T) {
	root := runtimeRoot(t)
	serviceRaw, err := os.ReadFile(filepath.Join(root, "application", "pipeline", "pipeline_transition_application_service.go"))
	if err != nil {
		t.Fatal(err)
	}
	service := string(serviceRaw)
	for _, required := range []string{"PlanUpdate", "PlanCreate", "CommitPlans", "MutationSourceAction", "WorkflowTriggers"} {
		if !strings.Contains(service, required) {
			t.Errorf("Pipeline transition missing canonical delegation %q", required)
		}
	}
	for _, forbidden := range []string{"RunBefore", "AfterOutbox", "ValidateRelations", "ValidatePolicies", "ValidateUnique", "ValidateDuplicate", "BuildAudit", "PrepareWorkflow", "CommitRecordMutation"} {
		if strings.Contains(service, forbidden) {
			t.Errorf("Pipeline transition retains copied Record semantic %q", forbidden)
		}
	}

	wiringRaw, err := os.ReadFile(filepath.Join(root, "bootstrap", "composition", "pipeline_application_wiring.go"))
	if err != nil {
		t.Fatal(err)
	}
	wiring := string(wiringRaw)
	for _, required := range []string{"recordApplicationService.PlanUpdateMutation", "recordApplicationService.PlanCreateMutation", "mutationKernel.CommitBatch"} {
		if !strings.Contains(wiring, required) {
			t.Errorf("Pipeline composition missing canonical owner %q", required)
		}
	}
	for _, forbidden := range []string{"RecordValidationDomainService.Validate", "automationApplicationService.RunBefore", "recordRepo.CommitRecordMutationBatch"} {
		if strings.Contains(wiring, forbidden) {
			t.Errorf("Pipeline composition retains direct bypass %q", forbidden)
		}
	}
}
