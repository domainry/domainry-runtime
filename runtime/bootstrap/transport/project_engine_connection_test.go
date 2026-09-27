package transport

import (
	"context"
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
