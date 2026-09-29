package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
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
	request             ConnectorCallRequest
	result              ConnectorCallResult
	accountReadRequest  ConnectionAccountReadRequest
	accountReadResult   ConnectionAccountReadResult
	accountWriteRequest ConnectionAccountWriteRequest
	accountWriteResult  ConnectionAccountWriteResult
	knowledgeUpload     KnowledgeDocumentUploadRequest
	knowledgeRead       KnowledgeDocumentReadRequest
	knowledgeResult     KnowledgeDocumentResult
	knowledgeLibraryID  string
	knowledgeLibrary    KnowledgeLibraryResult
}

func (t *connectorBindingTarget) ReadConnectionAccount(_ context.Context, _ runtimeext.ActionExecution, request ConnectionAccountReadRequest) (ConnectionAccountReadResult, error) {
	t.accountReadRequest = request
	return t.accountReadResult, nil
}

func (t *connectorBindingTarget) Call(_ context.Context, _ runtimeext.ActionExecution, request ConnectorCallRequest) (ConnectorCallResult, error) {
	t.request = request
	return t.result, nil
}

func (t *connectorBindingTarget) WriteConnectionAccount(_ context.Context, _ runtimeext.ActionExecution, request ConnectionAccountWriteRequest) (ConnectionAccountWriteResult, error) {
	t.accountWriteRequest = request
	return t.accountWriteResult, nil
}

func (t *connectorBindingTarget) UploadKnowledgeDocument(_ context.Context, _ runtimeext.ActionExecution, request KnowledgeDocumentUploadRequest) (KnowledgeDocumentResult, error) {
	t.knowledgeUpload = request
	return t.knowledgeResult, nil
}

func (t *connectorBindingTarget) ReadKnowledgeDocument(_ context.Context, _ runtimeext.ActionExecution, request KnowledgeDocumentReadRequest) (KnowledgeDocumentResult, error) {
	t.knowledgeRead = request
	return t.knowledgeResult, nil
}

