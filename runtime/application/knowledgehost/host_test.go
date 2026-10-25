package knowledgehost

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
)

type principalResolverFunc func(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error)

func (resolve principalResolverFunc) Resolve(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	return resolve(ctx, request)
}

func TestResolveAcceptsIdentityValidatedExternalWorkspace(t *testing.T) {
	host := &Host{
		runtimeID: "runtime-a",
		application: identitysdk.ApplicationScope{
			WorkspaceID:    "installation-workspace",
			ApplicationKey: "knowledge",
		},
		principals: principalResolverFunc(func(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
			workspaceID := requestcontext.WorkspaceID(ctx)
			principal := identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: string(request.SubjectID), RoleKey: request.RoleKey}
			return identitysdk.PrincipalResolution{Principal: principal}, nil
		}),
	}

	principal, err := host.resolve(t.Context(), knowledgesdk.Authority{
		Known: true, RuntimeID: "runtime-a", WorkspaceID: "external-team-workspace", UserID: "user-a", RoleKey: "knowledge_member",
	})
	if err != nil {
		t.Fatalf("resolve external Workspace principal: %v", err)
	}
	if principal.WorkspaceID != "external-team-workspace" || principal.UserID != "user-a" {
		t.Fatalf("unexpected principal: %#v", principal)
	}
}

func TestResolveRejectsPrincipalFromDifferentWorkspace(t *testing.T) {
	host := &Host{
		runtimeID: "runtime-a",
		application: identitysdk.ApplicationScope{
			WorkspaceID:    "installation-workspace",
			ApplicationKey: "knowledge",
		},
		principals: principalResolverFunc(func(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
			return identitysdk.PrincipalResolution{Principal: identitysdk.Principal{Known: true, WorkspaceID: "other-workspace", UserID: "user-a"}}, nil
		}),
	}

	if _, err := host.resolve(t.Context(), knowledgesdk.Authority{
		Known: true, RuntimeID: "runtime-a", WorkspaceID: "external-team-workspace", UserID: "user-a",
	}); err == nil {
		t.Fatal("expected cross-Workspace principal to be denied")
	}
}
