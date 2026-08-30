package runtimehost

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type connectorBindingExecution struct{}
type connectorBindingLease struct{}

func (connectorBindingLease) Release() {}
func (connectorBindingExecution) Identity() runtimeext.ExecutionIdentity {
	return runtimeext.ExecutionIdentity{ActionKey: "group_class.book_class"}
}
func (connectorBindingExecution) Principal() runtimeext.Principal { return runtimeext.Principal{} }
func (connectorBindingExecution) Workspace() runtimeext.Workspace { return runtimeext.Workspace{} }
func (connectorBindingExecution) Phase() runtimeext.ExecutionPhase {
	return runtimeext.ExecutionPhasePrewrite
}
func (connectorBindingExecution) QueryRecords(context.Context, runtimeext.RecordQuery) (runtimeext.RecordQueryResult, error) {
	return runtimeext.RecordQueryResult{}, nil
}
func (connectorBindingExecution) ApplyRecordMutation(context.Context, runtimeext.RecordMutation) (runtimeext.RecordMutationResult, error) {
	return runtimeext.RecordMutationResult{}, nil
}
func (connectorBindingExecution) StageDurableIntent(context.Context, runtimeext.DurableIntent) (runtimeext.DurableIntentReceipt, error) {
	return runtimeext.DurableIntentReceipt{}, nil
}
func (connectorBindingExecution) AcquireSynchronousConnectorCall(runtimeext.ActionConnectorCapability) (runtimeext.SynchronousConnectorCallLease, error) {
	return connectorBindingLease{}, nil
}

type connectorBindingTarget struct {
	request ConnectorCallRequest
	result  ConnectorCallResult
}

func (t *connectorBindingTarget) Call(_ context.Context, _ runtimeext.ActionExecution, request ConnectorCallRequest) (ConnectorCallResult, error) {
	t.request = request
	return t.result, nil
}

func TestBindableConnectorGatewayMapsGeneratedRequestToRuntime(t *testing.T) {
	gateway := &bindableConnectorGateway{}
	request := ConnectorCallRequest{
		ConnectorKey: "member_center", ConnectionKey: "primary", OperationKey: "get_member",
		ContractSHA256: "contract", Mode: "call", Payload: json.RawMessage(`{"sequence":9007199254740993}`),
	}
	if _, err := gateway.Call(t.Context(), connectorBindingExecution{}, request); runtimeextErrorCode(err) != "backend.connector.gateway_unavailable" {
		t.Fatalf("unbound error=%v", err)
	}
	target := &connectorBindingTarget{result: ConnectorCallResult{Payload: json.RawMessage(`{"name":"Ada"}`)}}
	if err := gateway.bind(target); err != nil {
		t.Fatal(err)
	}
	if err := gateway.bind(target); err == nil {
		t.Fatal("duplicate Runtime gateway binding was accepted")
	}
	result, err := gateway.Call(t.Context(), connectorBindingExecution{}, request)
	if err != nil || string(result.Payload) != `{"name":"Ada"}` {
		t.Fatalf("result=%s error=%v", result.Payload, err)
	}
	if target.request.ConnectorKey != request.ConnectorKey || target.request.ConnectionKey != request.ConnectionKey || target.request.OperationKey != request.OperationKey || target.request.ContractSHA256 != request.ContractSHA256 || target.request.Mode != request.Mode || string(target.request.Payload) != string(request.Payload) {
		t.Fatalf("mapped request=%#v", target.request)
	}
	gateway.unbind()
	if _, err := gateway.Call(t.Context(), connectorBindingExecution{}, request); runtimeextErrorCode(err) != "backend.connector.gateway_unavailable" {
		t.Fatalf("unbound-after-close error=%v", err)
	}
}

func TestBindableConnectorGatewayRejectsInvalidGeneratedInput(t *testing.T) {
	gateway := &bindableConnectorGateway{}
	if err := gateway.bind(&connectorBindingTarget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.Call(t.Context(), nil, ConnectorCallRequest{Payload: json.RawMessage(`{}`)}); runtimeextErrorCode(err) != runtimeext.ConnectorActionExecutionRequiredErrorCode {
		t.Fatalf("nil execution error=%v", err)
	}
	for _, payload := range []json.RawMessage{nil, json.RawMessage(`[]`), json.RawMessage(`{} {}`)} {
		if _, err := gateway.Call(t.Context(), connectorBindingExecution{}, ConnectorCallRequest{Payload: payload}); runtimeextErrorCode(err) != "backend.connector.request_invalid" {
			t.Fatalf("payload=%s error=%v", payload, err)
		}
	}
}

func runtimeextErrorCode(err error) string {
	if business, ok := err.(*runtimeext.BusinessError); ok {
		return business.Code
	}
	return ""
}
