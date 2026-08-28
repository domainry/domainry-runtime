package repository

import (
	"context"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

type PartyRepository interface {
	List(context.Context, string) ([]partymodel.Aggregate, error)
	Get(context.Context, string, string) (partymodel.Aggregate, bool, error)
	Upsert(context.Context, string, partymodel.Aggregate) (partymodel.Aggregate, error)
}

type PartyCatalogRepository interface {
	ListJobs(context.Context, string) ([]partymodel.JobCatalogItem, error)
	GetJob(context.Context, string, string) (partymodel.JobCatalogItem, bool, error)
	UpsertJob(context.Context, string, partymodel.JobCatalogItem) (partymodel.JobCatalogItem, error)
	ListPositions(context.Context, string) ([]partymodel.Position, error)
	GetPosition(context.Context, string, string) (partymodel.Position, bool, error)
	UpsertPosition(context.Context, string, partymodel.Position) (partymodel.Position, error)
	ListOrganizationExtensions(context.Context, string) ([]partymodel.OrganizationExtension, error)
	GetOrganizationExtension(context.Context, string, string) (partymodel.OrganizationExtension, bool, error)
	UpsertOrganizationExtension(context.Context, string, partymodel.OrganizationExtension) (partymodel.OrganizationExtension, error)
	ListOrganizationExtensionMemberships(context.Context, string, string) ([]partymodel.OrganizationExtensionMembership, error)
	UpsertOrganizationExtensionMembership(context.Context, string, partymodel.OrganizationExtensionMembership) (partymodel.OrganizationExtensionMembership, error)
}
