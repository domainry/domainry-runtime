package automation

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"context"
	"errors"
	"fmt"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestInstructionDispatcherInvokesBusinessActionAndMergesObjectOutput(t *testing.T) {
	payload := map[string]any{"source": "automation"}
	dispatcher := NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{
		InvokeAction: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			if invocation.ActionKey != "order.normalize" || invocation.ObjectKey != "order" || invocation.Source != actionmodel.ActionSourceAutomation || invocation.RequestID != "request-1" || invocation.Input["extra"] != true {
				t.Fatalf("unexpected invocation: %#v", invocation)
			}
			if _, leaked := invocation.Input["source"]; leaked {
				t.Fatalf("explicit Action input must not receive unrelated record payload: %#v", invocation.Input)
			}
			return actionmodel.ActionInvocationResult{InvocationID: "inv-1", Output: map[string]any{"data": map[string]any{"normalized": true}}, OutboxIDs: []string{"outbox-1"}}, nil
		},
	})
	result, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{
		Key: "normalize", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before"},
	}, automationmodel.AutomationInstructionSchema{
		Key: "normalize", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.normalize", "input": map[string]any{"extra": true}},
	}, renderContext(payload), nil, principalmodel.Principal{RequestID: "request-1"})
	if err != nil || result.InvocationID != "inv-1" || len(result.OutboxIDs) != 1 || payload["normalized"] != true {
		t.Fatalf("unexpected dispatch result=%#v payload=%#v err=%v", result, payload, err)
	}
}

func TestInstructionDispatcherBusinessActionFailureAndRecordSelection(t *testing.T) {
	want := errors.New("invoke failed")
	renderFailure := renderContext(map[string]any{})
	renderFailure.RenderOptionalData = func(any) (map[string]any, error) { return nil, want }
	instruction := automationmodel.AutomationInstructionSchema{Key: "invoke", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.run", "input": map[string]any{}}}
	dispatcher := NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{InvokeAction: func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
		return actionmodel.ActionInvocationResult{}, want
	}})
	if _, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{ObjectKey: "order"}, instruction, renderFailure, nil, principalmodel.Principal{}); !errors.Is(err, want) {
		t.Fatalf("render error=%v", err)
	}
	render := renderContext(map[string]any{})
	record := &recordmodel.Record{ID: "order-1"}
	if _, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}}, instruction, render, record, principalmodel.Principal{}); !errors.Is(err, want) {
		t.Fatalf("invoke error=%v", err)
	}
	dispatcher.dependencies.InvokeAction = func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
		if invocation.RecordID != "" {
			t.Fatalf("before phase inherited record id=%q", invocation.RecordID)
		}
		return actionmodel.ActionInvocationResult{Output: map[string]any{"data": map[string]any{"merged": true}}}, nil
	}
	if result, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before"}}, instruction, render, record, principalmodel.Principal{}); err != nil || result.Status != "success" || render.Payload["merged"] != true {
		t.Fatalf("before result=%+v payload=%v err=%v", result, render.Payload, err)
	}
	instruction.Config["record_id"] = "order-explicit"
	dispatcher.dependencies.InvokeAction = func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
		if invocation.RecordID != "order-explicit" {
			t.Fatalf("explicit record id=%q", invocation.RecordID)
		}
		return actionmodel.ActionInvocationResult{Output: map[string]any{}}, nil
	}
	if result, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}}, instruction, render, record, principalmodel.Principal{}); err != nil || result.Status != "success" {
		t.Fatalf("explicit record result=%+v err=%v", result, err)
	}
}

func TestInstructionDispatcherLeavesActionInputEmptyWhenNotConfigured(t *testing.T) {
	dispatcher := NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{
		InvokeAction: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			if len(invocation.Input) != 0 {
				t.Fatalf("unconfigured Action input leaked the record payload: %#v", invocation.Input)
			}
			return actionmodel.ActionInvocationResult{}, nil
		},
	})
	_, err := dispatcher.Execute(
		t.Context(),
		automationmodel.AutomationRuleSchema{ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}},
		automationmodel.AutomationInstructionSchema{
			Key: "invoke", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.run"},
		},
		renderContext(map[string]any{"source": "record-payload"}),
		&recordmodel.Record{ID: "order-1"},
		principalmodel.Principal{},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstructionDispatcherReturnsStructuredMissingActionError(t *testing.T) {
	result, err := NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{}).Execute(t.Context(), automationmodel.AutomationRuleSchema{ObjectKey: "order"}, automationmodel.AutomationInstructionSchema{
		Key: "invoke", Type: "invoke_business_action", Config: map[string]any{},
	}, renderContext(map[string]any{}), nil, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}})
	assertAutomationError(t, err, apperror.KindBadRequest, "backend.automation.business_action_key_required")
	if result.Status != "failed" || result.ErrorCode != "backend.automation.business_action_key_required" {
		t.Fatalf("unexpected failed result: %#v", result)
	}
}

func renderContext(payload map[string]any) AutomationInstructionRenderContext {
	return AutomationInstructionRenderContext{
		Payload: payload,
		RenderString: func(value any) string {
			if value == nil {
				return ""
			}
			return fmt.Sprint(value)
		},
		RenderOptionalData: func(value any) (map[string]any, error) {
			if value == nil {
				return map[string]any{}, nil
			}
			if data, ok := value.(map[string]any); ok {
				return data, nil
			}
			return map[string]any{}, nil
		},
	}
}
