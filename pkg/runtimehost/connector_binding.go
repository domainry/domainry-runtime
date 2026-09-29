package runtimehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type runtimeConnectorGateway interface {
	Call(context.Context, runtimeext.ActionExecution, ConnectorCallRequest) (ConnectorCallResult, error)
	ReadConnectionAccount(context.Context, runtimeext.ActionExecution, ConnectionAccountReadRequest) (ConnectionAccountReadResult, error)
	WriteConnectionAccount(context.Context, runtimeext.ActionExecution, ConnectionAccountWriteRequest) (ConnectionAccountWriteResult, error)
	UploadKnowledgeDocument(context.Context, runtimeext.ActionExecution, KnowledgeDocumentUploadRequest) (KnowledgeDocumentResult, error)
	ReadKnowledgeDocument(context.Context, runtimeext.ActionExecution, KnowledgeDocumentReadRequest) (KnowledgeDocumentResult, error)
	ReadKnowledgeLibrary(context.Context, runtimeext.ActionExecution, string) (KnowledgeLibraryResult, error)
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

func (g *bindableConnectorGateway) ReadConnectionAccount(ctx context.Context, execution runtimeext.ActionExecution, request ConnectionAccountReadRequest) (ConnectionAccountReadResult, error) {
	g.mu.RLock()
	target := g.target
	g.mu.RUnlock()
	if target == nil {
		return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: "backend.connector.gateway_unavailable", Message: "Runtime Connector gateway is unavailable"}
	}
	if execution == nil {
		return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Connector ActionExecution is required"}
	}
	if _, err := decodeConnectorCallPayload(request.Payload); err != nil {
		return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: "backend.connector.request_invalid", Message: "Connector request payload must be one JSON object", Cause: err}
	}
	return target.ReadConnectionAccount(ctx, execution, request)
}

func (g *bindableConnectorGateway) WriteConnectionAccount(ctx context.Context, execution runtimeext.ActionExecution, request ConnectionAccountWriteRequest) (ConnectionAccountWriteResult, error) {
	g.mu.RLock()
	target := g.target
	g.mu.RUnlock()
	if target == nil {
		return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: "backend.connector.gateway_unavailable", Message: "Runtime Connector gateway is unavailable"}
	}
	if execution == nil {
		return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Connector ActionExecution is required"}
	}
	if _, err := decodeConnectorCallPayload(request.Payload); err != nil {
		return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: "backend.connector.request_invalid", Message: "Connector request payload must be one JSON object", Cause: err}
	}
	return target.WriteConnectionAccount(ctx, execution, request)
}

func (g *bindableConnectorGateway) UploadKnowledgeDocument(ctx context.Context, execution runtimeext.ActionExecution, request KnowledgeDocumentUploadRequest) (KnowledgeDocumentResult, error) {
	g.mu.RLock()
	target := g.target
	g.mu.RUnlock()
	if target == nil {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Runtime Knowledge gateway is unavailable"}
	}
	if execution == nil {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Knowledge ActionExecution is required"}
	}
	if strings.TrimSpace(request.LibraryID) == "" || strings.TrimSpace(request.ClientID) == "" || strings.TrimSpace(request.Filename) == "" || len(request.Data) == 0 {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.upload_request_invalid", Message: "Knowledge document upload request is invalid"}
	}
	request.Data = append([]byte(nil), request.Data...)
	return target.UploadKnowledgeDocument(ctx, execution, request)
}

func (g *bindableConnectorGateway) ReadKnowledgeDocument(ctx context.Context, execution runtimeext.ActionExecution, request KnowledgeDocumentReadRequest) (KnowledgeDocumentResult, error) {
	g.mu.RLock()
	target := g.target
	g.mu.RUnlock()
	if target == nil {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Runtime Knowledge gateway is unavailable"}
	}
	if execution == nil {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Knowledge ActionExecution is required"}
	}
	if strings.TrimSpace(request.LibraryID) == "" || strings.TrimSpace(request.DocumentID) == "" {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.read_request_invalid", Message: "Knowledge document read request is invalid"}
	}
	return target.ReadKnowledgeDocument(ctx, execution, request)
}

