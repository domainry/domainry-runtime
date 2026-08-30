package changeplan

import (
	"encoding/json"
	"errors"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestSimulateDraftScenariosCoversAuthorizationDraftAndCandidateFailures(t *testing.T) {
	plan := changePlanScenarioConditionPlan()
	payload, _ := json.Marshal(plan)
	repository := &changePlanDraftRepositoryFake{found: true, draft: changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 1, Status: "draft", Payload: payload}}
	runtime := &changePlanRuntimeFake{}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, runtime, &changePlanScenarioRuntimeFake{result: AcceptanceScenarioRuntimeResult{Valid: true, SideEffectFree: true}})
	if _, err := service.SimulateDraftScenarios(t.Context(), plan.PlanID, 1, principalmodel.Principal{}); err == nil {
		t.Fatal("unknown principal accepted")
	}
	nonAdmin := changePlanAdmin()
	nonAdmin = changePlanWithoutPermissions(nonAdmin)
	if _, err := service.SimulateDraftScenarios(t.Context(), plan.PlanID, 1, nonAdmin); err == nil {
		t.Fatal("non-admin accepted")
	}
	repository.getErr = errors.New("read")
	if _, err := service.SimulateDraftScenarios(t.Context(), plan.PlanID, 1, changePlanAdmin()); err == nil {
		t.Fatal("draft read error ignored")
	}
	repository.getErr = nil
	runtime.validateErr = errors.New("candidate")
	if _, err := service.SimulateDraftScenarios(t.Context(), plan.PlanID, 1, changePlanAdmin()); err == nil {
		t.Fatal("candidate error ignored")
	}
}

func TestDraftForScenarioSimulationCoversIdentityRevisionStatusAndPayloadEdges(t *testing.T) {
	plan := changePlanScenarioConditionPlan()
	payload, _ := json.Marshal(plan)
	repository := &changePlanDraftRepositoryFake{}
	service := NewChangePlanApplicationService(repository, nil, nil, &changePlanRuntimeFake{})
	principal := changePlanAdmin()
	if _, _, err := service.draftForScenarioSimulation(t.Context(), " ", 1, principal); err == nil {
		t.Fatal("blank plan accepted")
	}
	repository.getErr = errors.New("read")
	if _, _, err := service.draftForScenarioSimulation(t.Context(), plan.PlanID, 1, principal); err == nil {
		t.Fatal("read error ignored")
	}
	repository.getErr, repository.found = nil, false
	if _, _, err := service.draftForScenarioSimulation(t.Context(), plan.PlanID, 1, principal); err == nil {
		t.Fatal("missing draft accepted")
	}
	repository.found = true
	repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 1, Status: "draft", Payload: payload}
	for _, testCase := range []struct {
		expected int
		status   string
	}{
		{0, "draft"}, {2, "draft"}, {1, "invalid"},
	} {
		repository.draft.Status = testCase.status
		if _, _, err := service.draftForScenarioSimulation(t.Context(), plan.PlanID, testCase.expected, principal); err == nil {
			t.Fatalf("revision/status accepted: %#v", testCase)
		}
	}
	for _, status := range []string{"in_review", "approved"} {
		repository.draft.Status = status
		if _, _, err := service.draftForScenarioSimulation(t.Context(), plan.PlanID, 1, principal); err != nil {
			t.Fatalf("status %s rejected: %v", status, err)
		}
	}
	repository.draft.Status, repository.draft.Payload = "draft", json.RawMessage(`{`)
	if _, _, err := service.draftForScenarioSimulation(t.Context(), plan.PlanID, 1, principal); err == nil {
		t.Fatal("malformed draft accepted")
	}
	wrong := plan
	wrong.PlanID = "other"
	repository.draft.Payload, _ = json.Marshal(wrong)
	if _, _, err := service.draftForScenarioSimulation(t.Context(), plan.PlanID, 1, principal); err == nil {
		t.Fatal("identity mismatch accepted")
	}
}

func TestSimulateAcceptanceScenariosCoversContractAndRuntimeEdges(t *testing.T) {
	principal := changePlanAdmin()
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, nil, &changePlanRuntimeFake{})
	if result, err := service.simulateAcceptanceScenarios(t.Context(), changeplanmodel.BusinessSystemChangePlan{PlanID: "plan"}, nil, principal); err != nil || !result.Passed {
		t.Fatalf("empty result=%#v err=%v", result, err)
	}
	plan := changePlanScenarioConditionPlan()
	mutation := appschemamodel.ApplicationDefinitionMutation{Operation: "update", ResourceType: "action", ResourceKey: "order.approve", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}}
	ignored := appschemamodel.ApplicationDefinitionMutation{Operation: "delete", ResourceType: "object", ResourceKey: "ignored"}
	ignoredAction := appschemamodel.ApplicationDefinitionMutation{Operation: "delete", ResourceType: "action", ResourceKey: "ignored-action"}
	if _, err := service.simulateAcceptanceScenarios(t.Context(), plan, []appschemamodel.ApplicationDefinitionMutation{ignored, mutation}, principal); err == nil {
		t.Fatal("missing scenario runtime accepted")
	}
	runtime := &changePlanScenarioRuntimeFake{result: AcceptanceScenarioRuntimeResult{Valid: true, SideEffectFree: true}}
	service.scenarios = runtime
	createMutation := mutation
	createMutation.Operation = "create"
	if result, err := service.simulateAcceptanceScenarios(t.Context(), plan, []appschemamodel.ApplicationDefinitionMutation{ignored, ignoredAction, createMutation}, principal); err != nil || !result.Passed {
		t.Fatalf("create action candidate result=%#v err=%v", result, err)
	}
	for _, scenarios := range [][]changeplanmodel.BusinessAcceptanceScenario{
		{{Key: " ", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve"}},
		{{Key: "same", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve"}, {Key: "same", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve"}},
	} {
		plan.AcceptanceScenarios = scenarios
		if _, err := service.simulateAcceptanceScenarios(t.Context(), plan, []appschemamodel.ApplicationDefinitionMutation{mutation}, principal); err == nil {
			t.Fatal("invalid scenario contract accepted")
		}
	}
	plan = changePlanScenarioConditionPlan()
	runtime.err = errors.New("simulate")
	if _, err := service.simulateAcceptanceScenarios(t.Context(), plan, []appschemamodel.ApplicationDefinitionMutation{mutation}, principal); err == nil {
		t.Fatal("runtime error ignored")
	}
}

func TestChangePlanScenarioStringHelpersCoverEmptyDuplicateLengthAndValueMismatch(t *testing.T) {
	if got := sortedUniqueStrings([]string{" b ", "", "a", "a"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("sorted=%#v", got)
	}
	if equalStrings([]string{"a"}, nil) || equalStrings([]string{"a"}, []string{"b"}) || !equalStrings([]string{"a"}, []string{"a"}) {
		t.Fatal("string equality mismatch")
	}
}

func changePlanScenarioConditionPlan() changeplanmodel.BusinessSystemChangePlan {
	return changeplanmodel.BusinessSystemChangePlan{PlanID: "plan", DraftRevision: 1, ReleaseOrder: []string{"action"}, Items: []changeplanmodel.BusinessSystemChangeItem{{ItemID: "action", Operation: "update", ResourceType: "action", ResourceKey: "order.approve", After: json.RawMessage(`{}`)}}, AcceptanceScenarios: []changeplanmodel.BusinessAcceptanceScenario{{Key: "approve", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve", Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true}}}}
}
