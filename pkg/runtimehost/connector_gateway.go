package runtimehost

import (
	"context"
	"encoding/json"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

// ConnectorCallRequest is the generated composition DTO for one synchronous
// Connector operation. Runtime resolves Provider, Connection, Secret and all
// execution policy; project code cannot supply those owners.
type ConnectorCallRequest struct {
	ConnectorKey   string
	ConnectionKey  string
	OperationKey   string
	ContractSHA256 string
	Mode           string
	Effect         string
	Payload        json.RawMessage
}

type ConnectorCallResult struct {
	Payload json.RawMessage
}

// ConnectorGateway is visible only to generated composition. Action Handlers
// receive operation-specific typed clients instead of this generic port.
type ConnectorGateway interface {
	Call(context.Context, runtimeext.ActionExecution, ConnectorCallRequest) (ConnectorCallResult, error)
}

// BusinessHandlerFactory binds generated typed clients to the Runtime-owned
// Connector gateway and returns the complete immutable project Handler set.
type BusinessHandlerFactory func(ConnectorGateway) (runtimeext.ExtensionSet, error)
