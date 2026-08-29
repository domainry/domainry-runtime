package party

import (
	"context"

	partysdk "github.com/domainry/domainry-party-sdk"
	partymodel "github.com/domainry/domainry-party-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type PartyCatalogApplicationService struct{ catalog partysdk.Catalog }

func NewPartyCatalogApplicationService(binding partysdk.Binding) *PartyCatalogApplicationService {
	var catalog partysdk.Catalog
	if binding != nil {
		catalog = binding.Catalog()
	}
	return &PartyCatalogApplicationService{catalog: catalog}
}
func (s *PartyCatalogApplicationService) available() error {
	if s.catalog == nil {
		return partyUnavailable()
	}
	return nil
}
func (s *PartyCatalogApplicationService) ListJobs(ctx context.Context, p principalmodel.Principal) ([]partymodel.JobCatalogItem, error) {
	if e := authorizePartyCatalog(p, "party.read"); e != nil {
		return nil, e
	}
	if e := s.available(); e != nil {
		return nil, e
	}
	return s.catalog.ListJobs(ctx)
}
func (s *PartyCatalogApplicationService) GetJob(ctx context.Context, id string, p principalmodel.Principal) (partymodel.JobCatalogItem, bool, error) {
	if e := authorizePartyCatalog(p, "party.read"); e != nil {
		return partymodel.JobCatalogItem{}, false, e
	}
	if e := s.available(); e != nil {
		return partymodel.JobCatalogItem{}, false, e
	}
	return s.catalog.GetJob(ctx, id)
}
func (s *PartyCatalogApplicationService) UpsertJob(ctx context.Context, v partymodel.JobCatalogItem, p principalmodel.Principal) (partymodel.JobCatalogItem, error) {
	if e := authorizePartyCatalog(p, "party.write"); e != nil {
		return partymodel.JobCatalogItem{}, e
	}
	if e := s.available(); e != nil {
		return partymodel.JobCatalogItem{}, e
	}
	return s.catalog.UpsertJob(ctx, v)
}
func (s *PartyCatalogApplicationService) ListPositions(ctx context.Context, p principalmodel.Principal) ([]partymodel.Position, error) {
	if e := authorizePartyCatalog(p, "party.read"); e != nil {
		return nil, e
	}
	if e := s.available(); e != nil {
		return nil, e
	}
	return s.catalog.ListPositions(ctx)
}
func (s *PartyCatalogApplicationService) GetPosition(ctx context.Context, id string, p principalmodel.Principal) (partymodel.Position, bool, error) {
	if e := authorizePartyCatalog(p, "party.read"); e != nil {
		return partymodel.Position{}, false, e
	}
	if e := s.available(); e != nil {
		return partymodel.Position{}, false, e
	}
	return s.catalog.GetPosition(ctx, id)
}
func (s *PartyCatalogApplicationService) UpsertPosition(ctx context.Context, v partymodel.Position, p principalmodel.Principal) (partymodel.Position, error) {
	if e := authorizePartyCatalog(p, "party.write"); e != nil {
		return partymodel.Position{}, e
	}
	if e := s.available(); e != nil {
		return partymodel.Position{}, e
	}
	return s.catalog.UpsertPosition(ctx, v)
}
func (s *PartyCatalogApplicationService) ListOrganizationExtensions(ctx context.Context, p principalmodel.Principal) ([]partymodel.OrganizationExtension, error) {
	if e := authorizePartyCatalog(p, "party.read"); e != nil {
		return nil, e
	}
	if e := s.available(); e != nil {
		return nil, e
	}
	return s.catalog.ListOrganizationExtensions(ctx)
}
func (s *PartyCatalogApplicationService) UpsertOrganizationExtension(ctx context.Context, v partymodel.OrganizationExtension, p principalmodel.Principal) (partymodel.OrganizationExtension, error) {
	if e := authorizePartyCatalog(p, "party.write"); e != nil {
		return partymodel.OrganizationExtension{}, e
	}
	if e := s.available(); e != nil {
		return partymodel.OrganizationExtension{}, e
	}
	return s.catalog.UpsertOrganizationExtension(ctx, v)
}
func (s *PartyCatalogApplicationService) ListOrganizationExtensionMemberships(ctx context.Context, id string, p principalmodel.Principal) ([]partymodel.OrganizationExtensionMembership, error) {
	if e := authorizePartyCatalog(p, "party.read"); e != nil {
		return nil, e
	}
	if e := s.available(); e != nil {
		return nil, e
	}
	return s.catalog.ListOrganizationExtensionMemberships(ctx, id)
}
func (s *PartyCatalogApplicationService) UpsertOrganizationExtensionMembership(ctx context.Context, v partymodel.OrganizationExtensionMembership, p principalmodel.Principal) (partymodel.OrganizationExtensionMembership, error) {
	if e := authorizePartyCatalog(p, "party.write"); e != nil {
		return partymodel.OrganizationExtensionMembership{}, e
	}
	if e := s.available(); e != nil {
		return partymodel.OrganizationExtensionMembership{}, e
	}
	return s.catalog.UpsertOrganizationExtensionMembership(ctx, v)
}
