package runtimehost

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	partypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/party"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

func projectIdentityDatabaseHandle(database *bootstrap.ProjectDatabase, filePath string) identitysdk.DatabaseHandle {
	partyStore := partypersistence.NewSQLPartyStore(database.DB(), database.Driver(), database.DatabaseSchema())
	return identitysdk.DatabaseHandle{
		Pool: database.DB(), Driver: database.Driver(), Schema: database.DatabaseSchema(), FilePath: filePath,
		OrganizationScopeResolver: runtimeOrganizationScopeResolver(partyStore.ResolveIdentityOrganizationScopes),
		BusinessProfileResolver:   runtimeBusinessProfileResolver(database),
	}
}

func runtimeBusinessProfileResolver(database *bootstrap.ProjectDatabase) identitysdk.BusinessProfileResolver {
	metadata := metadatapersistence.NewMetadataStore(database)
	records := recordpersistence.NewRecordStore(database)
	return func(ctx context.Context, workspaceID, userID string) ([]identitysdk.BusinessProfileBinding, error) {
		manifest, err := metadata.LoadManifest(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve Identity business profile roles"))
		if err != nil {
			return nil, err
		}
		service := principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{
			Records:    records,
			Objects:    func() []definitionmodel.ObjectSchema { return manifest.Objects },
			Extensions: func() []profilebindingmodel.Binding { return manifest.IdentityProfileExtensions },
		})
		resolved, err := service.ResolveBusinessPrincipal(ctx, principalmodel.Principal{Principal: identitysdk.Principal{
			Known: true, WorkspaceID: workspaceID, UserID: userID,
		}}, "", "", "")
		if err != nil {
			return nil, err
		}
		out := make([]identitysdk.BusinessProfileBinding, 0, len(resolved.BusinessProfiles))
		for _, profile := range resolved.BusinessProfiles {
			out = append(out, identitysdk.BusinessProfileBinding{BindingKey: profile.BindingKey, ProfileID: profile.RecordID})
		}
		return out, nil
	}
}

func runtimeOrganizationScopeResolver(resolve func(context.Context, string, []string) (partymodel.OrganizationScopeFacts, error)) identitysdk.OrganizationScopeResolver {
	return func(ctx context.Context, workspaceID string, workforceProfileIDs []string) (identitysdk.OrganizationScopes, error) {
		facts, err := resolve(ctx, workspaceID, workforceProfileIDs)
		if err != nil {
			return identitysdk.OrganizationScopes{}, err
		}
		return identitysdk.OrganizationScopes{
			TeamIDs: append([]string(nil), facts.TeamIDs...), StoreIDs: append([]string(nil), facts.StoreIDs...),
			TerritoryIDs: append([]string(nil), facts.TerritoryIDs...), WarehouseIDs: append([]string(nil), facts.WarehouseIDs...),
		}, nil
	}
}
