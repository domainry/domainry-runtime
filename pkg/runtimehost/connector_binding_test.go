package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
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

type integrationConnectorLease struct {
	requestID string
	released  bool
}

func (lease *integrationConnectorLease) ConnectorRequestID() string { return lease.requestID }
func (lease *integrationConnectorLease) Release()                   { lease.released = true }

type integrationConnectorExecution struct {
	identity   runtimeext.ExecutionIdentity
	principal  runtimeext.Principal
	workspace  runtimeext.Workspace
	lease      *integrationConnectorLease
	capability runtimeext.ActionConnectorCapability
	acquireErr error
}

func (execution *integrationConnectorExecution) Identity() runtimeext.ExecutionIdentity {
	return execution.identity
}
func (execution *integrationConnectorExecution) Principal() runtimeext.Principal {
	return execution.principal
}
func (execution *integrationConnectorExecution) Workspace() runtimeext.Workspace {
	return execution.workspace
}
func (*integrationConnectorExecution) Phase() runtimeext.ExecutionPhase {
	return runtimeext.ExecutionPhasePrewrite
}
func (*integrationConnectorExecution) QueryRecords(context.Context, runtimeext.RecordQuery) (runtimeext.RecordQueryResult, error) {
	return runtimeext.RecordQueryResult{}, nil
}
func (*integrationConnectorExecution) ApplyRecordMutation(context.Context, runtimeext.RecordMutation) (runtimeext.RecordMutationResult, error) {
	return runtimeext.RecordMutationResult{}, nil
}
func (*integrationConnectorExecution) StageDurableIntent(context.Context, runtimeext.DurableIntent) (runtimeext.DurableIntentReceipt, error) {
	return runtimeext.DurableIntentReceipt{}, nil
}
func (execution *integrationConnectorExecution) AcquireSynchronousConnectorCall(capability runtimeext.ActionConnectorCapability) (runtimeext.SynchronousConnectorCallLease, error) {
	execution.capability = capability
	if execution.acquireErr != nil {
		return nil, execution.acquireErr
	}
	return execution.lease, nil
}

type integrationOperationsProbe struct {
	integrationsdk.Operations
	request integrationsdk.ProviderCallRequest
	result  integrationsdk.ProviderCallResult
	err     error
	calls   int
}

func (probe *integrationOperationsProbe) Call(_ context.Context, request integrationsdk.ProviderCallRequest) (integrationsdk.ProviderCallResult, error) {
	probe.calls++
	probe.request = request
	return probe.result, probe.err
}

func TestIntegrationRuntimeConnectorGatewayUsesOwnerOperationAndLease(t *testing.T) {
	probe := &integrationOperationsProbe{result: integrationsdk.ProviderCallResult{Response: json.RawMessage(`{"text":"receipt"}`)}}
	lease := &integrationConnectorLease{requestID: "execution-1:connector:3"}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "execution-1", ActionKey: "invoice_ocr_job.process"},
		principal: runtimeext.Principal{UserID: "worker-1", RoleKey: "ocr_worker"},
		workspace: runtimeext.Workspace{ID: "workspace-a"}, lease: lease,
	}
	request := ConnectorCallRequest{
		ConnectorKey: "expense_ocr", ConnectionKey: "primary", OperationKey: "parse_expense",
		ContractSHA256: strings.Repeat("a", 64), Mode: "call", Effect: "write", Payload: json.RawMessage(`{"document":"base64"}`),
	}
	result, err := (integrationRuntimeConnectorGateway{operations: probe}).Call(t.Context(), execution, request)
	if err != nil || string(result.Payload) != `{"text":"receipt"}` {
		t.Fatalf("result=%s error=%v", result.Payload, err)
	}
	if !lease.released || probe.calls != 1 {
		t.Fatalf("released=%t calls=%d", lease.released, probe.calls)
	}
	if execution.capability.ConnectorKey != "expense_ocr" || execution.capability.ConnectionKey != "primary" || execution.capability.OperationKey != "parse_expense" || execution.capability.ContractSHA256 != strings.Repeat("a", 64) || execution.capability.Mode != runtimeext.ConnectorModeCall || execution.capability.Effect != runtimeext.ConnectorEffectWrite {
		t.Fatalf("capability=%+v", execution.capability)
	}
	got := probe.request
	if got.RequestID != lease.requestID || got.WorkspaceID != "workspace-a" || got.ConnectorKey != "expense_ocr" || got.ConnectionKey != "primary" || got.Operation != "parse_expense" || got.ActorID != "worker-1" || got.RoleKey != "ocr_worker" || got.PersistenceMode != integrationsdk.ProviderCallPersistenceSensitive || got.MaskedDestination != "expense_ocr/parse_expense" || string(got.Payload) != string(request.Payload) {
		t.Fatalf("provider request=%+v", got)
	}
}

