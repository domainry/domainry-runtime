package businessreferences

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func businessReferenceTestHandler(principal principalmodel.Principal) *BusinessReferencesHandler {
	return NewBusinessReferencesHandler(BusinessReferencesDependencies{
		Service:   changeplanapplication.NewChangePlanReferenceApplicationService(nil, nil, nil),
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.Header().Set("X-Error-Code", code)
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusForbidden) },
	})
}

func businessReferenceAdmin() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{changeplanapplication.ActionBusinessReferenceGraph, changeplanapplication.ActionBusinessReferenceImpact}})
}

func TestBusinessReferenceGraphAndImpact(t *testing.T) {
	handler := businessReferenceTestHandler(businessReferenceAdmin())
	request := httptest.NewRequest(http.MethodGet, "/business-references/graph", nil)
	if graph, err := handler.Graph(request); err != nil || graph.Version == "" {
		t.Fatalf("graph=%#v err=%v", graph, err)
	}

	response := httptest.NewRecorder()
	handler.businessReferenceGraph(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("graph status=%d", response.Code)
	}

	response = httptest.NewRecorder()
	handler.businessReferenceImpact(response, httptest.NewRequest(http.MethodGet, "/business-references//", nil))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.reference.identity_required" {
		t.Fatalf("missing identity status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	request = httptest.NewRequest(http.MethodGet, "/business-references/object/", nil)
	request.SetPathValue("resourceType", "object")
	response = httptest.NewRecorder()
	handler.businessReferenceImpact(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing resource key status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/business-references/object/customer", nil)
	request.SetPathValue("resourceType", "object")
	request.SetPathValue("resourceKey", "customer")
	response = httptest.NewRecorder()
	handler.businessReferenceImpact(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("impact status=%d", response.Code)
	}
}

func TestBusinessReferenceErrorsAndRoutes(t *testing.T) {
	handler := businessReferenceTestHandler(principalmodel.Principal{})
	response := httptest.NewRecorder()
	handler.businessReferenceGraph(response, httptest.NewRequest(http.MethodGet, "/business-references/graph", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("graph error status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.businessReferenceImpact(response, httptest.NewRequest(http.MethodGet, "/business-references/object/customer", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("impact error status=%d", response.Code)
	}

	mux := http.NewServeMux()
	businessReferenceTestHandler(businessReferenceAdmin()).RegisterRoutes(mux)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/business-references/graph", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("registered graph route status=%d", response.Code)
	}
}
