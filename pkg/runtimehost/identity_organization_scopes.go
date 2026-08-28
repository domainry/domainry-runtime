package runtimehost

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/party"
)

func projectIdentityDatabaseHandle(database *bootstrap.ProjectDatabase, filePath string) identitysdk.DatabaseHandle {
	partyStore := partypersistence.NewSQLPartyStore(database.DB(), database.Driver(), database.DatabaseSchema())
	return identitysdk.DatabaseHandle{
		Pool: database.DB(), Driver: database.Driver(), Schema: database.DatabaseSchema(), FilePath: filePath,
		OrganizationScopes: runtimeOrganizationScopeResolver{resolve: partyStore.ResolveIdentityOrganizationScopes},
	}
}

type runtimeOrganizationScopeResolver struct {
	resolve func(context.Context, string, []string) (partymodel.OrganizationScopeFacts, error)
}

func (r runtimeOrganizationScopeResolver) ResolveIdentityOrganizationScopes(ctx context.Context, workspaceID string, workforceProfileIDs []string) (identitysdk.OrganizationScopes, error) {
	facts, err := r.resolve(ctx, workspaceID, workforceProfileIDs)
	if err != nil {
		return identitysdk.OrganizationScopes{}, err
	}
	return identitysdk.OrganizationScopes{
		TeamIDs: append([]string(nil), facts.TeamIDs...), StoreIDs: append([]string(nil), facts.StoreIDs...),
		TerritoryIDs: append([]string(nil), facts.TerritoryIDs...), WarehouseIDs: append([]string(nil), facts.WarehouseIDs...),
	}, nil
}
