package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type workspaceAdmissionProbe struct {
	active      bool
	err         error
	workspaceID string
}

func (probe *workspaceAdmissionProbe) WorkspaceActive(_ context.Context, workspaceID string) (bool, error) {
	probe.workspaceID = workspaceID
	return probe.active, probe.err
}

func TestAuthenticationMiddlewareRejectsSuspendedWorkspaceFromAuthenticatedPrincipal(t *testing.T) {
	probe := &workspaceAdmissionProbe{}
	router := routerWithIdentitySDK(identitysdk.Principal{Known: true, UserID: "staff-a", WorkspaceID: "workspace-suspended"})
	router.workspaceAdmission = probe
	mux := http.NewServeMux()
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/records", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	router.withAuth(mux, mux).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || probe.workspaceID != "workspace-suspended" {
		t.Fatalf("status=%d workspace=%q body=%s", response.Code, probe.workspaceID, response.Body.String())
	}
	probe.active = true
	response = httptest.NewRecorder()
	router.withAuth(mux, mux).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("active Workspace status=%d body=%s", response.Code, response.Body.String())
	}
}
