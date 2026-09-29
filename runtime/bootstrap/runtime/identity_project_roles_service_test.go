package runtime

import (
	"testing"

	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

func TestWorkspaceRoleCatalogAcceptsManualServiceAccountRole(t *testing.T) {
	role := projectmodel.Role{
		Key: "open_api_service", Name: "Open API", Audience: "service", AssignmentMode: "manual",
	}
	bootstrap, complete, err := runtimeWorkspaceRoleCatalogRoles([]projectmodel.Role{role})
	if err != nil {
		t.Fatal(err)
	}
	if len(bootstrap) != 0 || len(complete) != 1 || complete[0].Key != role.Key {
		t.Fatalf("bootstrap=%#v complete=%#v", bootstrap, complete)
	}

	role.ProvisionToWorkspaces = true
	if _, _, err = runtimeWorkspaceRoleCatalogRoles([]projectmodel.Role{role}); err == nil {
		t.Fatal("service role was accepted as a human Workspace bootstrap role")
	}
}