func (t *connectorBindingTarget) ReadKnowledgeLibrary(_ context.Context, _ runtimeext.ActionExecution, libraryID string) (KnowledgeLibraryResult, error) {
	t.knowledgeLibraryID = libraryID
	return t.knowledgeLibrary, nil
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
		if _, err := gateway.WriteConnectionAccount(t.Context(), connectorBindingExecution{}, ConnectionAccountWriteRequest{Payload: payload}); runtimeextErrorCode(err) != "backend.connector.request_invalid" {
			t.Fatalf("account payload=%s error=%v", payload, err)
		}
		if _, err := gateway.ReadConnectionAccount(t.Context(), connectorBindingExecution{}, ConnectionAccountReadRequest{Payload: payload}); runtimeextErrorCode(err) != "backend.connector.request_invalid" {
			t.Fatalf("account read payload=%s error=%v", payload, err)
		}
	}
	if _, err := gateway.UploadKnowledgeDocument(t.Context(), connectorBindingExecution{}, KnowledgeDocumentUploadRequest{}); runtimeextErrorCode(err) != "backend.knowledge.upload_request_invalid" {
		t.Fatalf("knowledge upload error=%v", err)
	}
	if _, err := gateway.ReadKnowledgeDocument(t.Context(), connectorBindingExecution{}, KnowledgeDocumentReadRequest{}); runtimeextErrorCode(err) != "backend.knowledge.read_request_invalid" {
		t.Fatalf("knowledge read error=%v", err)
	}
	if _, err := gateway.ReadKnowledgeLibrary(t.Context(), connectorBindingExecution{}, ""); runtimeextErrorCode(err) != "backend.knowledge.library_request_invalid" {
		t.Fatalf("knowledge library read error=%v", err)
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
	identity           runtimeext.ExecutionIdentity
	principal          runtimeext.Principal
	workspace          runtimeext.Workspace
	lease              *integrationConnectorLease
	capability         runtimeext.ActionConnectorCapability
	acquireErr         error
	accountWriteLeased bool
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

func (execution *integrationConnectorExecution) AcquireConnectionAccountWrite() (runtimeext.SynchronousConnectorCallLease, error) {
	execution.accountWriteLeased = true
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

type integrationAccountWritesProbe struct {
	integrationsdk.ConnectionAccountWrites
	subject integrationsdk.ConnectionAccountSubject
	key     string
	op      integrationsdk.ConnectionAccountWriteOperation
	request integrationsdk.ConnectionAccountWriteRequest
	result  integrationsdk.ConnectionAccountWriteResult
	calls   int
}

type integrationAccountReadsProbe struct {
	integrationsdk.ConnectionAccountReads
	subject integrationsdk.ConnectionAccountSubject
	key     string
	op      integrationsdk.ConnectionAccountReadOperation
	request integrationsdk.ConnectionAccountReadRequest
	result  integrationsdk.ConnectionAccountReadResult
	calls   int
}

type knowledgeDocumentServiceProbe struct {
	agentsdk.KnowledgeDocumentService
	uploadLibrary string
	upload        agentsdk.KnowledgeDocumentUpload
	uploadSource  agentsdk.KnowledgeDocumentSourceAccess
	readLibrary   string
	readDocument  string
	authority     agentsdk.ConversationAuthority
	result        agentsdk.KnowledgeDocument
}

type knowledgeLibraryServiceProbe struct {
	agentsdk.KnowledgeLibraryService
	libraryID string
	authority agentsdk.ConversationAuthority
	result    agentsdk.KnowledgeLibrary
}

func (probe *knowledgeLibraryServiceProbe) KnowledgeLibrary(_ context.Context, id string, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	probe.libraryID, probe.authority = id, authority
	return probe.result, nil
}

func (probe *knowledgeDocumentServiceProbe) UploadKnowledgeDocument(_ context.Context, library string, upload agentsdk.KnowledgeDocumentUpload, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	probe.uploadLibrary, probe.upload, probe.authority = library, upload, authority
	return probe.result, nil
}

func (probe *knowledgeDocumentServiceProbe) UploadKnowledgeDocumentForSource(_ context.Context, library string, upload agentsdk.KnowledgeDocumentUpload, source agentsdk.KnowledgeDocumentSourceAccess, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	probe.uploadLibrary, probe.upload, probe.uploadSource, probe.authority = library, upload, source, authority
	return probe.result, nil
}

func (probe *knowledgeDocumentServiceProbe) KnowledgeDocument(_ context.Context, library, document string, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	probe.readLibrary, probe.readDocument, probe.authority = library, document, authority
	return probe.result, nil
}

func (probe *integrationAccountReadsProbe) AuthorizeConnectionAccountRead(_ context.Context, subject integrationsdk.ConnectionAccountSubject, key string, op integrationsdk.ConnectionAccountReadOperation) (integrationsdk.ConnectionAccountReadAccess, error) {
	probe.subject, probe.key, probe.op = subject, key, op
	return integrationsdk.ConnectionAccountReadAccess{}, nil
}

func (probe *integrationAccountReadsProbe) ReadConnectionAccount(_ context.Context, subject integrationsdk.ConnectionAccountSubject, key string, request integrationsdk.ConnectionAccountReadRequest) (integrationsdk.ConnectionAccountReadResult, error) {
	probe.calls++
	probe.subject, probe.key, probe.request = subject, key, request
	return probe.result, nil
}

func (probe *integrationAccountWritesProbe) AuthorizeConnectionAccountWrite(_ context.Context, subject integrationsdk.ConnectionAccountSubject, key string, op integrationsdk.ConnectionAccountWriteOperation) (integrationsdk.ConnectionAccountWriteAccess, error) {
	probe.subject, probe.key, probe.op = subject, key, op
	return integrationsdk.ConnectionAccountWriteAccess{Source: integrationsdk.ConnectionAccountWriteSource{
		WorkspaceID: subject.WorkspaceID, ConnectionKey: key, ConnectorKey: "google_workspace", ProviderKey: "google",
		AccountUpdatedAt: "2026-09-26T00:00:00Z", Operation: op.Operation, ContractSHA256: op.ContractSHA256,
	}}, nil
}

func (probe *integrationAccountWritesProbe) WriteConnectionAccount(_ context.Context, _ integrationsdk.ConnectionAccountSubject, _ string, request integrationsdk.ConnectionAccountWriteRequest) (integrationsdk.ConnectionAccountWriteResult, error) {
	probe.calls++
	probe.request = request
	result := probe.result
	if result.Source.ConnectionKey == "" {
		result.Source = request.ExpectedSource
	}
	return result, nil
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
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "execution-1", ActionKey: "invoice_ocr_job.process", ObjectKey: "invoice", RecordID: "invoice-a"},
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
	if got.Source != (integrationsdk.InvocationSource{ExecutionID: "execution-1", ObjectKey: "invoice", RecordID: "invoice-a"}) || got.RequestID != lease.requestID || got.WorkspaceID != "workspace-a" || got.ConnectorKey != "expense_ocr" || got.ConnectionKey != "primary" || got.Operation != "parse_expense" || got.ActorID != "worker-1" || got.RoleKey != "ocr_worker" || got.PersistenceMode != integrationsdk.ProviderCallPersistenceSensitive || got.MaskedDestination != "expense_ocr/parse_expense" || string(got.Payload) != string(request.Payload) {
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

func TestIntegrationRuntimeConnectorGatewayUsesOwnedPersonalAccountRead(t *testing.T) {
	hash := strings.Repeat("e", 64)
	probe := &integrationAccountReadsProbe{result: integrationsdk.ConnectionAccountReadResult{
		InvocationID: "account-read:1", ReadAt: "2026-09-26T00:00:01Z", PayloadAvailable: true, Payload: json.RawMessage(`{"status":"ready"}`),
	}}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "sync-1", ActionKey: "meeting.sync_transcript", ObjectKey: "meeting", RecordID: "meeting-a"},
		principal: runtimeext.Principal{UserID: "user-a", RoleKey: "sales_rep"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
	}
	request := ConnectionAccountReadRequest{ConnectionKey: "feishu-a", OperationKey: "fetch_meeting_content", ContractSHA256: hash, Payload: json.RawMessage(`{"meeting_no":"123456789"}`)}
	result, err := (integrationRuntimeConnectorGateway{accountReads: probe}).ReadConnectionAccount(t.Context(), execution, request)
	if err != nil || !result.PayloadAvailable || result.InvocationID != "account-read:1" || string(result.Payload) != `{"status":"ready"}` {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if probe.calls != 1 || probe.subject.WorkspaceID != "workspace-a" || probe.subject.UserID != "user-a" || !probe.subject.Access.Personal || probe.subject.Access.Workspace || probe.key != "feishu-a" || probe.op.Operation != "fetch_meeting_content" || probe.op.ContractSHA256 != hash {
		t.Fatalf("probe=%+v subject=%+v", probe, probe.subject)
	}
	if probe.request.RequestID != "sync-1" || probe.request.Operation != "fetch_meeting_content" || probe.request.ContractSHA256 != hash || string(probe.request.Payload) != string(request.Payload) {
		t.Fatalf("read request=%+v", probe.request)
	}
}

func TestIntegrationRuntimeConnectorGatewayUsesOwnedPersonalAccountWrite(t *testing.T) {
	hash := strings.Repeat("d", 64)
	probe := &integrationAccountWritesProbe{result: integrationsdk.ConnectionAccountWriteResult{
		InvocationID: "account-write:1", Status: integrationsdk.AccountWriteSucceeded, RecordedAt: "2026-09-26T00:00:01Z", Receipt: json.RawMessage(`{"status":"accepted"}`),
	}}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "send-1", ActionKey: "email_draft.send", ObjectKey: "email_draft", RecordID: "draft-a"},
		principal: runtimeext.Principal{UserID: "user-a", RoleKey: "sales_rep"}, workspace: runtimeext.Workspace{ID: "workspace-a"}, lease: &integrationConnectorLease{},
	}
	request := ConnectionAccountWriteRequest{RequestID: "email-draft:draft-a:revision-1", ConnectionKey: "gmail-a", OperationKey: "mail_send", ContractSHA256: hash, Payload: json.RawMessage(`{"message":{"to":[]}}`)}
	result, err := (integrationRuntimeConnectorGateway{accountWrites: probe}).WriteConnectionAccount(t.Context(), execution, request)
	if err != nil || result.Status != integrationsdk.AccountWriteSucceeded || result.InvocationID != "account-write:1" || result.ConnectionKey != "gmail-a" || result.ConnectorKey != "google_workspace" || result.ProviderKey != "google" || string(result.Receipt) != `{"status":"accepted"}` {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if probe.calls != 1 || probe.subject.WorkspaceID != "workspace-a" || probe.subject.UserID != "user-a" || !probe.subject.Access.Personal || probe.subject.Access.Workspace || probe.key != "gmail-a" || probe.op.Operation != "mail_send" || probe.op.ContractSHA256 != hash {
		t.Fatalf("probe=%+v subject=%+v", probe, probe.subject)
	}
	if probe.request.RequestID != request.RequestID || probe.request.ExpectedSource.ConnectionKey != "gmail-a" || string(probe.request.Payload) != string(request.Payload) {
		t.Fatalf("write request=%+v", probe.request)
	}
	if !execution.accountWriteLeased || !execution.lease.released {
		t.Fatalf("account write lease was not acquired and released: execution=%+v", execution)
	}
}

func TestIntegrationRuntimeConnectorGatewayUsesExplicitWorkspaceAccountWrite(t *testing.T) {
	hash := strings.Repeat("f", 64)
	probe := &integrationAccountWritesProbe{result: integrationsdk.ConnectionAccountWriteResult{Status: integrationsdk.AccountWriteSucceeded}}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "feishu-send-1", ActionKey: "activity.send_feishu_message", ObjectKey: "activity"},
		principal: runtimeext.Principal{UserID: "user-a", RoleKey: "sales_rep"}, workspace: runtimeext.Workspace{ID: "workspace-a"}, lease: &integrationConnectorLease{},
	}
	request := ConnectionAccountWriteRequest{RequestID: "feishu-message:1", ConnectionKey: "feishu-agent", Workspace: true, OperationKey: "collaboration_message_send", ContractSHA256: hash, Payload: json.RawMessage(`{"recipient":"buyer@example.test","text":"hello"}`)}
	result, err := (integrationRuntimeConnectorGateway{accountWrites: probe}).WriteConnectionAccount(t.Context(), execution, request)
	if err != nil || result.ConnectionKey != request.ConnectionKey {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if probe.calls != 1 || probe.subject.WorkspaceID != "workspace-a" || probe.subject.UserID != "user-a" || probe.subject.Access.Personal || !probe.subject.Access.Workspace || probe.key != request.ConnectionKey {
		t.Fatalf("probe=%+v subject=%+v", probe, probe.subject)
	}
}

func TestIntegrationRuntimeConnectorGatewayDefaultsAccountWriteRequestIDToExecution(t *testing.T) {
	probe := &integrationAccountWritesProbe{result: integrationsdk.ConnectionAccountWriteResult{Status: integrationsdk.AccountWriteFailed}}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "send-default", ActionKey: "email_draft.send", ObjectKey: "email_draft", RecordID: "draft-a"},
		principal: runtimeext.Principal{UserID: "user-a", RoleKey: "sales_rep"}, workspace: runtimeext.Workspace{ID: "workspace-a"}, lease: &integrationConnectorLease{},
	}
	_, err := (integrationRuntimeConnectorGateway{accountWrites: probe}).WriteConnectionAccount(t.Context(), execution, ConnectionAccountWriteRequest{
		ConnectionKey: "gmail-a", OperationKey: "mail_send", ContractSHA256: strings.Repeat("d", 64), Payload: json.RawMessage(`{"message":{"to":[]}}`),
	})
	if err != nil || probe.request.RequestID != "send-default" {
		t.Fatalf("request=%+v err=%v", probe.request, err)
	}
}

