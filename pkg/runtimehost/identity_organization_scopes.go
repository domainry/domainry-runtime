package runtimehost

import (
	"context"
	"sync"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	partypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/party"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

func projectIdentityDatabaseHandle(database *bootstrap.ProjectDatabase, filePath string, profiles ...*runtimeBusinessProfileProjection) identitysdk.DatabaseHandle {
	partyStore := partypersistence.NewSQLPartyStore(database.DB(), database.Driver(), database.DatabaseSchema())
	var profileResolver identitysdk.BusinessProfileResolver
	if len(profiles) > 0 && profiles[0] != nil {
		profileResolver = profiles[0].Resolve
	}
	return identitysdk.DatabaseHandle{
		Pool: database.DB(), Driver: database.Driver(), Schema: database.DatabaseSchema(), FilePath: filePath,
		OrganizationScopeResolver: runtimeOrganizationScopeResolver(partyStore.ResolveIdentityOrganizationScopes),
		BusinessProfileResolver:   profileResolver,
	}
}

type runtimeBusinessProfileProjection struct {
	mu         sync.RWMutex
	records    recordpersistence.RecordStore
	objects    []definitionmodel.ObjectSchema
	extensions []profilebindingmodel.Binding
}

func newRuntimeBusinessProfileProjection(database *bootstrap.ProjectDatabase) *runtimeBusinessProfileProjection {
	return &runtimeBusinessProfileProjection{records: recordpersistence.NewRecordStore(database)}
}

func (p *runtimeBusinessProfileProjection) Publish(objects []definitionmodel.ObjectSchema, extensions []profilebindingmodel.Binding) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.objects = append([]definitionmodel.ObjectSchema(nil), objects...)
	p.extensions = append([]profilebindingmodel.Binding(nil), extensions...)
}

func (p *runtimeBusinessProfileProjection) Resolve(ctx context.Context, workspaceID, userID string) ([]identitysdk.BusinessProfileBinding, error) {
	if p == nil {
		return nil, nil
	}
	p.mu.RLock()
	objects := append([]definitionmodel.ObjectSchema(nil), p.objects...)
	extensions := append([]profilebindingmodel.Binding(nil), p.extensions...)
	p.mu.RUnlock()
	service := principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{
		Records: p.records, Objects: func() []definitionmodel.ObjectSchema { return objects }, Extensions: func() []profilebindingmodel.Binding { return extensions },
	})
	resolved, err := service.ResolveBusinessPrincipal(ctx, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: userID}}, "", "", "")
	if err != nil {
		return nil, err
	}
	out := make([]identitysdk.BusinessProfileBinding, 0, len(resolved.BusinessProfiles))
	for _, profile := range resolved.BusinessProfiles {
		out = append(out, identitysdk.BusinessProfileBinding{BindingKey: profile.BindingKey, ProfileID: profile.RecordID})
	}
	return out, nil
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
