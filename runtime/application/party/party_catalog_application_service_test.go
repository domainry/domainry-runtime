package party

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	partymodel "github.com/domainry/domainry-party-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	partysdkfixture "github.com/domainry/domainry-runtime/testsupport/partysdkfixture"
)

type applicationCatalogRepository struct {
	jobs        map[string]partymodel.JobCatalogItem
	positions   map[string]partymodel.Position
	extensions  map[string]partymodel.OrganizationExtension
	memberships map[string]partymodel.OrganizationExtensionMembership
}

func (r *applicationCatalogRepository) ListOrganizationExtensions(context.Context, string) ([]partymodel.OrganizationExtension, error) {
	values := []partymodel.OrganizationExtension{}
	for _, value := range r.extensions {
		values = append(values, value)
	}
	return values, nil
}
func (r *applicationCatalogRepository) GetOrganizationExtension(_ context.Context, _, id string) (partymodel.OrganizationExtension, bool, error) {
	value, found := r.extensions[id]
	return value, found, nil
}
func (r *applicationCatalogRepository) UpsertOrganizationExtension(_ context.Context, _ string, value partymodel.OrganizationExtension) (partymodel.OrganizationExtension, error) {
	r.extensions[value.ID] = value
	return value, nil
}
func (r *applicationCatalogRepository) ListOrganizationExtensionMemberships(context.Context, string, string) ([]partymodel.OrganizationExtensionMembership, error) {
	values := []partymodel.OrganizationExtensionMembership{}
	for _, value := range r.memberships {
		values = append(values, value)
	}
	return values, nil
}
func (r *applicationCatalogRepository) UpsertOrganizationExtensionMembership(_ context.Context, _ string, value partymodel.OrganizationExtensionMembership) (partymodel.OrganizationExtensionMembership, error) {
	r.memberships[value.ID] = value
	return value, nil
}

func (r *applicationCatalogRepository) ListJobs(context.Context, string) ([]partymodel.JobCatalogItem, error) {
	values := []partymodel.JobCatalogItem{}
	for _, value := range r.jobs {
		values = append(values, value)
	}
	return values, nil
}
func (r *applicationCatalogRepository) GetJob(_ context.Context, _, id string) (partymodel.JobCatalogItem, bool, error) {
	value, found := r.jobs[id]
	return value, found, nil
}
func (r *applicationCatalogRepository) UpsertJob(_ context.Context, _ string, value partymodel.JobCatalogItem) (partymodel.JobCatalogItem, error) {
	r.jobs[value.ID] = value
	return value, nil
}
func (r *applicationCatalogRepository) ListPositions(context.Context, string) ([]partymodel.Position, error) {
	values := []partymodel.Position{}
	for _, value := range r.positions {
		values = append(values, value)
	}
	return values, nil
}
func (r *applicationCatalogRepository) GetPosition(_ context.Context, _, id string) (partymodel.Position, bool, error) {
	value, found := r.positions[id]
	return value, found, nil
}
func (r *applicationCatalogRepository) UpsertPosition(_ context.Context, _ string, value partymodel.Position) (partymodel.Position, error) {
	r.positions[value.ID] = value
	return value, nil
}

func TestPartyCatalogApplicationServiceAuthorizesLifecycle(t *testing.T) {
	repository := newApplicationCatalogRepository()
	service := NewPartyCatalogApplicationService(partysdkfixture.NewBinding("workspace", nil, repository))
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read", "party.write"}})
	if _, err := service.UpsertJob(t.Context(), partymodel.JobCatalogItem{ID: "job", Code: "J", Name: "Job"}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertPosition(t.Context(), partymodel.Position{ID: "position", Code: "P", Name: "Position", JobCatalogItemID: "job", Headcount: 1}, principal); err != nil {
		t.Fatal(err)
	}
	if values, err := service.ListJobs(t.Context(), principal); err != nil || len(values) != 1 {
		t.Fatalf("jobs=%#v err=%v", values, err)
	}
	if _, found, err := service.GetJob(t.Context(), "job", principal); err != nil || !found {
		t.Fatalf("job found=%v err=%v", found, err)
	}
	if values, err := service.ListPositions(t.Context(), principal); err != nil || len(values) != 1 {
		t.Fatalf("positions=%#v err=%v", values, err)
	}
	if _, found, err := service.GetPosition(t.Context(), "position", principal); err != nil || !found {
		t.Fatalf("position found=%v err=%v", found, err)
	}
	if _, err := service.UpsertOrganizationExtension(t.Context(), partymodel.OrganizationExtension{ID: "team", Kind: "team", Code: "T", Name: "Team"}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertOrganizationExtensionMembership(t.Context(), partymodel.OrganizationExtensionMembership{ID: "membership", ExtensionID: "team", WorkforceProfileID: "workforce"}, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListOrganizationExtensions(t.Context(), principal); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListOrganizationExtensionMemberships(t.Context(), "workforce", principal); err != nil {
		t.Fatal(err)
	}
}

func TestPartyCatalogApplicationServiceFailsClosed(t *testing.T) {
	repository := newApplicationCatalogRepository()
	service := NewPartyCatalogApplicationService(partysdkfixture.NewBinding("workspace", nil, repository))
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}
	checks := []error{}
	_, err := service.ListJobs(t.Context(), principal)
	checks = append(checks, err)
	_, _, err = service.GetJob(t.Context(), "job", principal)
	checks = append(checks, err)
	_, err = service.UpsertJob(t.Context(), partymodel.JobCatalogItem{}, principal)
	checks = append(checks, err)
	_, err = service.ListPositions(t.Context(), principal)
	checks = append(checks, err)
	_, _, err = service.GetPosition(t.Context(), "position", principal)
	checks = append(checks, err)
	_, err = service.UpsertPosition(t.Context(), partymodel.Position{}, principal)
	checks = append(checks, err)
	_, err = service.ListOrganizationExtensions(t.Context(), principal)
	checks = append(checks, err)
	_, err = service.UpsertOrganizationExtension(t.Context(), partymodel.OrganizationExtension{}, principal)
	checks = append(checks, err)
	_, err = service.ListOrganizationExtensionMemberships(t.Context(), "", principal)
	checks = append(checks, err)
	_, err = service.UpsertOrganizationExtensionMembership(t.Context(), partymodel.OrganizationExtensionMembership{}, principal)
	checks = append(checks, err)
	for _, err := range checks {
		if apperror.CodeOf(err) != "backend.permission.denied" {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestWorkspaceAdminManagesPartyFoundationCatalog(t *testing.T) {
	repository := newApplicationCatalogRepository()
	service := NewPartyCatalogApplicationService(partysdkfixture.NewBinding("workspace", nil, repository))
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})

	if _, err := service.UpsertOrganizationExtension(t.Context(), partymodel.OrganizationExtension{ID: "team", Kind: "team", Code: "TEAM", Name: "Team"}, admin); err != nil {
		t.Fatalf("workspace admin write extension: %v", err)
	}
	values, err := service.ListOrganizationExtensions(t.Context(), admin)
	if err != nil || len(values) != 1 || values[0].ID != "team" {
		t.Fatalf("workspace admin read extensions=%#v err=%v", values, err)
	}
}

func newApplicationCatalogRepository() *applicationCatalogRepository {
	return &applicationCatalogRepository{
		jobs: map[string]partymodel.JobCatalogItem{}, positions: map[string]partymodel.Position{},
		extensions: map[string]partymodel.OrganizationExtension{}, memberships: map[string]partymodel.OrganizationExtensionMembership{},
	}
}