func TestIntegrationRuntimeConnectorGatewayUsesAuthorizedKnowledgeDocumentService(t *testing.T) {
	updatedAt := time.Date(2026, 9, 26, 1, 2, 3, 0, time.UTC)
	probe := &knowledgeDocumentServiceProbe{result: agentsdk.KnowledgeDocument{
		ID: "document-a", LibraryID: "library-a", Filename: "meeting-a.txt", ContentType: "text/plain", Bytes: 10,
		SHA256: strings.Repeat("a", 64), State: "queued", IndexStatus: "QUEUED", Revision: 2, UpdatedAt: updatedAt,
	}}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "publish-1", ActionKey: "meeting.publish_transcript", ObjectKey: "meeting", RecordID: "meeting-a"},
		principal: runtimeext.Principal{UserID: "user-a", RoleKey: "sales_rep"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
	}
	gateway := integrationRuntimeConnectorGateway{runtimeID: "aurora-runtime", knowledgeDocuments: probe}
	content := []byte("transcript")
	result, err := gateway.UploadKnowledgeDocument(t.Context(), execution, KnowledgeDocumentUploadRequest{
		LibraryID: "library-a", ClientID: "meeting:meeting-a:hash", Filename: "meeting-a.txt", Data: content,
	})
	if err != nil || result.ID != "document-a" || result.State != "queued" || result.IndexStatus != "QUEUED" || result.UpdatedAt != "2026-09-26T01:02:03Z" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	content[0] = 'X'
	if probe.uploadLibrary != "library-a" || probe.upload.ClientID != "meeting:meeting-a:hash" || string(probe.upload.Data) != "transcript" {
		t.Fatalf("upload library=%q upload=%+v", probe.uploadLibrary, probe.upload)
	}
	if probe.uploadSource != (agentsdk.KnowledgeDocumentSourceAccess{Namespace: agentsdk.KnowledgeDocumentSourceNamespaceRuntimeRecord, ResourceType: "meeting", ResourceID: "meeting-a"}) {
		t.Fatalf("source=%+v", probe.uploadSource)
	}
	wantAuthority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "aurora-runtime", WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "sales_rep"}
	if probe.authority != wantAuthority {
		t.Fatalf("authority=%+v", probe.authority)
	}
	result, err = gateway.ReadKnowledgeDocument(t.Context(), execution, KnowledgeDocumentReadRequest{LibraryID: "library-a", DocumentID: "document-a"})
	if err != nil || result.ID != "document-a" || probe.readLibrary != "library-a" || probe.readDocument != "document-a" || probe.authority != wantAuthority {
		t.Fatalf("result=%+v read=%q/%q authority=%+v err=%v", result, probe.readLibrary, probe.readDocument, probe.authority, err)
	}
}

