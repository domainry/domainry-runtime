package transport

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type projectAccountWritesProbe struct {
	integrationsdk.ConnectionAccountWrites
	subject   integrationsdk.ConnectionAccountSubject
	key       string
	operation integrationsdk.ConnectionAccountWriteOperation
	access    integrationsdk.ConnectionAccountWriteAccess
	err       error
}

type projectAccountReadsProbe struct {
	integrationsdk.ConnectionAccountReads
	subject      integrationsdk.ConnectionAccountSubject
	key          string
	operation    integrationsdk.ConnectionAccountReadOperation
	request      integrationsdk.ConnectionAccountReadRequest
	access       integrationsdk.ConnectionAccountReadAccess
	result       integrationsdk.ConnectionAccountReadResult
	authorizeErr error
	readErr      error
}

func (probe *projectAccountReadsProbe) AuthorizeConnectionAccountRead(_ context.Context, subject integrationsdk.ConnectionAccountSubject, key string, operation integrationsdk.ConnectionAccountReadOperation) (integrationsdk.ConnectionAccountReadAccess, error) {
	probe.subject, probe.key, probe.operation = subject, key, operation
	return probe.access, probe.authorizeErr
}

func (probe *projectAccountReadsProbe) ReadConnectionAccount(_ context.Context, subject integrationsdk.ConnectionAccountSubject, key string, request integrationsdk.ConnectionAccountReadRequest) (integrationsdk.ConnectionAccountReadResult, error) {
	probe.subject, probe.key, probe.request = subject, key, request
	return probe.result, probe.readErr
}

func (probe *projectAccountWritesProbe) AuthorizeConnectionAccountWrite(_ context.Context, subject integrationsdk.ConnectionAccountSubject, key string, operation integrationsdk.ConnectionAccountWriteOperation) (integrationsdk.ConnectionAccountWriteAccess, error) {
	probe.subject, probe.key, probe.operation = subject, key, operation
	return probe.access, probe.err
}

func TestProjectEngineAuthorizesPersonalConnectionWriteWithoutSending(t *testing.T) {
	probe := &projectAccountWritesProbe{access: integrationsdk.ConnectionAccountWriteAccess{Source: integrationsdk.ConnectionAccountWriteSource{ConnectionKey: "gmail-alice", ProviderKey: "google"}}}
	engine := &projectEngine{accountWrites: probe, principal: func(context.Context) (principalmodel.Principal, bool) {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "alice"}}, true
	}}
	access, err := engine.AuthorizeConnectionAccountWrite(t.Context(), " gmail-alice ", " mail_send ", " hash ")
	if err != nil || access.ConnectionKey != "gmail-alice" || access.ProviderKey != "google" {
		t.Fatalf("access=%#v err=%v", access, err)
	}
	if probe.subject.WorkspaceID != "workspace-a" || probe.subject.UserID != "alice" || !probe.subject.Access.Personal || probe.subject.Access.Workspace || probe.key != "gmail-alice" || probe.operation.Operation != "mail_send" || probe.operation.ContractSHA256 != "hash" {
		t.Fatalf("account authorization was not scoped to the authenticated user: %#v", probe)
	}
	probe.err = errors.New("account missing")
	if _, err = engine.AuthorizeConnectionAccountWrite(t.Context(), "gmail-alice", "mail_send", "hash"); runtimeengine.HTTPStatus(err) != 403 {
		t.Fatalf("missing account did not fail closed: %v", err)
	}
}

func TestProjectEngineConnectionWriteAuthorizationRequiresBindingAndPrincipal(t *testing.T) {
	engine := &projectEngine{principal: func(context.Context) (principalmodel.Principal, bool) {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "alice"}}, true
	}}
	if _, err := engine.AuthorizeConnectionAccountWrite(t.Context(), "gmail-alice", "mail_send", "hash"); runtimeengine.HTTPStatus(err) != 503 {
		t.Fatalf("missing Integration binding did not fail closed: %v", err)
	}
	engine.principal = func(context.Context) (principalmodel.Principal, bool) { return principalmodel.Principal{}, false }
	if _, err := engine.AuthorizeConnectionAccountWrite(t.Context(), "gmail-alice", "mail_send", "hash"); runtimeengine.HTTPStatus(err) != 401 {
		t.Fatalf("missing principal did not fail closed: %v", err)
	}
}

func TestProjectEngineReadsCurrentPersonalConnectionAndKeepsIdentityHostOwned(t *testing.T) {
	source := integrationsdk.ConnectionAccountReadSource{WorkspaceID: "workspace-a", ConnectionKey: "gmail-alice", ConnectorKey: "google_workspace", ProviderKey: "google", AccountUpdatedAt: "revision-a", Operation: "mail_attachment_download", ContractSHA256: "hash"}
	probe := &projectAccountReadsProbe{
		access: integrationsdk.ConnectionAccountReadAccess{Source: source},
		result: integrationsdk.ConnectionAccountReadResult{Source: source, PayloadAvailable: true, Payload: json.RawMessage(`{"data_base64":"cHJpdmF0ZQ=="}`)},
	}
	engine := &projectEngine{accountReads: probe, principal: func(context.Context) (principalmodel.Principal, bool) {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "alice"}}, true
	}}
	result, err := engine.ReadConnectionAccount(t.Context(), " gmail-alice ", " mail_attachment_download ", " hash ", map[string]string{"message_id": "message-a"})
	if err != nil || result.ConnectionKey != "gmail-alice" || result.ProviderKey != "google" || string(result.Payload) != `{"data_base64":"cHJpdmF0ZQ=="}` {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if probe.subject.WorkspaceID != "workspace-a" || probe.subject.UserID != "alice" || !probe.subject.Access.Personal || probe.subject.Access.Workspace || probe.key != "gmail-alice" || probe.operation.Operation != "mail_attachment_download" || probe.request.RequestID == "" || string(probe.request.Payload) != `{"message_id":"message-a"}` {
		t.Fatalf("account read was not bound to the authenticated principal and request: %#v", probe)
	}
}

func TestProjectEngineConnectionReadFailsClosed(t *testing.T) {
	principal := func(context.Context) (principalmodel.Principal, bool) {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "alice"}}, true
	}
	if _, err := (&projectEngine{principal: principal}).ReadConnectionAccount(t.Context(), "gmail", "read", "hash", map[string]any{}); runtimeengine.HTTPStatus(err) != 503 {
		t.Fatalf("missing read binding did not fail closed: %v", err)
	}
	probe := &projectAccountReadsProbe{authorizeErr: errors.New("not owner")}
	if _, err := (&projectEngine{principal: principal, accountReads: probe}).ReadConnectionAccount(t.Context(), "gmail", "read", "hash", map[string]any{}); runtimeengine.HTTPStatus(err) != 403 {
		t.Fatalf("authorization failure did not fail closed: %v", err)
	}
	probe.authorizeErr = nil
	probe.access.Source = integrationsdk.ConnectionAccountReadSource{ConnectionKey: "gmail", Operation: "read", ContractSHA256: "hash"}
	probe.readErr = errors.New("provider failed")
	if _, err := (&projectEngine{principal: principal, accountReads: probe}).ReadConnectionAccount(t.Context(), "gmail", "read", "hash", map[string]any{}); runtimeengine.HTTPStatus(err) != 503 {
		t.Fatalf("provider failure was not hidden: %v", err)
	}
}
