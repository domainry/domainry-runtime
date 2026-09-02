package invocation

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	bindingcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/binding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActionInvocationClosesRequiredUnknownTypeAndOptions(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "document.record", ObjectKey: "document", PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "count", Type: "integer", Required: true},
		{Key: "decision", Type: "select", Options: []string{"approved", "rejected"}},
	}}
	environment := bindingcontract.NewEnvironment(bindingcontract.Fact{Reference: "$workflow.count", Type: bindingcontract.TypeInteger})
	if issues := ValidateAction(action, "document", map[string]any{"count": "$workflow.count", "decision": "approved"}, environment); len(issues) != 0 {
		t.Fatalf("valid invocation issues=%#v", issues)
	}
	issues := ValidateAction(action, "other", map[string]any{"unknown": true, "decision": "other"}, environment)
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{"invocation.action_object_mismatch", "invocation.input_required", "invocation.input_unknown", "invocation.input_option_invalid"} {
		if !codes[code] {
			t.Fatalf("missing %s in %#v", code, issues)
		}
	}
}

func TestWorkflowInvocationClosesEntryEnabledAndInputContract(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "document.review", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, InputFields: []definitionmodel.WorkflowInputField{{Key: "comment", Type: "text", Required: true}}}
	if issues := ValidateWorkflow(workflow, WorkflowEntryAgent, map[string]any{"comment": "ready", "agent_interactive_run_id": "run"}, nil); len(issues) != 0 {
		t.Fatalf("valid Agent entry issues=%#v", issues)
	}
	workflow.Enabled = false
	issues := ValidateWorkflow(workflow, WorkflowEntryIntegrationEvent, map[string]any{"unknown": "value"}, nil)
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{"invocation.workflow_disabled", "invocation.workflow_entry_mode_invalid", "invocation.input_required", "invocation.input_unknown"} {
		if !codes[code] {
			t.Fatalf("missing %s in %#v", code, issues)
		}
	}
}

func TestInvocationUsesFiniteProducerValuesForSelectInputs(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "document.record", ObjectKey: "document", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "decision", Type: "select", Options: []string{"rejected"}}}}
	bindings := bindingcontract.NewEnvironment(bindingcontract.Fact{Reference: "$workflow.approval_decision", Type: bindingcontract.TypeText, Values: []string{"rejected"}})
	if issues := ValidateAction(action, "document", map[string]any{"decision": "$workflow.approval_decision"}, bindings); len(issues) != 0 {
		t.Fatalf("branch-bounded approval decision issues=%#v", issues)
	}
	bindings = bindingcontract.NewEnvironment(bindingcontract.Fact{Reference: "$workflow.approval_decision", Type: bindingcontract.TypeText, Values: []string{"approved", "rejected"}})
	issues := ValidateAction(action, "document", map[string]any{"decision": "$workflow.approval_decision"}, bindings)
	if len(issues) != 1 || issues[0].Code != "invocation.input_option_invalid" {
		t.Fatalf("unbounded branch value domain issues=%#v", issues)
	}
}

func TestWorkflowReservedInputRejectsUnknownReference(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "document.review", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}}
	issues := ValidateWorkflow(workflow, WorkflowEntryAgent, map[string]any{"record_id": "$workflow.missing"}, nil)
	if len(issues) != 1 || issues[0].Code != "invocation.input_reference_unknown" {
		t.Fatalf("reserved unknown reference issues=%#v", issues)
	}
}

func TestWorkflowReservedInputsForTriggerMatchRuntimeEntryContracts(t *testing.T) {
	tests := []struct {
		trigger string
		want    string
	}{
		{trigger: "manual", want: "request_id"},
		{trigger: "record_created", want: "request_id"},
		{trigger: "scheduled", want: "scheduled_at"},
		{trigger: "integration_event", want: "integration_event_id"},
		{trigger: "action_completed", want: "request_id"},
	}
	for _, test := range tests {
		if _, ok := WorkflowReservedInputsForTrigger(test.trigger)[test.want]; !ok {
			t.Fatalf("trigger %q does not publish Runtime input %q", test.trigger, test.want)
		}
	}
	if _, ok := WorkflowReservedInputsForTrigger("manual")["agent_interactive_run_id"]; ok {
		t.Fatal("manual authoring contract leaked Agent-only input")
	}
}

func TestStringBackedIdentityAndTemporalLiteralsMatchRuntimeValues(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "document.assign", ObjectKey: "document", PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "owner_id", Type: "user"},
		{Key: "record_id", Type: "relation"},
		{Key: "due_on", Type: "date"},
		{Key: "due_at", Type: "datetime"},
	}}
	input := map[string]any{"owner_id": "user-1", "record_id": "record-1", "due_on": "2026-08-23", "due_at": "2026-08-23T10:00:00Z"}
	if issues := ValidateAction(action, "document", input, nil); len(issues) != 0 {
		t.Fatalf("Runtime string-backed literal issues=%#v", issues)
	}
	workflow := definitionmodel.WorkflowSchema{Key: "document.review", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}}
	if issues := ValidateWorkflow(workflow, WorkflowEntryManual, map[string]any{"record_id": "record-1", "initiating_user_id": "user-1"}, nil); len(issues) != 0 {
		t.Fatalf("reserved Runtime string-backed literal issues=%#v", issues)
	}
}

func TestSharedInvocationPermissionContractFailsClosed(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key:       "document.reject",
		ObjectKey: "document",
	}
	workflow := definitionmodel.WorkflowSchema{Key: "document.review"}
	unknown := principalmodel.Principal{}
	denied := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Key: "reader", Permissions: []string{"document.read"}})
	allowed := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Key: "reviewer", Permissions: []string{"document.reject", "workflow.document.review.run"}})
	sibling := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Key: "other-workflow", Permissions: []string{"workflow.document.publish.run"}})

	for name, principal := range map[string]principalmodel.Principal{"unknown": unknown, "denied": denied} {
		if issues := ValidateActionPermission(action, principal); len(issues) != 1 || issues[0].Code != "invocation.action_permission_denied" {
			t.Fatalf("%s Action permission issues=%#v", name, issues)
		}
		if issues := ValidateWorkflowPermission(workflow, principal); len(issues) != 1 || issues[0].Code != "invocation.workflow_permission_denied" {
			t.Fatalf("%s Workflow permission issues=%#v", name, issues)
		}
	}
	if issues := ValidateActionPermission(action, allowed); len(issues) != 0 {
		t.Fatalf("allowed Action permission issues=%#v", issues)
	}
	if issues := ValidateWorkflowPermission(workflow, allowed); len(issues) != 0 {
		t.Fatalf("allowed Workflow permission issues=%#v", issues)
	}
	if issues := ValidateWorkflowPermission(workflow, sibling); len(issues) != 1 || issues[0].Expected != "workflow.document.review.run" {
		t.Fatalf("sibling Workflow Action must not authorize this Workflow: %#v", issues)
	}
}
