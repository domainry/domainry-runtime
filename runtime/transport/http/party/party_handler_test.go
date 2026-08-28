package party

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	partyapplication "github.com/domainry/domainry-runtime/runtime/application/party"
	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	partyservice "github.com/domainry/domainry-runtime/runtime/domain/party/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type handlerPartyRepository struct {
	values      map[string]partymodel.Aggregate
	jobs        map[string]partymodel.JobCatalogItem
	positions   map[string]partymodel.Position
	extensions  map[string]partymodel.OrganizationExtension
	memberships map[string]partymodel.OrganizationExtensionMembership
	catalogErr  error
	partyErr    error
}

func (r *handlerPartyRepository) ListOrganizationExtensions(context.Context, string) ([]partymodel.OrganizationExtension, error) {
	if r.catalogErr != nil {
		return nil, r.catalogErr
	}
	values := []partymodel.OrganizationExtension{}
	for _, value := range r.extensions {
		values = append(values, value)
	}
	return values, nil
}
func (r *handlerPartyRepository) GetOrganizationExtension(_ context.Context, _, id string) (partymodel.OrganizationExtension, bool, error) {
	value, found := r.extensions[id]
	return value, found, nil
}
func (r *handlerPartyRepository) UpsertOrganizationExtension(_ context.Context, _ string, value partymodel.OrganizationExtension) (partymodel.OrganizationExtension, error) {
	if r.catalogErr != nil {
		return partymodel.OrganizationExtension{}, r.catalogErr
	}
	r.extensions[value.ID] = value
	return value, nil
}
func (r *handlerPartyRepository) ListOrganizationExtensionMemberships(_ context.Context, _, profileID string) ([]partymodel.OrganizationExtensionMembership, error) {
	if r.catalogErr != nil {
		return nil, r.catalogErr
	}
	values := []partymodel.OrganizationExtensionMembership{}
	for _, value := range r.memberships {
		if profileID == "" || value.WorkforceProfileID == profileID {
			values = append(values, value)
		}
	}
	return values, nil
}
func (r *handlerPartyRepository) UpsertOrganizationExtensionMembership(_ context.Context, _ string, value partymodel.OrganizationExtensionMembership) (partymodel.OrganizationExtensionMembership, error) {
	if r.catalogErr != nil {
		return partymodel.OrganizationExtensionMembership{}, r.catalogErr
	}
	r.memberships[value.ID] = value
	return value, nil
}

func (r *handlerPartyRepository) ListJobs(context.Context, string) ([]partymodel.JobCatalogItem, error) {
	if r.catalogErr != nil {
		return nil, r.catalogErr
	}
	values := []partymodel.JobCatalogItem{}
	for _, value := range r.jobs {
		values = append(values, value)
	}
	return values, nil
}
func (r *handlerPartyRepository) GetJob(_ context.Context, _, id string) (partymodel.JobCatalogItem, bool, error) {
	if r.catalogErr != nil {
		return partymodel.JobCatalogItem{}, false, r.catalogErr
	}
	value, found := r.jobs[id]
	return value, found, nil
}
func (r *handlerPartyRepository) UpsertJob(_ context.Context, _ string, value partymodel.JobCatalogItem) (partymodel.JobCatalogItem, error) {
	if r.catalogErr != nil {
		return partymodel.JobCatalogItem{}, r.catalogErr
	}
	r.jobs[value.ID] = value
	return value, nil
}
func (r *handlerPartyRepository) ListPositions(context.Context, string) ([]partymodel.Position, error) {
	if r.catalogErr != nil {
		return nil, r.catalogErr
	}
	values := []partymodel.Position{}
	for _, value := range r.positions {
		values = append(values, value)
	}
	return values, nil
}
func (r *handlerPartyRepository) GetPosition(_ context.Context, _, id string) (partymodel.Position, bool, error) {
	if r.catalogErr != nil {
		return partymodel.Position{}, false, r.catalogErr
	}
	value, found := r.positions[id]
	return value, found, nil
}
func (r *handlerPartyRepository) UpsertPosition(_ context.Context, _ string, value partymodel.Position) (partymodel.Position, error) {
	if r.catalogErr != nil {
		return partymodel.Position{}, r.catalogErr
	}
	r.positions[value.ID] = value
	return value, nil
}

func (r *handlerPartyRepository) List(context.Context, string) ([]partymodel.Aggregate, error) {
	out := []partymodel.Aggregate{}
	for _, value := range r.values {
		out = append(out, value)
	}
	return out, nil
}
func (r *handlerPartyRepository) Get(_ context.Context, _ string, id string) (partymodel.Aggregate, bool, error) {
	if r.partyErr != nil {
		return partymodel.Aggregate{}, false, r.partyErr
	}
	value, found := r.values[id]
	return value, found, nil
}
func (r *handlerPartyRepository) Upsert(_ context.Context, _ string, value partymodel.Aggregate) (partymodel.Aggregate, error) {
	if r.partyErr != nil {
		return partymodel.Aggregate{}, r.partyErr
	}
	r.values[value.Party.ID] = value
	return value, nil
}