func TestIntegrationRuntimeConnectorGatewayKeepsReadEvidenceAndReleasesOnFailure(t *testing.T) {
	want := errors.New("provider failed")
	probe := &integrationOperationsProbe{err: want}
	lease := &integrationConnectorLease{}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "execution-2", ActionKey: "member.lookup"},
		workspace: runtimeext.Workspace{ID: "workspace-b"}, lease: lease,
	}
	request := ConnectorCallRequest{
		ConnectorKey: "directory", ConnectionKey: "primary", OperationKey: "lookup",
		ContractSHA256: strings.Repeat("b", 64), Mode: "call", Effect: "read", Payload: json.RawMessage(`{}`),
	}
	_, err := (integrationRuntimeConnectorGateway{operations: probe}).Call(t.Context(), execution, request)
	if !errors.Is(err, want) || !lease.released {
		t.Fatalf("error=%v released=%t", err, lease.released)
	}
	if probe.request.RequestID != "execution-2" || probe.request.PersistenceMode != integrationsdk.ProviderCallPersistenceStandard || probe.request.MaskedDestination != "" {
		t.Fatalf("provider request=%+v", probe.request)
	}
}

func TestIntegrationRuntimeConnectorGatewayFailsClosedBeforeOwnerCall(t *testing.T) {
	request := ConnectorCallRequest{ConnectorKey: "directory", ConnectionKey: "primary", OperationKey: "lookup", ContractSHA256: strings.Repeat("c", 64), Mode: "call", Effect: "read", Payload: json.RawMessage(`{}`)}
	execution := &integrationConnectorExecution{identity: runtimeext.ExecutionIdentity{ExecutionID: "execution-3", ActionKey: "member.lookup"}, workspace: runtimeext.Workspace{ID: "workspace-c"}, lease: &integrationConnectorLease{}}
	if _, err := (integrationRuntimeConnectorGateway{}).Call(t.Context(), execution, request); runtimeextErrorCode(err) != "backend.connector.gateway_unavailable" {
		t.Fatalf("missing Operations error=%v", err)
	}
	probe := &integrationOperationsProbe{}
	execution.identity = runtimeext.ExecutionIdentity{}
	if _, err := (integrationRuntimeConnectorGateway{operations: probe}).Call(t.Context(), execution, request); runtimeextErrorCode(err) != "backend.connector.execution_identity_invalid" || probe.calls != 0 {
		t.Fatalf("invalid identity error=%v calls=%d", err, probe.calls)
	}
	execution.identity = runtimeext.ExecutionIdentity{ExecutionID: "execution-3", ActionKey: "member.lookup"}
	execution.acquireErr = errors.New("grant denied")
	if _, err := (integrationRuntimeConnectorGateway{operations: probe}).Call(t.Context(), execution, request); !errors.Is(err, execution.acquireErr) || probe.calls != 0 {
		t.Fatalf("grant error=%v calls=%d", err, probe.calls)
	}
}
