package party

import (
	"context"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyservice "github.com/domainry/domainry-runtime/runtime/domain/party/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type PartyCatalogApplicationService struct {
	domain *partyservice.PartyCatalogDomainService
}

func NewPartyCatalogApplicationService(domain *partyservice.PartyCatalogDomainService) *PartyCatalogApplicationService {
	return &PartyCatalogApplicationService{domain: domain}
}

func (s *PartyCatalogApplicationService) ListJobs(ctx context.Context, principal principalmodel.Principal) ([]partymodel.JobCatalogItem, error) {
	if err := authorizePartyCatalog(principal, "party.read"); err != nil {
		return nil, err
	}
	return s.domain.ListJobs(ctx, principal.WorkspaceID)
}

func (s *PartyCatalogApplicationService) GetJob(ctx context.Context, id string, principal principalmodel.Principal) (partymodel.JobCatalogItem, bool, error) {
	if err := authorizePartyCatalog(principal, "party.read"); err != nil {
		return partymodel.JobCatalogItem{}, false, err
	}
	return s.domain.GetJob(ctx, principal.WorkspaceID, id)
}

func (s *PartyCatalogApplicationService) UpsertJob(ctx context.Context, value partymodel.JobCatalogItem, principal principalmodel.Principal) (partymodel.JobCatalogItem, error) {
	if err := authorizePartyCatalog(principal, "party.write"); err != nil {
		return partymodel.JobCatalogItem{}, err
	}
	return s.domain.UpsertJob(ctx, principal.WorkspaceID, value)
}

func (s *PartyCatalogApplicationService) ListPositions(ctx context.Context, principal principalmodel.Principal) ([]partymodel.Position, error) {
	if err := authorizePartyCatalog(principal, "party.read"); err != nil {
		return nil, err
	}
	return s.domain.ListPositions(ctx, principal.WorkspaceID)
}

func (s *PartyCatalogApplicationService) GetPosition(ctx context.Context, id string, principal principalmodel.Principal) (partymodel.Position, bool, error) {
	if err := authorizePartyCatalog(principal, "party.read"); err != nil {
		return partymodel.Position{}, false, err
	}
	return s.domain.GetPosition(ctx, principal.WorkspaceID, id)
}

func (s *PartyCatalogApplicationService) UpsertPosition(ctx context.Context, value partymodel.Position, principal principalmodel.Principal) (partymodel.Position, error) {
	if err := authorizePartyCatalog(principal, "party.write"); err != nil {
		return partymodel.Position{}, err
	}
	return s.domain.UpsertPosition(ctx, principal.WorkspaceID, value)
}

func (s *PartyCatalogApplicationService) ListOrganizationExtensions(ctx context.Context, principal principalmodel.Principal) ([]partymodel.OrganizationExtension, error) {
	if err := authorizePartyCatalog(principal, "party.read"); err != nil {
		return nil, err
	}
	return s.domain.ListOrganizationExtensions(ctx, principal.WorkspaceID)
}

func (s *PartyCatalogApplicationService) UpsertOrganizationExtension(ctx context.Context, value partymodel.OrganizationExtension, principal principalmodel.Principal) (partymodel.OrganizationExtension, error) {
	if err := authorizePartyCatalog(principal, "party.write"); err != nil {
		return partymodel.OrganizationExtension{}, err
	}
	return s.domain.UpsertOrganizationExtension(ctx, principal.WorkspaceID, value)
}

func (s *PartyCatalogApplicationService) ListOrganizationExtensionMemberships(ctx context.Context, workforceProfileID string, principal principalmodel.Principal) ([]partymodel.OrganizationExtensionMembership, error) {
	if err := authorizePartyCatalog(principal, "party.read"); err != nil {
		return nil, err
	}
	return s.domain.ListOrganizationExtensionMemberships(ctx, principal.WorkspaceID, workforceProfileID)
}

func (s *PartyCatalogApplicationService) UpsertOrganizationExtensionMembership(ctx context.Context, value partymodel.OrganizationExtensionMembership, principal principalmodel.Principal) (partymodel.OrganizationExtensionMembership, error) {
	if err := authorizePartyCatalog(principal, "party.write"); err != nil {
		return partymodel.OrganizationExtensionMembership{}, err
	}
	return s.domain.UpsertOrganizationExtensionMembership(ctx, principal.WorkspaceID, value)
}