func TestPartyHandlerPublishesListGetAndUpsert(t *testing.T) {
	repository := &handlerPartyRepository{values: map[string]partymodel.Aggregate{
		"person": {Party: partymodel.Party{ID: "person", Kind: "person", DisplayName: "Person", Status: "active"}, Person: &partymodel.Person{PartyID: "person"}},
	}}
	auditEvent := ""
	handler := testPartyHandler(repository, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read", "party.write"}}), func(_ *http.Request, event, _ string, _ map[string]any) { auditEvent = event })
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/party", nil),
		httptest.NewRequest(http.MethodGet, "/party/person", nil),
		httptest.NewRequest(http.MethodPut, "/party/organization", jsonBody(`{
			"party":{"kind":"organization","display_name":"Organization"},
			"organization":{"legal_name":"Organization Ltd"},
			"contact_points":[{"id":"organization-email","type":"email","value":"hello@example.com","primary":true}],
			"addresses":[{"id":"organization-office","type":"office","line1":"1 Runtime Way","country":"CN"}],
			"identifiers":[{"id":"organization-registration","type":"registration","value":"REG-1","issuer":"registry"}],
			"communication_preferences":[{"id":"organization-email-preference","channel":"email","allowed":true,"preferred":true,"locale":"zh-CN"}],
			"consents":[{"id":"organization-consent","purpose":"marketing","status":"granted","source":"web","captured_at":"2026-07-25T00:00:00Z"}],
			"privacy_preferences":[{"id":"organization-privacy","key":"profiling","value":"denied","updated_at":"2026-07-25T00:00:00Z"}],
			"marketing_subscriptions":[{"id":"organization-subscription","channel":"email","topic":"news","status":"subscribed","contact_point_id":"organization-email","source":"web","subscribed_at":"2026-07-25T00:00:00Z"}]
		}`)),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
	organization := repository.values["organization"]
	if organization.Organization == nil ||
		len(organization.ContactPoints) != 1 || organization.ContactPoints[0].Value != "hello@example.com" ||
		len(organization.Addresses) != 1 || organization.Addresses[0].Line1 != "1 Runtime Way" ||
		len(organization.Identifiers) != 1 || organization.Identifiers[0].Value != "REG-1" ||
		len(organization.CommunicationPreferences) != 1 || !organization.CommunicationPreferences[0].Preferred ||
		len(organization.Consents) != 1 || organization.Consents[0].Purpose != "marketing" ||
		len(organization.PrivacyPreferences) != 1 || organization.PrivacyPreferences[0].Value != "denied" ||
		len(organization.MarketingSubscriptions) != 1 || organization.MarketingSubscriptions[0].ContactPointID != "organization-email" {
		t.Fatalf("organization=%#v", organization)
	}
	if auditEvent != "party_upserted" {
		t.Fatalf("audit event=%q", auditEvent)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/party/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPartyHandlerRejectsMalformedAndUnauthorizedRequests(t *testing.T) {
	repository := &handlerPartyRepository{values: map[string]partymodel.Aggregate{}}
	handler := testPartyHandler(repository, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, func(*http.Request, string, string, map[string]any) {})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/party", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("forbidden status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/party/person", jsonBody(`{`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d", response.Code)
	}
}

func TestPartyHandlerPropagatesGetAndUpsertErrors(t *testing.T) {
	repository := &handlerPartyRepository{values: map[string]partymodel.Aggregate{}, partyErr: errors.New("party failed")}
	handler := testPartyHandler(repository, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read", "party.write"}}), func(*http.Request, string, string, map[string]any) {})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/party/person", nil),
		httptest.NewRequest(http.MethodPut, "/party/person", jsonBody(`{"party":{"kind":"person","display_name":"Person"},"person":{}}`)),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", request.Method, response.Code, response.Body.String())
		}
	}
}

