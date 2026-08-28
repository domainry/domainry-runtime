// Authoring-capability domain service tests.
package capability

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"reflect"
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestAuthoringCapabilityServiceProjectsInstanceSchema(t *testing.T) {
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects:   []definitionmodel.ObjectSchema{{Key: "invoice"}},
			Actions:   []definitionmodel.ActionSchema{{Key: "invoice.issue"}},
			Workflows: []definitionmodel.WorkflowSchema{{Key: "invoice_approval"}},
			Reports:   []reportmodel.ReportSchema{{Key: "invoice_summary"}},
			Integrations: integrationmodel.IntegrationSchema{
				Connectors:  []integrationmodel.ConnectorSchema{{Key: "erp", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "post_invoice"}}}},
				Connections: []integrationmodel.ConnectionSchema{{ConnectorKey: "erp", Status: "ready"}},
			},
		}
	})
	contract, err := service.Capabilities(t.Context(), accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(contract.Instance.ObjectKeys, []string{"invoice"}) || len(contract.Instance.ConnectorOperations) != 1 || !contract.Instance.ConnectorOperations[0].Ready {
		t.Fatalf("unexpected instance projection: %#v", contract.Instance)
	}
}

func TestAuthoringCapabilityServiceRequiresAdministrator(t *testing.T) {
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{}
	})
	if _, err := service.Capabilities(t.Context(), principalmodel.Principal{}); err == nil {
		t.Fatal("unknown principal must be forbidden")
	}
}