func (g *bindableConnectorGateway) ReadKnowledgeLibrary(ctx context.Context, execution runtimeext.ActionExecution, libraryID string) (KnowledgeLibraryResult, error) {
	g.mu.RLock()
	target := g.target
	g.mu.RUnlock()
	if target == nil {
		return KnowledgeLibraryResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Runtime Knowledge gateway is unavailable"}
	}
	if execution == nil {
		return KnowledgeLibraryResult{}, &runtimeext.BusinessError{Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Knowledge ActionExecution is required"}
	}
	if strings.TrimSpace(libraryID) == "" {
		return KnowledgeLibraryResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.library_request_invalid", Message: "Knowledge library ID is required"}
	}
	return target.ReadKnowledgeLibrary(ctx, execution, strings.TrimSpace(libraryID))
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

func (unavailableRuntimeConnectorGateway) ReadConnectionAccount(context.Context, runtimeext.ActionExecution, ConnectionAccountReadRequest) (ConnectionAccountReadResult, error) {
	return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: "backend.connector.gateway_unavailable", Message: "Integration account reads are unavailable"}
}

func (unavailableRuntimeConnectorGateway) WriteConnectionAccount(context.Context, runtimeext.ActionExecution, ConnectionAccountWriteRequest) (ConnectionAccountWriteResult, error) {
	return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: "backend.connector.gateway_unavailable", Message: "Integration account writes are unavailable"}
}

func (unavailableRuntimeConnectorGateway) UploadKnowledgeDocument(context.Context, runtimeext.ActionExecution, KnowledgeDocumentUploadRequest) (KnowledgeDocumentResult, error) {
	return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Knowledge document uploads are unavailable"}
}

func (unavailableRuntimeConnectorGateway) ReadKnowledgeDocument(context.Context, runtimeext.ActionExecution, KnowledgeDocumentReadRequest) (KnowledgeDocumentResult, error) {
	return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Knowledge document reads are unavailable"}
}

func (unavailableRuntimeConnectorGateway) ReadKnowledgeLibrary(context.Context, runtimeext.ActionExecution, string) (KnowledgeLibraryResult, error) {
	return KnowledgeLibraryResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Knowledge library reads are unavailable"}
}

type connectorRequestIDLease interface {
	runtimeext.SynchronousConnectorCallLease
	ConnectorRequestID() string
}

type integrationRuntimeConnectorGateway struct {
	operations         integrationsdk.Operations
	accountReads       integrationsdk.ConnectionAccountReads
	accountWrites      integrationsdk.ConnectionAccountWrites
	runtimeID          string
	knowledgeDocuments agentsdk.KnowledgeDocumentService
	knowledgeLibraries agentsdk.KnowledgeLibraryService
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
		Source:    integrationsdk.InvocationSource{ExecutionID: identity.ExecutionID, ObjectKey: identity.ObjectKey, RecordID: identity.RecordID},
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

func (gateway integrationRuntimeConnectorGateway) ReadConnectionAccount(ctx context.Context, execution runtimeext.ActionExecution, request ConnectionAccountReadRequest) (ConnectionAccountReadResult, error) {
	if gateway.accountReads == nil {
		return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: "backend.connector.account_read_unavailable", Message: "Integration account reads are unavailable"}
	}
	identity, workspace, principal := execution.Identity(), execution.Workspace(), execution.Principal()
	if !identity.Valid() || !workspace.Valid() || strings.TrimSpace(principal.UserID) == "" {
		return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: "backend.connector.execution_identity_invalid", Message: "Account read execution identity, Workspace and user are required"}
	}
	connectionKey := strings.TrimSpace(request.ConnectionKey)
	operation := integrationsdk.ConnectionAccountReadOperation{Operation: strings.TrimSpace(request.OperationKey), ContractSHA256: strings.TrimSpace(request.ContractSHA256)}
	if connectionKey == "" || operation.Validate() != nil {
		return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: "backend.connector.account_read_request_invalid", Message: "Account read connection and operation are invalid"}
	}
	subject := integrationsdk.ConnectionAccountSubject{
		WorkspaceID: workspace.ID, UserID: strings.TrimSpace(principal.UserID),
		Access: integrationsdk.ConnectionAccountAccess{Personal: true},
	}
	if _, err := gateway.accountReads.AuthorizeConnectionAccountRead(ctx, subject, connectionKey, operation); err != nil {
		return ConnectionAccountReadResult{}, &runtimeext.BusinessError{Code: "backend.connector.account_read_denied", Message: "Connection account read is not authorized", Cause: err}
	}
	requestID := strings.TrimSpace(request.RequestID)
	if requestID == "" {
		requestID = identity.ExecutionID
	}
	result, err := gateway.accountReads.ReadConnectionAccount(ctx, subject, connectionKey, integrationsdk.ConnectionAccountReadRequest{
		RequestID: requestID, Operation: operation.Operation, ContractSHA256: operation.ContractSHA256, Payload: append(json.RawMessage(nil), request.Payload...),
	})
	if err != nil {
		return ConnectionAccountReadResult{}, err
	}
	return ConnectionAccountReadResult{
		InvocationID: result.InvocationID, ReadAt: result.ReadAt, PayloadAvailable: result.PayloadAvailable,
		Payload: append(json.RawMessage(nil), result.Payload...),
	}, nil
}

