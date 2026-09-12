package runtimehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type runtimeConnectorGateway interface {
	Call(context.Context, runtimeext.ActionExecution, ConnectorCallRequest) (ConnectorCallResult, error)
}

type bindableConnectorGateway struct {
	mu     sync.RWMutex
	target runtimeConnectorGateway
}

func (g *bindableConnectorGateway) bind(target runtimeConnectorGateway) error {
	if target == nil {
		return errors.New("Runtime Connector gateway is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.target != nil {
		return errors.New("Runtime Connector gateway is already bound")
	}
	g.target = target
	return nil
}

func (g *bindableConnectorGateway) unbind() {
	g.mu.Lock()
	g.target = nil
	g.mu.Unlock()
}

func (g *bindableConnectorGateway) Call(ctx context.Context, execution runtimeext.ActionExecution, request ConnectorCallRequest) (ConnectorCallResult, error) {
	g.mu.RLock()
	target := g.target
	g.mu.RUnlock()
	if target == nil {
		return ConnectorCallResult{}, &runtimeext.BusinessError{Code: "backend.connector.gateway_unavailable", Message: "Runtime Connector gateway is unavailable"}
	}
	if execution == nil {
		return ConnectorCallResult{}, &runtimeext.BusinessError{Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Connector ActionExecution is required"}
	}
	if _, err := decodeConnectorCallPayload(request.Payload); err != nil {
		return ConnectorCallResult{}, &runtimeext.BusinessError{Code: "backend.connector.request_invalid", Message: "Connector request payload must be one JSON object", Cause: err}
	}
	return target.Call(ctx, execution, request)
}

func decodeConnectorCallPayload(payload json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("Connector request must be an object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("Connector request must contain exactly one JSON value")
		}
		return nil, err
	}
	return result, nil
}

type unavailableRuntimeConnectorGateway struct{}

func (unavailableRuntimeConnectorGateway) Call(context.Context, runtimeext.ActionExecution, ConnectorCallRequest) (ConnectorCallResult, error) {
	return ConnectorCallResult{}, &runtimeext.BusinessError{Code: "backend.connector.gateway_unavailable", Message: "Synchronous Runtime Connector execution moved to the Integration owner"}
}

type connectorRequestIDLease interface {
	runtimeext.SynchronousConnectorCallLease
	ConnectorRequestID() string
}

type integrationRuntimeConnectorGateway struct {
	operations integrationsdk.Operations
}

func (gateway integrationRuntimeConnectorGateway) Call(ctx context.Context, execution runtimeext.ActionExecution, request ConnectorCallRequest) (ConnectorCallResult, error) {
	if gateway.operations == nil {
		return ConnectorCallResult{}, &runtimeext.BusinessError{Code: "backend.connector.gateway_unavailable", Message: "Integration Operations are unavailable"}
	}
	identity, workspace, principal := execution.Identity(), execution.Workspace(), execution.Principal()
	if !identity.Valid() || !workspace.Valid() {
		return ConnectorCallResult{}, &runtimeext.BusinessError{Code: "backend.connector.execution_identity_invalid", Message: "Connector execution identity and Workspace are required"}
	}
	capability := runtimeext.ActionConnectorCapability{
		ConnectorKey: strings.TrimSpace(request.ConnectorKey), ConnectionKey: strings.TrimSpace(request.ConnectionKey),
		OperationKey: strings.TrimSpace(request.OperationKey), ContractSHA256: strings.TrimSpace(request.ContractSHA256),
		Mode: runtimeext.ConnectorOperationMode(strings.TrimSpace(request.Mode)), Effect: runtimeext.ConnectorOperationEffect(strings.TrimSpace(request.Effect)),
	}
	lease, err := execution.AcquireSynchronousConnectorCall(capability)
	if err != nil {
		return ConnectorCallResult{}, err
	}
	defer lease.Release()
	requestID := identity.ExecutionID
	if identified, ok := lease.(connectorRequestIDLease); ok && strings.TrimSpace(identified.ConnectorRequestID()) != "" {
		requestID = strings.TrimSpace(identified.ConnectorRequestID())
	}
	persistence := integrationsdk.ProviderCallPersistenceStandard
	maskedDestination := ""
	if capability.Effect == runtimeext.ConnectorEffectReserve || capability.Effect == runtimeext.ConnectorEffectWrite {
		// Synchronous external effects can contain billable or sensitive business
		// payloads. Integration retains only routing/status evidence; the Handler
		// persists the approved result in business records.
		persistence = integrationsdk.ProviderCallPersistenceSensitive
		maskedDestination = capability.ConnectorKey + "/" + capability.OperationKey
	}
	result, err := gateway.operations.Call(ctx, integrationsdk.ProviderCallRequest{
		RequestID: requestID, WorkspaceID: workspace.ID,
		ConnectorKey: capability.ConnectorKey, ConnectionKey: capability.ConnectionKey, Operation: capability.OperationKey,
		Payload: append(json.RawMessage(nil), request.Payload...), PersistenceMode: persistence, MaskedDestination: maskedDestination,
		ActorID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey),
	})
	if err != nil {
		return ConnectorCallResult{}, err
	}
	return ConnectorCallResult{Payload: append(json.RawMessage(nil), result.Response...)}, nil
}
