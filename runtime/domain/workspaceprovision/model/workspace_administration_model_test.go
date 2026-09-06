package workspaceprovisionmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkspaceAdministrationJSONOmitsPhysicalIdentityAndInternalCommercialRevision(t *testing.T) {
	payload, err := json.Marshal(struct {
		Actor  AdministrationActor `json:"actor"`
		Result LifecycleResult     `json:"result"`
	}{
		Actor: AdministrationActor{WorkspaceID: "physical-workspace", UserID: "physical-user", RoleKey: "tenant_admin", RequestID: "request-1", AuthorizationRevision: "auth-1"},
		Result: LifecycleResult{Workspace: CatalogEntry{
			CanonicalCode: "workspace-a", DisplayName: "Workspace A", Status: WorkspaceStatusActive, Revision: 2,
			CommercialConfiguration: CommercialConfigurationView{Plan: "standard", Revision: 99},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	contract := strings.ToLower(string(payload))
	for _, forbidden := range []string{"physical-workspace", "physical-user", "workspace_id", "authorization_revision", `"revision":99`} {
		if strings.Contains(contract, forbidden) {
			t.Fatalf("Workspace administration JSON leaked %q: %s", forbidden, payload)
		}
	}
}
