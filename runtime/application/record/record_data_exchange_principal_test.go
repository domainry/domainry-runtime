package record

import (
	"context"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestDataExchangePrincipalBridgesAuthenticatedScopeBeforeResolution(t *testing.T) {
	for _, existingWorkspace := range []string{"", "unrelated-workspace"} {
		t.Run(existingWorkspace, func(t *testing.T) {
			ctx := requestcontext.WithWorkspaceID(t.Context(), existingWorkspace)
			ctx = requestcontext.WithActorID(ctx, "unrelated-actor")
			providers := NewDataExchangeProviders(func(ctx context.Context, actor, role string) principalmodel.Principal {
				if requestcontext.WorkspaceID(ctx) != "workspace-a" || requestcontext.ActorID(ctx) != "member-a" || actor != "member-a" || role != "member" {
					t.Fatal("resolver did not receive authenticated job scope")
				}
				return principalmodel.NewPrincipalFromIdentity(identitysdk.Principal{Known: true}, "")
			})
			principal := providers.ResolvePrincipal(ctx, dataexchange.Scope{WorkspaceID: "workspace-a", ActorID: "member-a", RoleKey: "member", RequestID: "request-a"})
			if !principal.Known || principal.WorkspaceID != "workspace-a" || principal.UserID != "member-a" || principal.RequestID != "request-a" {
				t.Fatalf("principal=%+v", principal)
			}
			if requestcontext.WorkspaceID(ctx) != existingWorkspace {
				t.Fatal("caller context was mutated")
			}
		})
	}
}

func TestDataExchangePrincipalDoesNotAuthorizeFailedResolution(t *testing.T) {
	providers := NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principalmodel.Principal{} })
	if principal := providers.ResolvePrincipal(t.Context(), dataexchange.Scope{WorkspaceID: "workspace-a", ActorID: "member-a"}); principal.Known {
		t.Fatal("failed identity resolution was authorized by scope enrichment")
	}
}
