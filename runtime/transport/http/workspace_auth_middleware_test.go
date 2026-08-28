package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestAuthenticatedWorkspaceMustMatchExplicitRequestTarget(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	for _, test := range []struct {
		name    string
		request *http.Request
		matches bool
	}{
		{name: "implicit target", request: httptest.NewRequest(http.MethodGet, "/records", nil), matches: true},
		{name: "matching header", request: workspaceRequest("workspace-a", ""), matches: true},
		{name: "different header", request: workspaceRequest("workspace-b", ""), matches: false},
		{name: "matching query", request: workspaceRequest("", "workspace-a"), matches: true},
		{name: "different query", request: workspaceRequest("", "workspace-b"), matches: false},
		{name: "missing authenticated workspace", request: workspaceRequest("workspace-a", ""), matches: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := principal
			if test.name == "missing authenticated workspace" {
				candidate.WorkspaceID = ""
			}
			if got := authenticatedWorkspaceMatchesRequest(candidate, test.request); got != test.matches {
				t.Fatalf("workspace match=%v want=%v", got, test.matches)
			}
		})
	}
}

func workspaceRequest(headerWorkspaceID, queryWorkspaceID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/records?workspace_id="+queryWorkspaceID, nil)
	request.Header.Set("X-Workspace-ID", headerWorkspaceID)
	return request
}
