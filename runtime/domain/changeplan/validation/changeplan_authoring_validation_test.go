package validation_test

import (
	"encoding/json"
	"reflect"
	"testing"

	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanvalidation "github.com/domainry/domainry-runtime/runtime/domain/changeplan/validation"
)

func TestChangePlanAuthoringEnumsShareOwnerModelWithValidator(t *testing.T) {
	definition := changeplancontract.ChangePlanValidationAuthoringCapability()
	item := definition.InputSchema.Definitions["business_system_change_item"]
	enums := map[string][]string{"operation": schemaStringEnum(item.Properties["operation"].Enum), "change_kind": schemaStringEnum(item.Properties["change_kind"].Enum), "risk_level": schemaStringEnum(item.Properties["risk_level"].Enum), "resource_owner": schemaStringEnum(item.Properties["resource_owner"].Enum)}
	checks := map[string]struct {
		contract  []string
		model     []string
		validator []string
	}{
		"operation":      {enums["operation"], changeplanmodel.BusinessChangeOperations(), changeplanvalidation.RuntimeBusinessChangeOperations()},
		"change_kind":    {enums["change_kind"], changeplanmodel.BusinessChangeKinds(), changeplanvalidation.RuntimeBusinessChangeKinds()},
		"risk_level":     {enums["risk_level"], changeplanmodel.BusinessChangeRiskLevels(), changeplanvalidation.RuntimeBusinessChangeRiskLevels()},
		"resource_owner": {enums["resource_owner"], changeplanmodel.BusinessResourceOwners(), changeplanvalidation.RuntimeBusinessResourceOwners()},
	}
	for name, check := range checks {
		if !reflect.DeepEqual(check.contract, check.model) || !reflect.DeepEqual(check.model, check.validator) {
			t.Fatalf("%s enum drifted: contract=%v model=%v validator=%v", name, check.contract, check.model, check.validator)
		}
	}
	rollback := changeplancontract.ChangePlanRollbackPolicyAuthoringCapability()
	if len(rollback.Parameters) != 0 || !reflect.DeepEqual(changeplanmodel.BusinessRollbackStrategies(), []string{"append_only_version", "compensating_change_plan", "manual_compensation", "new_published_version"}) {
		t.Fatalf("rollback policy input or owner strategies drifted: %#v", rollback)
	}
}

func TestChangePlanApplyContractMatchesHTTPEnvelope(t *testing.T) {
	capability := changeplancontract.ChangePlanApplyAuthoringCapability()
	if capability.OutputSchema == nil || len(capability.OutputSchema.Required) != 2 || capability.OutputVariables[0].JSONPointer != "/result/plan_id" || capability.OutputVariables[1].JSONPointer != "/result/schema_hash" {
		t.Fatalf("apply output contract=%#v variables=%#v", capability.OutputSchema, capability.OutputVariables)
	}
	if _, ok := capability.OutputSchema.Properties["result"]; !ok {
		t.Fatal("apply output schema omitted result envelope")
	}
	if _, ok := capability.OutputSchema.Properties["current_snapshot"]; !ok {
		t.Fatal("apply output schema omitted current_snapshot envelope")
	}
}

func TestChangePlanValidationExamplesExecuteOwnerValidator(t *testing.T) {
	definition := changeplancontract.ChangePlanValidationAuthoringCapability()
	for _, example := range definition.Examples {
		raw, err := json.Marshal(example.Value)
		if err != nil {
			t.Fatalf("marshal %s: %v", example.Name, err)
		}
		var plan changeplanmodel.BusinessSystemChangePlan
		if err := json.Unmarshal(raw, &plan); err != nil {
			t.Fatalf("decode %s: %v", example.Name, err)
		}
		snapshot := changeplanmodel.Snapshot{SnapshotHash: "$instance.snapshot_hash", RuntimeVersion: "$runtime.version", AuthoringContractVersion: "$contract.version", AuthoringContractHash: "$contract.hash", CapabilityKeys: []string{"schema.field"}}
		graph := changeplanmodel.ReferenceGraph{Hash: "$instance.reference_graph_hash"}
		result := changeplanvalidation.ValidateBusinessSystemChangePlan(plan, snapshot, graph)
		if example.Name == "invalid_with_repair" {
			if !changePlanValidationHasCode(result, example.ExpectedErrorCodes[0]) {
				t.Fatalf("%s did not produce %s: %#v", example.Name, example.ExpectedErrorCodes[0], result.Issues)
			}
		} else if !result.Valid {
			t.Fatalf("%s rejected by owner validator: %#v", example.Name, result.Issues)
		}
	}
}

func changePlanValidationHasCode(result changeplanmodel.BusinessChangePlanValidation, code string) bool {
	for _, issue := range result.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func schemaStringEnum(values []any) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.(string))
	}
	return result
}
