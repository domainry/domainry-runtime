package capability

import (
	"context"
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestCapabilityDiscoveryProgressivelyLoadsAndBindsReferences(t *testing.T) {
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects:   []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}}}},
			Actions:   []definitionmodel.ActionSchema{{Key: "order.confirm"}},
			Workflows: []definitionmodel.WorkflowSchema{{Key: "order.approval"}},
			Reports:   []reportmodel.ReportSchema{{Key: "orders.daily"}},
			Integrations: connectormodel.IntegrationSchema{
				Connectors:  []connectormodel.ConnectorSchema{{Key: "erp", Operations: []connectormodel.ConnectorOperationSchema{{Key: "sync"}}}},
				Connections: []connectormodel.ConnectionSchema{{Key: "erp-primary", ConnectorKey: "erp", Status: "ready"}},
			},
		}
	})
	service.UseIdentityReferenceSource(t.Context(), func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error) {
		return CapabilityIdentityReferences{
			UserIDs: []string{"user-a"}, DepartmentIDs: []string{"sales"}, RoleIDs: []string{"operator-id"}, MenuIDs: []string{"orders", "orders", " reports "},
		}, nil
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	index, err := service.DiscoveryIndex(t.Context(), admin)
	if err != nil || len(index.Domains) == 0 || index.ContractHash == "" || index.InstanceHash == "" {
		t.Fatalf("index=%#v err=%v", index, err)
	}
	if len(index.EndpointContracts) == 0 {
		t.Fatal("discovery index omitted the backend-compiled endpoint contracts")
	}
	for _, endpointContract := range index.EndpointContracts {
		if err := endpointContract.Validate(); err != nil {
			t.Fatalf("discovery index published invalid endpoint contract %q: %v", endpointContract.EndpointIdentity, err)
		}
	}
	domain, err := service.DomainCapabilities(t.Context(), admin, "schema", CapabilityDiscoveryFilter{Status: "supported"})
	if err != nil || len(domain.Capabilities) != 4 {
		t.Fatalf("domain=%#v err=%v", domain, err)
	}
	detail, err := service.CapabilityDetail(t.Context(), admin, "schema.object")
	if err != nil || detail.Domain != "schema" || detail.Capability.InputSchema == nil || detail.Capability.Key != "schema.object" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	for _, test := range []struct {
		kind, scope, want string
	}{
		{kind: "object_key", want: "order"}, {kind: "relation_target_object_key", want: "identity_department"}, {kind: "field_key", scope: "order", want: "status"},
		{kind: "action_key", want: "order.confirm"},
		{kind: "scheduler_target_key", scope: "workflow", want: "scheduled:order.approval"}, {kind: "scheduler_target_key", scope: "report_snapshot_refresh", want: "orders.daily"},
		{kind: "user_id", want: "user-a"}, {kind: "department_id", want: "sales"}, {kind: "role_id", want: "operator-id"},
		{kind: "menu_id", want: "orders"}, {kind: "menu_id", want: "reports"},
		{kind: "connector_key", want: "erp"}, {kind: "connection_key", want: "erp-primary"}, {kind: "operation_key", scope: "erp", want: "sync"},
	} {
		result, err := service.ReferenceValues(t.Context(), admin, test.kind, test.scope)
		if err != nil || !capabilityStringContains(result.Values, test.want) || result.InstanceHash != index.InstanceHash {
			t.Fatalf("kind=%s scope=%s result=%#v err=%v", test.kind, test.scope, result, err)
		}
	}
	if _, err := service.DomainCapabilities(t.Context(), admin, "missing", CapabilityDiscoveryFilter{}); apperror.CodeOf(err) != "backend.capability.domain_not_found" {
		t.Fatalf("domain error=%v", err)
	}
	if _, err := service.CapabilityDetail(t.Context(), admin, "missing"); apperror.CodeOf(err) != "backend.capability.not_found" {
		t.Fatalf("capability error=%v", err)
	}
	if _, err := service.ReferenceValues(t.Context(), admin, "field_key", ""); apperror.CodeOf(err) != "backend.capability.reference_scope_required" {
		t.Fatalf("scope error=%v", err)
	}
}

