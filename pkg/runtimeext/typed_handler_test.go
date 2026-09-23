package runtimeext

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type typedHandlerExecution struct{ actionKey string }

func (e typedHandlerExecution) Identity() ExecutionIdentity {
	return ExecutionIdentity{ActionKey: e.actionKey, ObjectKey: "order", RecordID: "order-one"}
}
func (typedHandlerExecution) Principal() Principal  { return Principal{Known: true} }
func (typedHandlerExecution) Workspace() Workspace  { return Workspace{ID: "workspace"} }
func (typedHandlerExecution) Phase() ExecutionPhase { return ExecutionPhasePrewrite }
func (typedHandlerExecution) QueryRecords(context.Context, RecordQuery) (RecordQueryResult, error) {
	return RecordQueryResult{}, nil
}
func (typedHandlerExecution) ApplyRecordMutation(context.Context, RecordMutation) (RecordMutationResult, error) {
	return RecordMutationResult{}, nil
}
func (typedHandlerExecution) StageDurableIntent(context.Context, DurableIntent) (DurableIntentReceipt, error) {
	return DurableIntentReceipt{}, nil
}
func (typedHandlerExecution) AcquireSynchronousConnectorCall(ActionConnectorCapability) (SynchronousConnectorCallLease, error) {
	return nil, nil
}

func typedHandlerDescriptor() HandlerDescriptor {
	return HandlerDescriptor{
		ActionKey: "order.approve", InputType: "example.com/project.ApproveInput", OutputType: "example.com/project.ApproveOutput",
		HandlerRevision: "one", ObjectCapabilities: []ActionObjectCapability{{ObjectKey: "order", Operations: []string{"update"}}},
	}
}

func TestTypedBusinessHandlerStrictlyDecodesAndEncodes(t *testing.T) {
	type input struct {
		Note string `json:"note"`
	}
	type output struct {
		Status string `json:"status"`
	}
	handler, err := NewTypedBusinessHandler(typedHandlerDescriptor(), func(execution ActionExecution) (string, error) {
		return execution.Identity().RecordID, nil
	}, Handler[string, input, output](func(_ context.Context, recordID string, value input) (output, error) {
		return output{Status: recordID + ":" + value.Note}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler.Invoke(t.Context(), typedHandlerExecution{actionKey: "order.approve"}, json.RawMessage(`{"note":"ok"}`))
	if err != nil || string(result) != `{"status":"order-one:ok"}` {
		t.Fatalf("result=%s err=%v", result, err)
	}
	native, ok := handler.(NativeBusinessHandler)
	if !ok {
		t.Fatal("typed handler does not expose native invocation")
	}
	result, accepted, err := native.InvokeNative(t.Context(), typedHandlerExecution{actionKey: "order.approve"}, input{Note: "direct"})
	if err != nil || !accepted || string(result) != `{"status":"order-one:direct"}` {
		t.Fatalf("native result=%s accepted=%v err=%v", result, accepted, err)
	}
	if _, accepted, err := native.InvokeNative(t.Context(), typedHandlerExecution{actionKey: "order.approve"}, struct{ Note string }{Note: "wrong type"}); err != nil || accepted {
		t.Fatalf("mismatched native input accepted=%v err=%v", accepted, err)
	}
	for _, payload := range []string{`{"unknown":true}`, `{"note":"ok"}{}`} {
		_, err := handler.Invoke(t.Context(), typedHandlerExecution{actionKey: "order.approve"}, json.RawMessage(payload))
		var business *BusinessError
		if !errors.As(err, &business) || business.Code != "backend.action.input_invalid" {
			t.Fatalf("payload=%q error=%#v", payload, err)
		}
	}
	if _, err := handler.Invoke(t.Context(), typedHandlerExecution{actionKey: "other"}, nil); err == nil || !strings.Contains(err.Error(), "execution identity") {
		t.Fatalf("identity error=%v", err)
	}
}

func TestTypedBusinessHandlerRejectsIncompleteConstruction(t *testing.T) {
	type empty struct{}
	descriptor := typedHandlerDescriptor()
	handler := Handler[empty, empty, empty](func(context.Context, empty, empty) (empty, error) { return empty{}, nil })
	if _, err := NewTypedBusinessHandler(descriptor, CapabilityFactory[empty](nil), handler); !errors.Is(err, ErrHandlerCapabilityFactoryNeeded) {
		t.Fatalf("factory error=%v", err)
	}
	if _, err := NewTypedBusinessHandler(descriptor, func(ActionExecution) (empty, error) { return empty{}, nil }, Handler[empty, empty, empty](nil)); !errors.Is(err, ErrTypedHandlerRequired) {
		t.Fatalf("handler error=%v", err)
	}
}
