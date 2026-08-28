package policy

import (
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestWorkflowAuthoringFragmentExamplesExecutePublishedOwnerPolicy(t *testing.T) {
	for _, capability := range WorkflowAuthoringDomain().Capabilities {
		if capability.Key == "workflow.definition" {
			continue
		}
		if capability.ValidationEndpoint != "POST /workflows/authoring-fragments/{capabilityKey}/validate" {
			t.Fatalf("capability %s validation endpoint=%q", capability.Key, capability.ValidationEndpoint)
		}
		for _, example := range capability.Examples {
			err := WorkflowValidateAuthoringFragment(capability.Key, example.Value)
			code := apperror.CodeOf(err)
			if len(example.ExpectedErrorCodes) == 0 && err != nil {
				t.Fatalf("capability=%s example=%s err=%v", capability.Key, example.Name, err)
			}
			if len(example.ExpectedErrorCodes) > 0 && code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%s want=%s", capability.Key, example.Name, code, example.ExpectedErrorCodes[0])
			}
		}
	}
}

func TestWorkflowAuthoringFragmentRejectsUnknownCapability(t *testing.T) {
	if code := apperror.CodeOf(WorkflowValidateAuthoringFragment("workflow.unknown", map[string]any{})); code != "backend.workflow.authoring_capability_unsupported" {
		t.Fatalf("code=%s", code)
	}
}

func TestWorkflowAuthoringFragmentRejectsUndecodablePayloadForEveryOwnerCapability(t *testing.T) {
	invalid := map[string]any{"invalid": func() {}}
	for _, capabilityKey := range []string{
		"workflow.graph_v2",
		"workflow.trigger_contract",
		"workflow.condition_contract",
		"workflow.assignee_resolver",
		"workflow.node.approval",
		"workflow.node.action",
		"workflow.node.cc",
		"workflow.node.timer",
		"workflow.graph_edge",
	} {
		if code := apperror.CodeOf(WorkflowValidateAuthoringFragment(capabilityKey, invalid)); code != "backend.workflow.authoring_fragment_invalid" {
			t.Fatalf("capability=%s code=%s", capabilityKey, code)
		}
	}

	target := struct {
		Version int `json:"version"`
	}{}
	if code := apperror.CodeOf(workflowDecodeAuthoringFragment(map[string]any{"version": "not-an-integer"}, &target)); code != "backend.workflow.authoring_fragment_invalid" {
		t.Fatalf("unmarshal code=%s", code)
	}
}