func (gateway integrationRuntimeConnectorGateway) WriteConnectionAccount(ctx context.Context, execution runtimeext.ActionExecution, request ConnectionAccountWriteRequest) (ConnectionAccountWriteResult, error) {
	if gateway.accountWrites == nil {
		return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: "backend.connector.account_write_unavailable", Message: "Integration account writes are unavailable"}
	}
	identity, workspace, principal := execution.Identity(), execution.Workspace(), execution.Principal()
	if !identity.Valid() || !workspace.Valid() || strings.TrimSpace(principal.UserID) == "" {
		return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: "backend.connector.execution_identity_invalid", Message: "Account write execution identity, Workspace and user are required"}
	}
	connectionKey := strings.TrimSpace(request.ConnectionKey)
	operation := integrationsdk.ConnectionAccountWriteOperation{Operation: strings.TrimSpace(request.OperationKey), ContractSHA256: strings.TrimSpace(request.ContractSHA256)}
	if connectionKey == "" || operation.Validate() != nil {
		return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: "backend.connector.account_write_request_invalid", Message: "Account write connection and operation are invalid"}
	}
	lease, err := runtimeext.AcquireConnectionAccountWrite(execution)
	if err != nil {
		return ConnectionAccountWriteResult{}, err
	}
	defer lease.Release()
	subject := integrationsdk.ConnectionAccountSubject{
		WorkspaceID: workspace.ID, UserID: strings.TrimSpace(principal.UserID),
		Access: integrationsdk.ConnectionAccountAccess{Personal: !request.Workspace, Workspace: request.Workspace},
	}
	access, err := gateway.accountWrites.AuthorizeConnectionAccountWrite(ctx, subject, connectionKey, operation)
	if err != nil {
		return ConnectionAccountWriteResult{}, &runtimeext.BusinessError{Code: "backend.connector.account_write_denied", Message: "Connection account write is not authorized", Cause: err}
	}
	requestID := strings.TrimSpace(request.RequestID)
	if requestID == "" {
		requestID = identity.ExecutionID
	}
	result, err := gateway.accountWrites.WriteConnectionAccount(ctx, subject, connectionKey, integrationsdk.ConnectionAccountWriteRequest{
		RequestID: requestID, ExpectedSource: access.Source, Payload: append(json.RawMessage(nil), request.Payload...),
	})
	if err != nil {
		return ConnectionAccountWriteResult{}, err
	}
	return ConnectionAccountWriteResult{
		InvocationID: result.InvocationID, Status: result.Status, RecordedAt: result.RecordedAt,
		ConnectionKey: result.Source.ConnectionKey, ConnectorKey: result.Source.ConnectorKey, ProviderKey: result.Source.ProviderKey,
		Receipt: append(json.RawMessage(nil), result.Receipt...),
	}, nil
}

func knowledgeDocumentAuthority(runtimeID string, execution runtimeext.ActionExecution) (agentsdk.ConversationAuthority, error) {
	if execution == nil {
		return agentsdk.ConversationAuthority{}, &runtimeext.BusinessError{Code: runtimeext.ConnectorActionExecutionRequiredErrorCode, Message: "Knowledge ActionExecution is required"}
	}
	identity, workspace, principal := execution.Identity(), execution.Workspace(), execution.Principal()
	if !identity.Valid() || !workspace.Valid() || strings.TrimSpace(runtimeID) == "" || strings.TrimSpace(principal.UserID) == "" || strings.TrimSpace(principal.RoleKey) == "" {
		return agentsdk.ConversationAuthority{}, &runtimeext.BusinessError{Code: "backend.knowledge.execution_identity_invalid", Message: "Knowledge execution identity, Runtime, Workspace, user and role are required"}
	}
	if execution.Phase() != runtimeext.ExecutionPhasePrewrite {
		return agentsdk.ConversationAuthority{}, &runtimeext.BusinessError{Code: "backend.knowledge.call_after_write_forbidden", Message: "Knowledge operations must run before business record writes"}
	}
	return agentsdk.ConversationAuthority{
		Known: true, RuntimeID: strings.TrimSpace(runtimeID), WorkspaceID: workspace.ID,
		UserID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey),
	}, nil
}

