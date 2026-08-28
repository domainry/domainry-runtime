package service

import (
	"context"
	"errors"
	"testing"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type partyCatalogRepositoryStub struct {
	jobs        map[string]partymodel.JobCatalogItem
	positions   map[string]partymodel.Position
	extensions  map[string]partymodel.OrganizationExtension
	memberships map[string]partymodel.OrganizationExtensionMembership
	err         error
}

func (s *partyCatalogRepositoryStub) ListOrganizationExtensions(context.Context, string) ([]partymodel.OrganizationExtension, error) {
	values := []partymodel.OrganizationExtension{}
	for _, value := range s.extensions {
		values = append(values, value)
	}
	return values, s.err
}
func (s *partyCatalogRepositoryStub) GetOrganizationExtension(_ context.Context, _, id string) (partymodel.OrganizationExtension, bool, error) {
	value, found := s.extensions[id]
	return value, found, s.err
}
func (s *partyCatalogRepositoryStub) UpsertOrganizationExtension(_ context.Context, _ string, value partymodel.OrganizationExtension) (partymodel.OrganizationExtension, error) {
	s.extensions[value.ID] = value
	return value, s.err
}
func (s *partyCatalogRepositoryStub) ListOrganizationExtensionMemberships(_ context.Context, _, profileID string) ([]partymodel.OrganizationExtensionMembership, error) {
	values := []partymodel.OrganizationExtensionMembership{}
	for _, value := range s.memberships {
		if profileID == "" || value.WorkforceProfileID == profileID {
			values = append(values, value)
		}
	}
	return values, s.err
}
func (s *partyCatalogRepositoryStub) UpsertOrganizationExtensionMembership(_ context.Context, _ string, value partymodel.OrganizationExtensionMembership) (partymodel.OrganizationExtensionMembership, error) {
	s.memberships[value.ID] = value
	return value, s.err
}

func (s *partyCatalogRepositoryStub) ListJobs(context.Context, string) ([]partymodel.JobCatalogItem, error) {
	return mapValues(s.jobs), s.err
}
func (s *partyCatalogRepositoryStub) GetJob(_ context.Context, _, id string) (partymodel.JobCatalogItem, bool, error) {
	value, found := s.jobs[id]
	return value, found, s.err
}
func (s *partyCatalogRepositoryStub) UpsertJob(_ context.Context, _ string, value partymodel.JobCatalogItem) (partymodel.JobCatalogItem, error) {
	s.jobs[value.ID] = value
	return value, s.err
}
func (s *partyCatalogRepositoryStub) ListPositions(context.Context, string) ([]partymodel.Position, error) {
	values := []partymodel.Position{}
	for _, value := range s.positions {
		values = append(values, value)
	}
	return values, s.err
}
func (s *partyCatalogRepositoryStub) GetPosition(_ context.Context, _, id string) (partymodel.Position, bool, error) {
	value, found := s.positions[id]
	return value, found, s.err
}
func (s *partyCatalogRepositoryStub) UpsertPosition(_ context.Context, _ string, value partymodel.Position) (partymodel.Position, error) {
	s.positions[value.ID] = value
	return value, s.err
}

func mapValues(values map[string]partymodel.JobCatalogItem) []partymodel.JobCatalogItem {
	out := []partymodel.JobCatalogItem{}
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func TestPartyCatalogDomainServiceLifecycle(t *testing.T) {
	repository := newPartyCatalogRepositoryStub()
	service := NewPartyCatalogDomainService(repository)
	job, err := service.UpsertJob(t.Context(), "workspace", partymodel.JobCatalogItem{ID: " job ", Code: " ENG ", Name: " Engineer ", Family: " Engineering "})
	if err != nil || job.ID != "job" || job.Code != "ENG" || job.Status != "active" {
		t.Fatalf("job=%#v err=%v", job, err)
	}
	position, err := service.UpsertPosition(t.Context(), "workspace", partymodel.Position{
		ID: " position ", Code: " ENG-1 ", Name: " Engineer 1 ", JobCatalogItemID: " job ", Headcount: 2,
	})
	if err != nil || position.ID != "position" || position.JobCatalogItemID != "job" || position.Status != "active" {
		t.Fatalf("position=%#v err=%v", position, err)
	}
	if jobs, err := service.ListJobs(t.Context(), "workspace"); err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	if loaded, found, err := service.GetJob(t.Context(), "workspace", " job "); err != nil || !found || loaded.ID != "job" {
		t.Fatalf("job=%#v found=%v err=%v", loaded, found, err)
	}
	if positions, err := service.ListPositions(t.Context(), "workspace"); err != nil || len(positions) != 1 {
		t.Fatalf("positions=%#v err=%v", positions, err)
	}
	if loaded, found, err := service.GetPosition(t.Context(), "workspace", " position "); err != nil || !found || loaded.ID != "position" {
		t.Fatalf("position=%#v found=%v err=%v", loaded, found, err)
	}
}

func TestPartyCatalogDomainServiceRejectsInvalidInput(t *testing.T) {
	repository := newPartyCatalogRepositoryStub()
	service := NewPartyCatalogDomainService(repository)
	if _, err := service.UpsertJob(t.Context(), "workspace", partymodel.JobCatalogItem{}); apperror.CodeOf(err) != "backend.party.job_identity_required" {
		t.Fatalf("job identity error=%v", err)
	}
	if _, err := service.UpsertJob(t.Context(), "workspace", partymodel.JobCatalogItem{ID: "job", Code: "J", Name: "Job", Status: "unknown"}); apperror.CodeOf(err) != "backend.party.job_status_invalid" {
		t.Fatalf("job status error=%v", err)
	}
	if _, err := service.UpsertPosition(t.Context(), "workspace", partymodel.Position{}); apperror.CodeOf(err) != "backend.party.position_invalid" {
		t.Fatalf("position error=%v", err)
	}
	if _, err := service.UpsertPosition(t.Context(), "workspace", partymodel.Position{ID: "p", Code: "P", Name: "P", JobCatalogItemID: "missing", Headcount: 1}); apperror.CodeOf(err) != "backend.party.position_job_not_found" {
		t.Fatalf("missing job error=%v", err)
	}
	repository.jobs["job"] = partymodel.JobCatalogItem{ID: "job"}
	if _, err := service.UpsertPosition(t.Context(), "workspace", partymodel.Position{ID: "p", Code: "P", Name: "P", JobCatalogItemID: "job", Headcount: 1, Status: "unknown"}); apperror.CodeOf(err) != "backend.party.position_status_invalid" {
		t.Fatalf("position status error=%v", err)
	}
}

func TestPartyCatalogDomainServiceRemainingValidationOutcomes(t *testing.T) {
	service := NewPartyCatalogDomainService(newPartyCatalogRepositoryStub())
	ctx := t.Context()

	if _, err := service.ListJobs(ctx, " \t "); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("list jobs workspace error=%v", err)
	}
	if _, _, err := service.GetJob(ctx, "", "job"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("get job workspace error=%v", err)
	}
	if _, err := service.UpsertJob(ctx, "", partymodel.JobCatalogItem{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("upsert job workspace error=%v", err)
	}
	for _, test := range []struct {
		name  string
		value partymodel.JobCatalogItem
	}{
		{name: "code", value: partymodel.JobCatalogItem{ID: "job", Name: "Job"}},
		{name: "name", value: partymodel.JobCatalogItem{ID: "job", Code: "J"}},
	} {
		t.Run("job_"+test.name, func(t *testing.T) {
			if _, err := service.UpsertJob(ctx, "workspace", test.value); apperror.CodeOf(err) != "backend.party.job_identity_required" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if value, err := service.UpsertJob(ctx, "workspace", partymodel.JobCatalogItem{
		ID: "inactive", Code: "I", Name: "Inactive", Status: partymodel.PartyStatusInactive,
	}); err != nil || value.Status != partymodel.PartyStatusInactive {
		t.Fatalf("inactive job=%#v err=%v", value, err)
	}

	if _, err := service.ListPositions(ctx, " "); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("list positions workspace error=%v", err)
	}
	if _, _, err := service.GetPosition(ctx, "", "position"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("get position workspace error=%v", err)
	}
	if _, err := service.UpsertPosition(ctx, "", partymodel.Position{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("upsert position workspace error=%v", err)
	}
	validPosition := partymodel.Position{
		ID: "position", Code: "P", Name: "Position", JobCatalogItemID: "inactive", Headcount: 1,
	}
	for _, test := range []struct {
		name  string
		patch func(*partymodel.Position)
	}{
		{name: "code", patch: func(value *partymodel.Position) { value.Code = "" }},
		{name: "name", patch: func(value *partymodel.Position) { value.Name = "" }},
		{name: "job", patch: func(value *partymodel.Position) { value.JobCatalogItemID = "" }},
		{name: "headcount", patch: func(value *partymodel.Position) { value.Headcount = 0 }},
	} {
		t.Run("position_"+test.name, func(t *testing.T) {
			value := validPosition
			test.patch(&value)
			if _, err := service.UpsertPosition(ctx, "workspace", value); apperror.CodeOf(err) != "backend.party.position_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	validPosition.Status = partymodel.PartyStatusInactive
	if value, err := service.UpsertPosition(ctx, "workspace", validPosition); err != nil || value.Status != partymodel.PartyStatusInactive {
		t.Fatalf("inactive position=%#v err=%v", value, err)
	}
}

func TestPartyCatalogDomainServicePropagatesRepositoryFailures(t *testing.T) {
	fault := errors.New("fault")
	repository := newPartyCatalogRepositoryStub()
	repository.jobs["job"] = partymodel.JobCatalogItem{ID: "job"}
	repository.err = fault
	service := NewPartyCatalogDomainService(repository)
	if _, err := service.ListJobs(t.Context(), "workspace"); !errors.Is(err, fault) {
		t.Fatalf("list jobs error=%v", err)
	}
	if _, _, err := service.GetJob(t.Context(), "workspace", "job"); !errors.Is(err, fault) {
		t.Fatalf("get job error=%v", err)
	}
	if _, err := service.UpsertPosition(t.Context(), "workspace", partymodel.Position{ID: "p", Code: "P", Name: "P", JobCatalogItemID: "job", Headcount: 1}); !errors.Is(err, fault) {
		t.Fatalf("position error=%v", err)
	}
}

func newPartyCatalogRepositoryStub() *partyCatalogRepositoryStub {
	return &partyCatalogRepositoryStub{
		jobs: map[string]partymodel.JobCatalogItem{}, positions: map[string]partymodel.Position{},
		extensions: map[string]partymodel.OrganizationExtension{}, memberships: map[string]partymodel.OrganizationExtensionMembership{},
	}
}

func TestPartyOrganizationExtensionDomainLifecycle(t *testing.T) {
	repository := newPartyCatalogRepositoryStub()
	service := NewPartyCatalogDomainService(repository)
	territory, err := service.UpsertOrganizationExtension(t.Context(), "workspace", partymodel.OrganizationExtension{
		ID: " territory ", Kind: "territory", Code: " EAST ", Name: " East ",
	})
	if err != nil || territory.ID != "territory" || territory.Code != "EAST" || territory.Status != "active" {
		t.Fatalf("territory=%#v err=%v", territory, err)
	}
	child, err := service.UpsertOrganizationExtension(t.Context(), "workspace", partymodel.OrganizationExtension{
		ID: "child", Kind: "territory", Code: "CITY", Name: "City", ParentID: "territory",
	})
	if err != nil || child.ParentID != "territory" {
		t.Fatalf("child=%#v err=%v", child, err)
	}
	membership, err := service.UpsertOrganizationExtensionMembership(t.Context(), "workspace", partymodel.OrganizationExtensionMembership{
		ID: " membership ", ExtensionID: " territory ", WorkforceProfileID: " workforce ",
	})
	if err != nil || membership.ID != "membership" || membership.WorkforceProfileID != "workforce" || membership.Status != "active" {
		t.Fatalf("membership=%#v err=%v", membership, err)
	}
	if values, err := service.ListOrganizationExtensions(t.Context(), "workspace"); err != nil || len(values) != 2 {
		t.Fatalf("extensions=%#v err=%v", values, err)
	}
	if values, err := service.ListOrganizationExtensionMemberships(t.Context(), "workspace", " workforce "); err != nil || len(values) != 1 {
		t.Fatalf("memberships=%#v err=%v", values, err)
	}
}

func TestPartyOrganizationExtensionDomainRejectsInvalidInput(t *testing.T) {
	repository := newPartyCatalogRepositoryStub()
	service := NewPartyCatalogDomainService(repository)
	cases := []struct {
		name  string
		value partymodel.OrganizationExtension
		code  string
	}{
		{name: "shape", value: partymodel.OrganizationExtension{}, code: "backend.party.organization_extension_invalid"},
		{name: "kind", value: partymodel.OrganizationExtension{ID: "x", Kind: "unknown", Code: "X", Name: "X"}, code: "backend.party.organization_extension_invalid"},
		{name: "status", value: partymodel.OrganizationExtension{ID: "x", Kind: "team", Code: "X", Name: "X", Status: "unknown"}, code: "backend.party.organization_extension_status_invalid"},
		{name: "self parent", value: partymodel.OrganizationExtension{ID: "x", Kind: "team", Code: "X", Name: "X", ParentID: "x"}, code: "backend.party.organization_extension_self_parent"},
		{name: "missing parent", value: partymodel.OrganizationExtension{ID: "x", Kind: "team", Code: "X", Name: "X", ParentID: "missing"}, code: "backend.party.organization_extension_parent_invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.UpsertOrganizationExtension(t.Context(), "workspace", test.value); apperror.CodeOf(err) != test.code {
				t.Fatalf("error=%v", err)
			}
		})
	}
	repository.extensions["inactive"] = partymodel.OrganizationExtension{ID: "inactive", Kind: "team", Status: "inactive"}
	if _, err := service.UpsertOrganizationExtensionMembership(t.Context(), "workspace", partymodel.OrganizationExtensionMembership{}); apperror.CodeOf(err) != "backend.party.organization_membership_invalid" {
		t.Fatalf("membership shape error=%v", err)
	}
	if _, err := service.UpsertOrganizationExtensionMembership(t.Context(), "workspace", partymodel.OrganizationExtensionMembership{ID: "m", ExtensionID: "inactive", WorkforceProfileID: "w", Status: "unknown"}); apperror.CodeOf(err) != "backend.party.organization_membership_status_invalid" {
		t.Fatalf("membership status error=%v", err)
	}
	if _, err := service.UpsertOrganizationExtensionMembership(t.Context(), "workspace", partymodel.OrganizationExtensionMembership{ID: "m", ExtensionID: "inactive", WorkforceProfileID: "w"}); apperror.CodeOf(err) != "backend.party.organization_extension_not_active" {
		t.Fatalf("inactive extension error=%v", err)
	}
}

func TestPartyOrganizationExtensionDomainRemainingOutcomes(t *testing.T) {
	repository := newPartyCatalogRepositoryStub()
	service := NewPartyCatalogDomainService(repository)
	ctx := t.Context()

	if _, err := service.ListOrganizationExtensions(ctx, " \t "); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("list extensions workspace error=%v", err)
	}
	if _, err := service.UpsertOrganizationExtension(ctx, "", partymodel.OrganizationExtension{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("upsert extension workspace error=%v", err)
	}
	for _, test := range []struct {
		name  string
		value partymodel.OrganizationExtension
	}{
		{name: "code", value: partymodel.OrganizationExtension{ID: "x", Kind: partymodel.OrganizationExtensionTeam, Name: "X"}},
		{name: "name", value: partymodel.OrganizationExtension{ID: "x", Kind: partymodel.OrganizationExtensionTeam, Code: "X"}},
	} {
		t.Run("extension_"+test.name, func(t *testing.T) {
			if _, err := service.UpsertOrganizationExtension(ctx, "workspace", test.value); apperror.CodeOf(err) != "backend.party.organization_extension_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if value, err := service.UpsertOrganizationExtension(ctx, "workspace", partymodel.OrganizationExtension{
		ID: "inactive", Kind: partymodel.OrganizationExtensionTeam, Code: "I", Name: "Inactive", Status: partymodel.PartyStatusInactive,
	}); err != nil || value.Status != partymodel.PartyStatusInactive {
		t.Fatalf("inactive extension=%#v err=%v", value, err)
	}

	repository.extensions["parent"] = partymodel.OrganizationExtension{
		ID: "parent", Kind: partymodel.OrganizationExtensionTerritory, Status: partymodel.PartyStatusActive,
	}
	child := partymodel.OrganizationExtension{
		ID: "child", Kind: partymodel.OrganizationExtensionTeam, Code: "C", Name: "Child", ParentID: "parent",
	}
	if _, err := service.UpsertOrganizationExtension(ctx, "workspace", child); apperror.CodeOf(err) != "backend.party.organization_extension_parent_invalid" {
		t.Fatalf("parent kind error=%v", err)
	}
	fault := errors.New("fault")
	repository.err = fault
	if _, err := service.UpsertOrganizationExtension(ctx, "workspace", child); !errors.Is(err, fault) {
		t.Fatalf("parent lookup error=%v", err)
	}
	repository.err = nil

	if _, err := service.ListOrganizationExtensionMemberships(ctx, " ", "profile"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("list memberships workspace error=%v", err)
	}
	if _, err := service.UpsertOrganizationExtensionMembership(ctx, "", partymodel.OrganizationExtensionMembership{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("upsert membership workspace error=%v", err)
	}
	for _, test := range []struct {
		name  string
		value partymodel.OrganizationExtensionMembership
	}{
		{name: "extension", value: partymodel.OrganizationExtensionMembership{ID: "m", WorkforceProfileID: "w"}},
		{name: "workforce", value: partymodel.OrganizationExtensionMembership{ID: "m", ExtensionID: "parent"}},
	} {
		t.Run("membership_"+test.name, func(t *testing.T) {
			if _, err := service.UpsertOrganizationExtensionMembership(ctx, "workspace", test.value); apperror.CodeOf(err) != "backend.party.organization_membership_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	membership := partymodel.OrganizationExtensionMembership{
		ID: "m", ExtensionID: "parent", WorkforceProfileID: "w",
	}
	repository.err = fault
	if _, err := service.UpsertOrganizationExtensionMembership(ctx, "workspace", membership); !errors.Is(err, fault) {
		t.Fatalf("extension lookup error=%v", err)
	}
	repository.err = nil
	membership.ExtensionID = "missing"
	if _, err := service.UpsertOrganizationExtensionMembership(ctx, "workspace", membership); apperror.CodeOf(err) != "backend.party.organization_extension_not_active" {
		t.Fatalf("missing extension error=%v", err)
	}
	membership.ExtensionID = "parent"
	membership.Status = partymodel.PartyStatusInactive
	if value, err := service.UpsertOrganizationExtensionMembership(ctx, "workspace", membership); err != nil || value.Status != partymodel.PartyStatusInactive {
		t.Fatalf("inactive membership=%#v err=%v", value, err)
	}
}
