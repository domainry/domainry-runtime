package party

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestPartyOrganizationExtensionHandlersPropagateFailuresAndRejectMalformedJSON(t *testing.T) {
	repository := &handlerPartyRepository{
		values:      map[string]partymodel.Aggregate{},
		extensions:  map[string]partymodel.OrganizationExtension{},
		memberships: map[string]partymodel.OrganizationExtensionMembership{},
		catalogErr:  errors.New("party catalog unavailable"),
	}
	handler := testPartyHandler(repository, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"party.read", "party.write"}}), func(*http.Request, string, string, map[string]any) {})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	failingRequests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/foundation/organization-extensions", nil),
		httptest.NewRequest(http.MethodPut, "/foundation/organization-extensions/team", jsonBody(`{"kind":"team","code":"TEAM","name":"Team"}`)),
		httptest.NewRequest(http.MethodGet, "/foundation/organization-memberships?workforce_profile_id=workforce", nil),
		httptest.NewRequest(http.MethodPut, "/foundation/organization-memberships/member", jsonBody(`{"extension_id":"team","workforce_profile_id":"workforce"}`)),
	}
	for _, request := range failingRequests {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}

	repository.catalogErr = nil
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPut, "/foundation/organization-extensions/team", jsonBody(`{`)),
		httptest.NewRequest(http.MethodPut, "/foundation/organization-memberships/member", jsonBody(`{`)),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", request.URL.Path, response.Code, response.Body.String())
		}
	}
	if len(repository.extensions) != 0 || len(repository.memberships) != 0 {
		t.Fatalf("malformed requests mutated extensions=%+v memberships=%+v", repository.extensions, repository.memberships)
	}
}
