package capability

import (
	"encoding/json"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationConnectionExamplesExecuteRuntimeStatusValidator(t *testing.T) {
	for _, capability := range []capabilitycontract.CapabilityAuthoringDefinition{
		integrationcontract.IntegrationConnectionAuthoringCapability(), integrationcontract.IntegrationConnectionRotateAuthoringCapability(),
	} {
		for _, example := range capability.Examples {
			status, _ := example.Value["status"].(string)
			_, err := integrationapplication.NormalizeConnectionStatus(status)
			if len(example.ExpectedErrorCodes) == 0 {
				if err != nil {
					t.Fatalf("capability=%s example=%s err=%v", capability.Key, example.Name, err)
				}
				continue
			}
			if code := apperror.CodeOf(err); code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%s want=%s err=%v", capability.Key, example.Name, code, example.ExpectedErrorCodes[0], err)
			}
		}
	}
}

func TestIntegrationOperationTestRepairExampleExecutesApplicationGuard(t *testing.T) {
	capability := integrationcontract.IntegrationOperationTestAuthoringCapability()
	example := capability.Examples[2]
	payload, err := json.Marshal(example.Value)
	if err != nil {
		t.Fatal(err)
	}
	var request integrationapplication.ConnectorOperationTestRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	service := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "builder", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"integration.connection.test"}})
	_, err = service.TestConnectorOperation(t.Context(), "missing", request, principal)
	if code := apperror.CodeOf(err); code != example.ExpectedErrorCodes[0] {
		t.Fatalf("example=%s code=%s want=%s err=%v", example.Name, code, example.ExpectedErrorCodes[0], err)
	}
}

func TestIntegrationConnectionCommandsPublishOneHTTPInterfaceEach(t *testing.T) {
	want := map[string]string{
		"integration.connection.rotate":  "POST /integrations/connections/{connectionKey}/rotate",
		"integration.connection.disable": "POST /integrations/connections/{connectionKey}/disable",
		"integration.connection.delete":  "DELETE /integrations/connections/{connectionKey}",
	}
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{
		integrationcontract.IntegrationConnectionRotateAuthoringCapability(),
		integrationcontract.IntegrationConnectionDisableAuthoringCapability(), integrationcontract.IntegrationConnectionDeleteAuthoringCapability(),
	}
	for _, capability := range capabilities {
		if len(capability.ConfigurationRoutes) != 1 || capability.ConfigurationRoutes[0] != want[capability.Key] {
			t.Errorf("capability=%s routes=%v want=%s", capability.Key, capability.ConfigurationRoutes, want[capability.Key])
		}
	}
	resource := integrationcontract.IntegrationConnectionAuthoringCapability()
	for _, route := range []string{"PUT /integrations/connections/{connectionKey}", "GET /integrations/connections/{connectionKey}", "GET /integrations/connections/{connectionKey}/versions", "DELETE /integrations/connections/{connectionKey}"} {
		found := false
		for _, actual := range resource.ConfigurationRoutes {
			found = found || actual == route
		}
		if !found {
			t.Errorf("integration.connection routes=%v missing=%s", resource.ConfigurationRoutes, route)
		}
	}
}