func TestIntegrationRuntimeConnectorGatewayRequiresSourceAwareKnowledgeService(t *testing.T) {
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "publish-1", ActionKey: "meeting.publish_transcript", ObjectKey: "meeting", RecordID: "meeting-a"},
		principal: runtimeext.Principal{UserID: "user-a", RoleKey: "sales_rep"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
	}
	service := struct {
		agentsdk.KnowledgeDocumentService
	}{}
	_, err := (integrationRuntimeConnectorGateway{runtimeID: "aurora-runtime", knowledgeDocuments: service}).UploadKnowledgeDocument(t.Context(), execution, KnowledgeDocumentUploadRequest{
		LibraryID: "library-a", ClientID: "meeting:meeting-a:hash", Filename: "meeting-a.txt", Data: []byte("transcript"),
	})
	if runtimeextErrorCode(err) != "backend.knowledge.source_acl_unavailable" {
		t.Fatalf("error=%v", err)
	}
}

func TestIntegrationRuntimeConnectorGatewayReadsCurrentKnowledgeLibrary(t *testing.T) {
	probe := &knowledgeLibraryServiceProbe{result: agentsdk.KnowledgeLibrary{ID: "library-a", Kind: "personal", OwnerUserID: "user-a", Role: "manager"}}
	execution := &integrationConnectorExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "publish-1", ActionKey: "meeting.publish_transcript", ObjectKey: "meeting", RecordID: "meeting-a"},
		principal: runtimeext.Principal{UserID: "user-a", RoleKey: "sales_rep"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
	}
	result, err := (integrationRuntimeConnectorGateway{runtimeID: "aurora-runtime", knowledgeLibraries: probe}).ReadKnowledgeLibrary(t.Context(), execution, "library-a")
	if err != nil || result.ID != "library-a" || result.Kind != "personal" || result.OwnerUserID != "user-a" || result.Role != "manager" {
		t.Fatalf("library=%+v err=%v", result, err)
	}
	if probe.libraryID != "library-a" || probe.authority != (agentsdk.ConversationAuthority{Known: true, RuntimeID: "aurora-runtime", WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "sales_rep"}) {
		t.Fatalf("request=%q authority=%+v", probe.libraryID, probe.authority)
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
