package changeplan

import (
	"context"
	"encoding/json"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type changePlanScenarioRuntimeFake struct {
	result      AcceptanceScenarioRuntimeResult
	results     []AcceptanceScenarioRuntimeResult
	resourceKey string
	request     metadatamodel.MetadataDefinitionUpsertRequest
	input       map[string]any
	record      map[string]any
	calls       int
	err         error
}

func (f *changePlanScenarioRuntimeFake) SimulateActionCandidate(_ context.Context, resourceKey string, request metadatamodel.MetadataDefinitionUpsertRequest, input, record map[string]any, _ principalmodel.Principal) (AcceptanceScenarioRuntimeResult, error) {
	f.calls++
	f.resourceKey, f.request, f.input, f.record = resourceKey, request, input, record
	if len(f.results) > 0 {
		result := f.results[0]
		f.results = f.results[1:]
		return result, f.err
	}
	return f.result, f.err
}

func TestChangePlanSimulatesTypedAcceptanceScenariosAgainstComposedCandidate(t *testing.T) {
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanID: "candidate-plan", DraftRevision: 4,
		ReleaseOrder: []string{"object", "action"},
		Items: []changeplanmodel.BusinessSystemChangeItem{
			{ItemID: "object", Operation: "create", ResourceType: "object", ResourceKey: "order", After: json.RawMessage(`{"key":"order","name":"Order"}`)},
			{ItemID: "action", Operation: "create", ResourceType: "action", ResourceKey: "order.approve", After: json.RawMessage(`{"key":"order.approve","object_key":"order","kind":"record_operation","config":{"steps":[]}}`)},
		},
		AcceptanceScenarios: []changeplanmodel.BusinessAcceptanceScenario{{
			Key: "approve-order", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve",
			Input: map[string]any{"reason": "verified"}, Record: map[string]any{"id": "order-1"},
			Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true},
		}},
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	repository := &changePlanDraftRepositoryFake{found: true, draft: changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "workspace-1", PlanID: plan.PlanID, Revision: 4, Status: "draft", Payload: payload}}
	metadataRuntime := &changePlanRuntimeFake{}
	scenarioRuntime := &changePlanScenarioRuntimeFake{result: AcceptanceScenarioRuntimeResult{Valid: true, SideEffectFree: true, Payload: []byte(`{"valid":true,"side_effect_free":true,"execution_performed":false}`)}}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, metadataRuntime, scenarioRuntime)

	result, err := service.SimulateDraftScenarios(t.Context(), plan.PlanID, 4, changePlanAdmin())
	if err != nil || !result.Passed || !result.SideEffectFree || result.DraftRevision != 4 || len(result.Results) != 1 || !result.Results[0].Passed {
		t.Fatalf("simulation=%#v err=%v", result, err)
	}
	if len(metadataRuntime.candidate) != 2 || scenarioRuntime.calls != 1 || scenarioRuntime.resourceKey != "order.approve" || scenarioRuntime.input["reason"] != "verified" || scenarioRuntime.record["id"] != "order-1" {
		t.Fatalf("candidate=%#v runtime=%#v", metadataRuntime.candidate, scenarioRuntime)
	}
	if string(scenarioRuntime.request.Payload) != string(plan.Items[1].After) {
		t.Fatalf("candidate action payload=%s", scenarioRuntime.request.Payload)
	}

	scenarioRuntime.result = AcceptanceScenarioRuntimeResult{Valid: false, SideEffectFree: true, ErrorCodes: []string{"backend.validation.required"}, Payload: []byte(`{"valid":false}`)}
	result, err = service.SimulateDraftScenarios(t.Context(), plan.PlanID, 4, changePlanAdmin())
	if err != nil || result.Passed || result.Results[0].Passed || result.Results[0].ActualErrorCodes[0] != "backend.validation.required" {
		t.Fatalf("failed expectation simulation=%#v err=%v", result, err)
	}
}

func TestChangePlanAcceptanceScenarioContractRejectsAmbiguousTargets(t *testing.T) {
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanID: "candidate-plan", DraftRevision: 1, ReleaseOrder: []string{}, Items: []changeplanmodel.BusinessSystemChangeItem{},
		AcceptanceScenarios: []changeplanmodel.BusinessAcceptanceScenario{{Key: "scenario", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "missing", Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true}}},
	}
	payload, _ := json.Marshal(plan)
	repository := &changePlanDraftRepositoryFake{found: true, draft: changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 1, Status: "draft", Payload: payload}}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, &changePlanRuntimeFake{}, &changePlanScenarioRuntimeFake{})
	if _, err := service.SimulateDraftScenarios(t.Context(), plan.PlanID, 1, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.scenario_resource_not_in_candidate" {
		t.Fatalf("missing target error=%v", err)
	}
	plan.AcceptanceScenarios[0].Kind = "custom_script"
	payload, _ = json.Marshal(plan)
	repository.draft.Payload = payload
	if _, err := service.SimulateDraftScenarios(t.Context(), plan.PlanID, 1, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.scenario_kind_unsupported" {
		t.Fatalf("unsupported kind error=%v", err)
	}
}
