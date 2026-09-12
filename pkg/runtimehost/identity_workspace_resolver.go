package runtimehost

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
)

type runtimeIdentityWorkspaceResolver struct{ database *bootstrap.ProjectDatabase }

func (resolver runtimeIdentityWorkspaceResolver) ResolveWorkspace(ctx context.Context, reference identitysdk.WorkspaceID) (identitysdk.WorkspaceID, error) {
	id, err := workspaceprovision.ResolveActiveIdentityWorkspace(ctx, resolver.database, string(reference))
	return identitysdk.WorkspaceID(id), err
}
