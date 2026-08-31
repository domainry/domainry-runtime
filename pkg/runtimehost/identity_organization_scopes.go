package runtimehost

import (
	"context"
	"fmt"
	"strings"
	"sync"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

func projectIdentityDatabaseHandle(database *bootstrap.ProjectDatabase, filePath string, profile *runtimeBusinessProfileProjection, scopes *partyOrganizationScopeProjection) identitysdk.DatabaseHandle {
	var profileResolver identitysdk.BusinessProfileResolver
	if profile != nil {
		profileResolver = profile.Resolve
	}
	var scopeResolver identitysdk.OrganizationScopeResolver
	if scopes != nil {
		scopeResolver = scopes.Resolve
	}
	return identitysdk.DatabaseHandle{Pool: database.DB(), Driver: database.Driver(), Schema: database.DatabaseSchema(), FilePath: filePath, OrganizationScopeResolver: scopeResolver, BusinessProfileResolver: profileResolver, Migrations: database}
}

// partyOrganizationScopeProjection breaks the assembly-time cycle without
// introducing a domain dependency. Identity retains this stable function;
// Runtime Host publishes the selected Party Binding before serving requests.
type partyOrganizationScopeProjection struct {
	mu          sync.RWMutex
	workspaceID string
	scopes      partysdk.OrganizationScopes
}

func (p *partyOrganizationScopeProjection) Bind(workspaceID string, scopes partysdk.OrganizationScopes) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workspaceID = strings.TrimSpace(workspaceID)
	p.scopes = scopes
}
func (p *partyOrganizationScopeProjection) Resolve(ctx context.Context, workspaceID string, profileIDs []string) (identitysdk.OrganizationScopes, error) {
	p.mu.RLock()
	expected, scopes := p.workspaceID, p.scopes
	p.mu.RUnlock()
	if scopes == nil {
		return identitysdk.OrganizationScopes{}, fmt.Errorf("Party organization scopes are unavailable")
	}
	if strings.TrimSpace(workspaceID) != expected {
		return identitysdk.OrganizationScopes{}, fmt.Errorf("Party organization scope workspace mismatch")
	}
	facts, err := scopes.Resolve(ctx, profileIDs)
	if err != nil {
		return identitysdk.OrganizationScopes{}, err
	}
	return identitysdk.OrganizationScopes{TeamIDs: append([]string(nil), facts.TeamIDs...), StoreIDs: append([]string(nil), facts.StoreIDs...), TerritoryIDs: append([]string(nil), facts.TerritoryIDs...), WarehouseIDs: append([]string(nil), facts.WarehouseIDs...)}, nil
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
	service := principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{Records: p.records, Objects: func() []definitionmodel.ObjectSchema { return objects }, Extensions: func() []profilebindingmodel.Binding { return extensions }})
	resolved, err := service.ResolveBusinessPrincipal(ctx, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: userID}}, "", "")
	if err != nil {
		return nil, err
	}
	out := make([]identitysdk.BusinessProfileBinding, 0, len(resolved.BusinessProfiles))
	for _, profile := range resolved.BusinessProfiles {
		out = append(out, identitysdk.BusinessProfileBinding{BindingKey: profile.BindingKey, ProfileID: profile.RecordID})
	}
	return out, nil
}
