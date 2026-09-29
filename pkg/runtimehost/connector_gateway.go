package runtimehost

import (
	"context"
	"encoding/json"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

// ConnectorCallRequest is the project composition DTO for one synchronous
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

// ConnectionAccountReadRequest is an exact, user-owned external account read.
// ConnectionKey is resolved at execution time because personal OAuth
// connections are created per user. Integration rechecks ownership, account
// state, provider contract and granted OAuth scopes before making the call.
type ConnectionAccountReadRequest struct {
	RequestID      string
	ConnectionKey  string
	OperationKey   string
	ContractSHA256 string
	Payload        json.RawMessage
}

type ConnectionAccountReadResult struct {
	InvocationID     string
	ReadAt           string
	PayloadAvailable bool
	Payload          json.RawMessage
}

// ConnectionAccountWriteRequest is an exact external account mutation. It
// defaults to the current user's personal account; product code may explicitly
// select one configured Workspace account for a source-owned Action.
type ConnectionAccountWriteRequest struct {
	// RequestID is an optional stable, code-owned effect identity. It lets a
	// record action recover the same Integration receipt after its local
	// transaction was interrupted. Browsers never call this gateway directly.
	RequestID     string
	ConnectionKey string
	// Workspace selects a server-configured Workspace account. The default is
	// the current user's personal account. Product handlers set this in code;
	// it is never copied from model or browser input.
	Workspace      bool
	OperationKey   string
	ContractSHA256 string
	Payload        json.RawMessage
}

type ConnectionAccountWriteResult struct {
	InvocationID  string
	Status        string
	RecordedAt    string
	ConnectionKey string
	ConnectorKey  string
	ProviderKey   string
	Receipt       json.RawMessage
}

// KnowledgeDocumentUploadRequest publishes one immutable project-owned source
// into an existing Knowledge library selected by the current user. Library
// membership, write role and datasource readiness are rechecked by Agent.
type KnowledgeDocumentUploadRequest struct {
	LibraryID string
	ClientID  string
	Filename  string
	Data      []byte
}

type KnowledgeDocumentReadRequest struct {
	LibraryID  string
	DocumentID string
}

// KnowledgeLibraryResult exposes only access facts needed before project-owned
// source content is published. The Agent service resolves current membership.
type KnowledgeLibraryResult struct {
	ID          string
	Kind        string
	OwnerUserID string
	Role        string
}

// KnowledgeDocumentResult intentionally exposes document lifecycle evidence
// only. Project handlers never receive Knowledge storage references or remote
// datasource credentials.
type KnowledgeDocumentResult struct {
	ID          string
	LibraryID   string
	Filename    string
	ContentType string
	Bytes       int64
	SHA256      string
	State       string
	IndexStatus string
	ErrorCode   string
	Revision    int64
	UpdatedAt   string
}

// ConnectorGateway is visible only to project composition. Action Handlers
// receive operation-specific typed clients instead of this generic port.
type ConnectorGateway interface {
	Call(context.Context, runtimeext.ActionExecution, ConnectorCallRequest) (ConnectorCallResult, error)
	ReadConnectionAccount(context.Context, runtimeext.ActionExecution, ConnectionAccountReadRequest) (ConnectionAccountReadResult, error)
	WriteConnectionAccount(context.Context, runtimeext.ActionExecution, ConnectionAccountWriteRequest) (ConnectionAccountWriteResult, error)
	UploadKnowledgeDocument(context.Context, runtimeext.ActionExecution, KnowledgeDocumentUploadRequest) (KnowledgeDocumentResult, error)
	ReadKnowledgeDocument(context.Context, runtimeext.ActionExecution, KnowledgeDocumentReadRequest) (KnowledgeDocumentResult, error)
	ReadKnowledgeLibrary(context.Context, runtimeext.ActionExecution, string) (KnowledgeLibraryResult, error)
}

// ProjectExtensionFactory binds typed clients to the Runtime-owned
// Connector gateway and returns the complete immutable project extension set.
type ProjectExtensionFactory func(ConnectorGateway) (runtimeext.ProjectExtensions, error)