func knowledgeDocumentResult(document agentsdk.KnowledgeDocument) KnowledgeDocumentResult {
	return KnowledgeDocumentResult{
		ID: document.ID, LibraryID: document.LibraryID, Filename: document.Filename, ContentType: document.ContentType,
		Bytes: document.Bytes, SHA256: document.SHA256, State: document.State, IndexStatus: document.IndexStatus,
		ErrorCode: document.ErrorCode, Revision: document.Revision, UpdatedAt: document.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

func (gateway integrationRuntimeConnectorGateway) UploadKnowledgeDocument(ctx context.Context, execution runtimeext.ActionExecution, request KnowledgeDocumentUploadRequest) (KnowledgeDocumentResult, error) {
	if gateway.knowledgeDocuments == nil {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Knowledge document service is unavailable"}
	}
	authority, err := knowledgeDocumentAuthority(gateway.runtimeID, execution)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	identity := execution.Identity()
	if strings.TrimSpace(identity.ObjectKey) == "" || strings.TrimSpace(identity.RecordID) == "" {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.source_record_required", Message: "Knowledge uploads require an exact source record"}
	}
	sourceDocuments, ok := gateway.knowledgeDocuments.(agentsdk.KnowledgeDocumentSourceUploadService)
	if !ok {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.source_acl_unavailable", Message: "Knowledge source record authorization is unavailable"}
	}
	document, err := sourceDocuments.UploadKnowledgeDocumentForSource(ctx, strings.TrimSpace(request.LibraryID), agentsdk.KnowledgeDocumentUpload{
		ClientID: strings.TrimSpace(request.ClientID), Filename: strings.TrimSpace(request.Filename), Data: append([]byte(nil), request.Data...),
	}, agentsdk.KnowledgeDocumentSourceAccess{
		Namespace: agentsdk.KnowledgeDocumentSourceNamespaceRuntimeRecord, ResourceType: strings.TrimSpace(identity.ObjectKey), ResourceID: strings.TrimSpace(identity.RecordID),
	}, authority)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return knowledgeDocumentResult(document), nil
}

func (gateway integrationRuntimeConnectorGateway) ReadKnowledgeDocument(ctx context.Context, execution runtimeext.ActionExecution, request KnowledgeDocumentReadRequest) (KnowledgeDocumentResult, error) {
	if gateway.knowledgeDocuments == nil {
		return KnowledgeDocumentResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Knowledge document service is unavailable"}
	}
	authority, err := knowledgeDocumentAuthority(gateway.runtimeID, execution)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	document, err := gateway.knowledgeDocuments.KnowledgeDocument(ctx, strings.TrimSpace(request.LibraryID), strings.TrimSpace(request.DocumentID), authority)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return knowledgeDocumentResult(document), nil
}

func (gateway integrationRuntimeConnectorGateway) ReadKnowledgeLibrary(ctx context.Context, execution runtimeext.ActionExecution, libraryID string) (KnowledgeLibraryResult, error) {
	if gateway.knowledgeLibraries == nil {
		return KnowledgeLibraryResult{}, &runtimeext.BusinessError{Code: "backend.knowledge.gateway_unavailable", Message: "Knowledge library service is unavailable"}
	}
	authority, err := knowledgeDocumentAuthority(gateway.runtimeID, execution)
	if err != nil {
		return KnowledgeLibraryResult{}, err
	}
	library, err := gateway.knowledgeLibraries.KnowledgeLibrary(ctx, strings.TrimSpace(libraryID), authority)
	if err != nil {
		return KnowledgeLibraryResult{}, err
	}
	return KnowledgeLibraryResult{ID: library.ID, Kind: library.Kind, OwnerUserID: library.OwnerUserID, Role: library.Role}, nil
}
