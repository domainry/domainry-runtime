// Package partysdkfixture adapts existing repository-shaped test doubles to
// the deployment-neutral Party SDK while Runtime completes its boundary tests.
package partysdkfixture

import (
	"context"

	partysdk "github.com/domainry/domainry-party-sdk"
	"github.com/domainry/domainry-party-sdk/contract"
)

type DirectoryRepository interface {
	List(context.Context, string) ([]contract.Aggregate, error)
	Get(context.Context, string, string) (contract.Aggregate, bool, error)
	Upsert(context.Context, string, contract.Aggregate) (contract.Aggregate, error)
}
type CatalogRepository interface {
	ListJobs(context.Context, string) ([]contract.JobCatalogItem, error)
	GetJob(context.Context, string, string) (contract.JobCatalogItem, bool, error)
	UpsertJob(context.Context, string, contract.JobCatalogItem) (contract.JobCatalogItem, error)
	ListPositions(context.Context, string) ([]contract.Position, error)
	GetPosition(context.Context, string, string) (contract.Position, bool, error)
	UpsertPosition(context.Context, string, contract.Position) (contract.Position, error)
	ListOrganizationExtensions(context.Context, string) ([]contract.OrganizationExtension, error)
	GetOrganizationExtension(context.Context, string, string) (contract.OrganizationExtension, bool, error)
	UpsertOrganizationExtension(context.Context, string, contract.OrganizationExtension) (contract.OrganizationExtension, error)
	ListOrganizationExtensionMemberships(context.Context, string, string) ([]contract.OrganizationExtensionMembership, error)
	UpsertOrganizationExtensionMembership(context.Context, string, contract.OrganizationExtensionMembership) (contract.OrganizationExtensionMembership, error)
}

type binding struct {
	workspace           string
	directoryRepository DirectoryRepository
	catalogRepository   CatalogRepository
}

func NewBinding(workspace string, directory DirectoryRepository, catalog CatalogRepository) partysdk.Binding {
	return &binding{workspace: workspace, directoryRepository: directory, catalogRepository: catalog}
}
func (*binding) Descriptor() partysdk.Descriptor {
	return partysdk.Descriptor{ProtocolVersion: partysdk.ProtocolVersionV1, Mode: partysdk.DeploymentModeModule}
}
func (b *binding) Directory() partysdk.Directory                 { return directory{b} }
func (b *binding) Catalog() partysdk.Catalog                     { return catalog{b} }
func (*binding) OrganizationScopes() partysdk.OrganizationScopes { return scopes{} }
func (*binding) Close(context.Context) error                     { return nil }

type directory struct{ *binding }

func (b directory) List(c context.Context) ([]contract.Aggregate, error) {
	return b.directoryRepository.List(c, b.workspace)
}
func (b directory) Get(c context.Context, id string) (contract.Aggregate, bool, error) {
	return b.directoryRepository.Get(c, b.workspace, id)
}
func (b directory) Upsert(c context.Context, v contract.Aggregate) (contract.Aggregate, error) {
	return b.directoryRepository.Upsert(c, b.workspace, v)
}

type catalog struct{ *binding }

func (b catalog) ListJobs(c context.Context) ([]contract.JobCatalogItem, error) {
	return b.catalogRepository.ListJobs(c, b.workspace)
}
func (b catalog) GetJob(c context.Context, id string) (contract.JobCatalogItem, bool, error) {
	return b.catalogRepository.GetJob(c, b.workspace, id)
}
func (b catalog) UpsertJob(c context.Context, v contract.JobCatalogItem) (contract.JobCatalogItem, error) {
	if v.Status == "" {
		v.Status = contract.PartyStatusActive
	}
	return b.catalogRepository.UpsertJob(c, b.workspace, v)
}
func (b catalog) ListPositions(c context.Context) ([]contract.Position, error) {
	return b.catalogRepository.ListPositions(c, b.workspace)
}
func (b catalog) GetPosition(c context.Context, id string) (contract.Position, bool, error) {
	return b.catalogRepository.GetPosition(c, b.workspace, id)
}
func (b catalog) UpsertPosition(c context.Context, v contract.Position) (contract.Position, error) {
	if v.Status == "" {
		v.Status = contract.PartyStatusActive
	}
	return b.catalogRepository.UpsertPosition(c, b.workspace, v)
}
func (b catalog) ListOrganizationExtensions(c context.Context) ([]contract.OrganizationExtension, error) {
	return b.catalogRepository.ListOrganizationExtensions(c, b.workspace)
}
func (b catalog) UpsertOrganizationExtension(c context.Context, v contract.OrganizationExtension) (contract.OrganizationExtension, error) {
	if v.Status == "" {
		v.Status = contract.PartyStatusActive
	}
	return b.catalogRepository.UpsertOrganizationExtension(c, b.workspace, v)
}
func (b catalog) ListOrganizationExtensionMemberships(c context.Context, id string) ([]contract.OrganizationExtensionMembership, error) {
	return b.catalogRepository.ListOrganizationExtensionMemberships(c, b.workspace, id)
}
func (b catalog) UpsertOrganizationExtensionMembership(c context.Context, v contract.OrganizationExtensionMembership) (contract.OrganizationExtensionMembership, error) {
	if v.Status == "" {
		v.Status = contract.PartyStatusActive
	}
	return b.catalogRepository.UpsertOrganizationExtensionMembership(c, b.workspace, v)
}

type scopes struct{}

func (scopes) Resolve(context.Context, []string) (contract.OrganizationScopeFacts, error) {
	return contract.OrganizationScopeFacts{}, nil
}
