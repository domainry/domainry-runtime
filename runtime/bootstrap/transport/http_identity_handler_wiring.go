package transport

import (
	"context"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	partyapplication "github.com/domainry/domainry-runtime/runtime/application/party"
	partyservice "github.com/domainry/domainry-runtime/runtime/domain/party/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	partypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/party"
	partyhttp "github.com/domainry/domainry-runtime/runtime/transport/http/party"
)

func (a *httpServerAssembly) wirePartyAndIdentityReferences(constructionContext context.Context) {
	records := a.dependencies.Records
	a.wirePartyHandler()
	if directory := a.dependencies.IdentityBinding.Directory(); directory != nil {
		records.Applications().AuthoringCapabilities.UseIdentityReferenceSource(constructionContext, identitySDKCapabilityReferenceSource(directory))
	}
}

func (a *httpServerAssembly) wirePartyHandler() {
	if a.dependencies.Store != nil {
		partyStore := partypersistence.NewSQLPartyStore(a.dependencies.Store.DB(), a.dependencies.Store.Driver(), a.dependencies.Store.DatabaseSchema())
		a.handlers.Party = partyhttp.NewPartyHandler(partyhttp.PartyDependencies{
			Service:       partyapplication.NewPartyApplicationService(partyservice.NewPartyDomainService(partyStore)),
			Catalog:       partyapplication.NewPartyCatalogApplicationService(partyservice.NewPartyCatalogDomainService(partyStore)),
			Authenticated: a.identityHTTP.AuthenticatedFunc, Principal: a.callbacks.Principal,
			WriteJSON: a.callbacks.WriteJSON, WriteError: a.callbacks.WriteError,
			WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
			SecurityAudit: a.callbacks.SecurityAudit,
		})
	}
}

func identitySDKCapabilityReferenceSource(directory identitysdk.Directory) func(context.Context, principalmodel.Principal) (capabilityapplication.CapabilityIdentityReferences, error) {
	return func(ctx context.Context, _ principalmodel.Principal) (capabilityapplication.CapabilityIdentityReferences, error) {
		users, err := directory.ListUsers(ctx, identitysdk.DirectoryQuery{})
		if err != nil {
			return capabilityapplication.CapabilityIdentityReferences{}, err
		}
		roles, err := directory.ListRoles(ctx, identitysdk.DirectoryQuery{})
		if err != nil {
			return capabilityapplication.CapabilityIdentityReferences{}, err
		}
		workforce, err := directory.ListWorkforce(ctx, identitysdk.DirectoryQuery{})
		if err != nil {
			return capabilityapplication.CapabilityIdentityReferences{}, err
		}
		references := capabilityapplication.CapabilityIdentityReferences{
			UserIDs: make([]string, 0, len(users)), RoleIDs: make([]string, 0, len(roles)),
			WorkforceProfileIDs: make([]string, 0, len(workforce)), DepartmentIDs: make([]string, 0, len(workforce)),
		}
		for _, user := range users {
			references.UserIDs = append(references.UserIDs, user.ID)
		}
		for _, role := range roles {
			references.RoleIDs = append(references.RoleIDs, role.ID)
		}
		departments := map[string]struct{}{}
		for _, entry := range workforce {
			if value := strings.TrimSpace(entry.WorkforceProfileID); value != "" {
				references.WorkforceProfileIDs = append(references.WorkforceProfileIDs, value)
			}
			if value := strings.TrimSpace(entry.OrganizationUnitID); value != "" {
				departments[value] = struct{}{}
			}
		}
		for departmentID := range departments {
			references.DepartmentIDs = append(references.DepartmentIDs, departmentID)
		}
		sort.Strings(references.UserIDs)
		sort.Strings(references.RoleIDs)
		sort.Strings(references.WorkforceProfileIDs)
		sort.Strings(references.DepartmentIDs)
		return references, nil
	}
}