func TestPartyHandlerPublishesJobAndPositionCatalog(t *testing.T) {
	repository := &handlerPartyRepository{
		values: map[string]partymodel.Aggregate{}, jobs: map[string]partymodel.JobCatalogItem{},
		positions: map[string]partymodel.Position{},
	}
	events := []string{}
	handler := testPartyHandler(repository, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read", "party.write"}}), func(_ *http.Request, event, _ string, _ map[string]any) { events = append(events, event) })
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPut, "/foundation/jobs/job", jsonBody(`{"code":"ENG","name":"Engineer","family":"Engineering","level":"L1"}`)),
		httptest.NewRequest(http.MethodGet, "/foundation/jobs", nil),
		httptest.NewRequest(http.MethodGet, "/foundation/jobs/job", nil),
		httptest.NewRequest(http.MethodPut, "/foundation/positions/position", jsonBody(`{"code":"ENG-1","name":"Engineer 1","job_catalog_item_id":"job","headcount":2}`)),
		httptest.NewRequest(http.MethodGet, "/foundation/positions", nil),
		httptest.NewRequest(http.MethodGet, "/foundation/positions/position", nil),
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
	if repository.jobs["job"].Status != "active" || repository.positions["position"].JobCatalogItemID != "job" ||
		len(events) != 2 || events[0] != "party_job_upserted" || events[1] != "party_position_upserted" {
		t.Fatalf("jobs=%#v positions=%#v events=%#v", repository.jobs, repository.positions, events)
	}
	for _, path := range []string{"/foundation/jobs/missing", "/foundation/positions/missing"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestPartyCatalogHandlerPropagatesServiceAndDecodeErrors(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read", "party.write"}})
	repository := &handlerPartyRepository{
		values: map[string]partymodel.Aggregate{}, jobs: map[string]partymodel.JobCatalogItem{},
		positions: map[string]partymodel.Position{}, catalogErr: errors.New("catalog unavailable"),
	}
	handler := testPartyHandler(repository, principal, func(*http.Request, string, string, map[string]any) {})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/foundation/jobs", nil),
		httptest.NewRequest(http.MethodGet, "/foundation/jobs/job", nil),
		httptest.NewRequest(http.MethodPut, "/foundation/jobs/job", jsonBody(`{"code":"JOB","name":"Job"}`)),
		httptest.NewRequest(http.MethodGet, "/foundation/positions", nil),
		httptest.NewRequest(http.MethodGet, "/foundation/positions/position", nil),
		httptest.NewRequest(http.MethodPut, "/foundation/positions/position", jsonBody(`{"code":"POSITION","name":"Position"}`)),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}

	repository.catalogErr = nil
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPut, "/foundation/jobs/job", jsonBody(`{`)),
		httptest.NewRequest(http.MethodPut, "/foundation/positions/position", jsonBody(`{`)),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", request.URL.Path, response.Code, response.Body.String())
		}
	}
	if len(repository.jobs) != 0 || len(repository.positions) != 0 {
		t.Fatalf("malformed catalog requests mutated jobs=%#v positions=%#v", repository.jobs, repository.positions)
	}
}

func TestPartyHandlerPublishesOrganizationExtensionsAndMemberships(t *testing.T) {
	repository := &handlerPartyRepository{values: map[string]partymodel.Aggregate{}}
	events := []string{}
	handler := testPartyHandler(repository, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read", "party.write"}}), func(_ *http.Request, event, _ string, _ map[string]any) { events = append(events, event) })
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodPut, "/foundation/organization-extensions/team", jsonBody(`{"kind":"team","code":"TEAM","name":"Team"}`)),
		httptest.NewRequest(http.MethodGet, "/foundation/organization-extensions", nil),
		httptest.NewRequest(http.MethodPut, "/foundation/organization-memberships/membership", jsonBody(`{"extension_id":"team","workforce_profile_id":"workforce"}`)),
		httptest.NewRequest(http.MethodGet, "/foundation/organization-memberships?workforce_profile_id=workforce", nil),
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
	if repository.extensions["team"].Kind != "team" || repository.memberships["membership"].WorkforceProfileID != "workforce" ||
		len(events) != 2 || events[0] != "party_organization_extension_upserted" || events[1] != "party_organization_membership_upserted" {
		t.Fatalf("extensions=%#v memberships=%#v events=%#v", repository.extensions, repository.memberships, events)
	}
}

func TestWorkspaceAdminReadsAndWritesOrganizationExtensions(t *testing.T) {
	repository := &handlerPartyRepository{values: map[string]partymodel.Aggregate{}}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	handler := testPartyHandler(repository, admin, func(*http.Request, string, string, map[string]any) {})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPut, "/foundation/organization-extensions/team", jsonBody(`{"kind":"team","code":"TEAM","name":"Team"}`)),
		httptest.NewRequest(http.MethodGet, "/foundation/organization-extensions", nil),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
}

func testPartyHandler(repository *handlerPartyRepository, principal principalmodel.Principal, audit func(*http.Request, string, string, map[string]any)) *PartyHandler {
	if repository.jobs == nil {
		repository.jobs = map[string]partymodel.JobCatalogItem{}
	}
	if repository.positions == nil {
		repository.positions = map[string]partymodel.Position{}
	}
	if repository.extensions == nil {
		repository.extensions = map[string]partymodel.OrganizationExtension{}
	}
	if repository.memberships == nil {
		repository.memberships = map[string]partymodel.OrganizationExtensionMembership{}
	}
	return NewPartyHandler(PartyDependencies{
		Service:       partyapplication.NewPartyApplicationService(partyservice.NewPartyDomainService(repository)),
		Catalog:       partyapplication.NewPartyCatalogApplicationService(partyservice.NewPartyCatalogDomainService(repository)),
		Authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
		Principal:     func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			http.Error(w, code, status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := http.StatusBadRequest
			if apperror.KindOf(err) == apperror.KindForbidden {
				status = http.StatusForbidden
			}
			http.Error(w, apperror.CodeOf(err), status)
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return false
			}
			return true
		},
		SecurityAudit: audit,
	})
}

func jsonBody(value string) *strings.Reader {
	return strings.NewReader(value)
}
