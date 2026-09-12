package auditmodule

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActorFromPrincipalPreservesRequestAndCorrelationIdentity(t *testing.T) {
	actor := ActorFromPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "member", AuthorizationRevision: "auth-r1"}, RequestID: "request-1", CorrelationID: "run-1"})
	if actor.WorkspaceID != "workspace-1" || actor.SubjectID != "user-1" || actor.RequestID != "request-1" || actor.CorrelationID != "run-1" || actor.AuthorizationRevision != "auth-r1" {
		t.Fatalf("actor=%+v", actor)
	}
}
