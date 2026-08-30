package runtimehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

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