func TestCapabilityDiscoveryPropagatesIdentityReferenceSourceFailure(t *testing.T) {
	want := errors.New("identity reference source unavailable")
	service := NewCapabilityAuthoringApplicationService(nil)
	service.UseIdentityReferenceSource(t.Context(), func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error) {
		return CapabilityIdentityReferences{}, want
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.DiscoveryIndex(t.Context(), admin); !errors.Is(err, want) {
		t.Fatalf("discovery error=%v", err)
	}
}

func TestCapabilityDetailSpecializesIntegrationConnectionForSelectedProvider(t *testing.T) {
	loads := 0
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		loads++
		return capabilitycontract.CapabilityInstanceSchema{Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{{
			Key: "webhook", Providers: []connectormodel.ConnectorProviderSchema{{Key: "generic", ConfigFields: []definitionmodel.FieldSchema{
				{Key: "url", Name: "URL", Type: "text", Required: true}, {Key: "timeout_seconds", Name: "Timeout", Type: "integer", Required: false},
			}, SecretFields: []definitionmodel.FieldSchema{{Key: "signing_secret", Name: "Signing Secret", Type: "opaque", Required: true}}}}, Operations: []connectormodel.ConnectorOperationSchema{{
				Key: "send", Method: "POST", ExecutionMode: "sync", SideEffect: "write", TimeoutDefaultSeconds: 10, TimeoutMaxSeconds: 30,
				Input: []definitionmodel.FieldSchema{{Key: "payload", Name: "Payload", Type: "json", Required: true}, {Key: "trace_id", Name: "Trace ID", Type: "text"}}, Output: []definitionmodel.FieldSchema{{Key: "status_code", Name: "Status", Type: "integer", Required: true}},
			}},
		}}}}
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	detail, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.connection", CapabilityDetailSelection{ConnectorKey: "webhook", ProviderKey: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("detail loaded instance schema %d times; specialization must bind to the same snapshot used for instance_hash", loads)
	}
	if detail.Selection["connector_key"] != "webhook" || detail.Selection["provider_key"] != "generic" {
		t.Fatalf("selection=%v", detail.Selection)
	}
	config := detail.Capability.InputSchema.Properties["config"]
	if config.AdditionalProperties == nil || *config.AdditionalProperties || config.Properties["url"].Type != "string" || config.Properties["timeout_seconds"].Type != "integer" || !capabilityStringContains(config.Required, "url") {
		t.Fatalf("specialized config schema=%#v", config)
	}
	secrets := detail.Capability.InputSchema.Properties["secret_refs"]
	if secrets.AdditionalProperties == nil || *secrets.AdditionalProperties || secrets.Properties["signing_secret"].Type != "string" || !capabilityStringContains(secrets.Required, "signing_secret") {
		t.Fatalf("specialized secret schema=%#v", secrets)
	}
	representative := detail.Capability.Examples[1].Value
	if representative["connector_key"] != "webhook" || representative["provider_key"] != "generic" {
		t.Fatalf("specialized example=%v", representative)
	}
	if _, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.connection", CapabilityDetailSelection{ConnectorKey: "webhook"}); apperror.CodeOf(err) != "backend.capability.detail_selection_incomplete" {
		t.Fatalf("incomplete selection error=%v", err)
	}
	if _, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.connection", CapabilityDetailSelection{ConnectorKey: "webhook", ProviderKey: "missing"}); apperror.CodeOf(err) != "backend.capability.provider_not_found" {
		t.Fatalf("missing provider error=%v", err)
	}
	operationDetail, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.operation_test", CapabilityDetailSelection{ConnectorKey: "webhook", OperationKey: "send"})
	if err != nil {
		t.Fatal(err)
	}
	operationInput := operationDetail.Capability.InputSchema.Properties["input"]
	if operationDetail.Selection["operation_key"] != "send" || operationInput.AdditionalProperties == nil || *operationInput.AdditionalProperties || operationInput.Properties["payload"].Type != "object" || operationInput.Properties["trace_id"].Type != "string" || !capabilityStringContains(operationInput.Required, "payload") {
		t.Fatalf("specialized operation detail=%#v", operationDetail)
	}
	if operationDetail.Capability.InputSchema.Properties["operation"].Const != "send" || operationDetail.Capability.OutputSchema.Properties["response"].Properties["status_code"].Type != "integer" {
		t.Fatalf("operation request/response schema=%#v/%#v", operationDetail.Capability.InputSchema, operationDetail.Capability.OutputSchema)
	}
	bindingDetail, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.binding_validation", CapabilityDetailSelection{ConnectorKey: "webhook", ProviderKey: "generic", OperationKey: "send"})
	if err != nil {
		t.Fatal(err)
	}
	if bindingDetail.Selection["connector_key"] != "webhook" || bindingDetail.Selection["provider_key"] != "generic" || bindingDetail.Selection["operation_key"] != "send" {
		t.Fatalf("binding selection=%v", bindingDetail.Selection)
	}
	bindingDraft := bindingDetail.Capability.InputSchema.Properties["connection_draft"]
	bindingInput := bindingDetail.Capability.InputSchema.Properties["input"]
	bindingOutput := bindingDetail.Capability.InputSchema.Properties["output"]
	if bindingDraft.AdditionalProperties == nil || *bindingDraft.AdditionalProperties || bindingDraft.Properties["config"].Properties["url"].Type != "string" {
		t.Fatalf("specialized binding connection draft=%#v", bindingDraft)
	}
	if bindingInput.AdditionalProperties == nil || *bindingInput.AdditionalProperties || bindingInput.Properties["payload"].Type != "object" || !capabilityStringContains(bindingInput.Required, "payload") {
		t.Fatalf("specialized binding input=%#v", bindingInput)
	}
	if bindingOutput.AdditionalProperties == nil || *bindingOutput.AdditionalProperties || bindingOutput.Properties["status_code"].Type != "integer" || !capabilityStringContains(bindingOutput.Required, "status_code") {
		t.Fatalf("specialized binding output=%#v", bindingOutput)
	}
	if _, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.operation_test", CapabilityDetailSelection{ConnectorKey: "webhook", OperationKey: "missing"}); apperror.CodeOf(err) != "backend.capability.operation_not_found" {
		t.Fatalf("missing operation error=%v", err)
	}
}

func TestCapabilityDiscoveryHashPreconditionsReturnStableDriftCodes(t *testing.T) {
	if err := ValidateCapabilityDiscoveryHashes("old", "current", "", "instance"); apperror.CodeOf(err) != "backend.capability.contract_drift" {
		t.Fatalf("contract drift error=%v", err)
	}
	if err := ValidateCapabilityDiscoveryHashes("current", "current", "old", "instance"); apperror.CodeOf(err) != "backend.capability.instance_drift" {
		t.Fatalf("instance drift error=%v", err)
	}
	if err := ValidateCapabilityDiscoveryHashes("current", "current", "instance", "instance"); err != nil {
		t.Fatalf("matching hashes error=%v", err)
	}
}
