package surfacecontext

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	surfacecontextbusiness "github.com/domainry/domainry-runtime/runtime/application/surfacecontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"
)

func surfaceContextTestHandler() *SurfaceContextHandler {
	return NewSurfaceContextHandler(SurfaceContextDependencies{
		Service: surfacecontextbusiness.NewSurfaceContextApplicationService(surfacecontextbusiness.SurfaceContextDependencies{}),
		Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.Header().Set("X-Error-Code", code)
			w.WriteHeader(status)
		},
	})
}

func TestSurfaceContextHandlerValidationAndPathAuthority(t *testing.T) {
	handler := surfaceContextTestHandler()
	response := httptest.NewRecorder()
	handler.surfaceContext(response, httptest.NewRequest(http.MethodPost, "/surfaces//context", strings.NewReader(`{}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing surface status=%d", response.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/surfaces/dashboard/context", strings.NewReader("{"))
	request.SetPathValue("surfaceKey", "dashboard")
	response = httptest.NewRecorder()
	handler.surfaceContext(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/surfaces/dashboard/context", strings.NewReader(`{"surface_key":"body-value","objects":[]}`))
	request.SetPathValue("surfaceKey", "dashboard")
	response = httptest.NewRecorder()
	handler.surfaceContext(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("context status=%d", response.Code)
	}
	var result surfacecontextmodel.SurfaceContextResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SurfaceKey != "dashboard" {
		t.Fatalf("surface key=%q, want path value", result.SurfaceKey)
	}
}

func TestSurfaceContextRoutes(t *testing.T) {
	mux := http.NewServeMux()
	surfaceContextTestHandler().RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/surfaces/dashboard/context", strings.NewReader(`{"objects":[]}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("registered route status=%d", response.Code)
	}
}
