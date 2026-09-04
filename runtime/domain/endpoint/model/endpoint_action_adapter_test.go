package endpointmodel

import (
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

func endpointActionTestContract() RuntimeEndpointContractV1 {
	return RuntimeEndpointContractV1{
		ContractVersion: ContractVersion, EndpointIdentity: "POST /workspaces",
		ActionKey: "runtime.workspaceprovision.provision_workspace", SourceOwner: "workspaceprovision", ApplicationUseCase: "provisionWorkspace",
		ListenerExposures:   []ListenerExposure{ListenerExposureManagement, ListenerExposureOps},
		RequiredPermissions: []string{"runtime.workspaceprovision.provision_workspace"}, PermissionPolicyRef: "static_permission:runtime.workspaceprovision.provision_workspace",
		EffectClass: EndpointEffectWrite, HighRiskPolicy: HighRiskActionConfirmRequired,
		IdempotencyDecision: "caller_key_required", AuditClass: "mutation_audit_required",
	}
}

func TestAuthorizationActionDefinitionProjectsStaticEndpointAsSameKeyPermission(t *testing.T) {
	contract := endpointActionTestContract()
	definition, err := AuthorizationActionDefinition(contract)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Key != contract.ActionKey || definition.OperationLabel != contract.ApplicationUseCase || definition.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || definition.Permission == nil || definition.Permission.Key != definition.Key {
		t.Fatalf("definition=%#v", definition)
	}
	if definition.HTTP == nil || definition.HTTP.RouteTemplate != "/workspaces" || definition.ApprovalPolicies[0] != actioncontract.ApprovalConfirmation {
		t.Fatalf("binding/governance=%#v", definition)
	}
}

func TestEndpointContractRejectsAnyRolePermissionShapeExceptExactSameKey(t *testing.T) {
	for name, mutate := range map[string]func(*RuntimeEndpointContractV1){
		"different permission": func(contract *RuntimeEndpointContractV1) {
			contract.RequiredPermissions = []string{"runtime.workspaceprovision.other"}
			contract.PermissionPolicyRef = "static_permission:runtime.workspaceprovision.other"
		},
		"additional permission": func(contract *RuntimeEndpointContractV1) {
			contract.RequiredPermissions = append(contract.RequiredPermissions, "runtime.workspaceprovision.other")
		},
		"ambient permission on owner policy": func(contract *RuntimeEndpointContractV1) {
			contract.PermissionPolicyRef = "owner_handler_policy:workspaceprovision.provisionWorkspace"
		},
	} {
		t.Run(name, func(t *testing.T) {
			contract := endpointActionTestContract()
			mutate(&contract)
			if err := contract.Validate(); err == nil {
				t.Fatalf("contract accepted non-canonical role permission shape: %#v", contract)
			}
		})
	}
}

func TestAuthorizationActionDefinitionPreservesDispatcherAndAnonymousPolicies(t *testing.T) {
	dynamic := endpointActionTestContract()
	dynamic.EndpointIdentity = "GET /records/{objectKey}/items/{recordID}"
	dynamic.ActionKey = "runtime.records.get_record"
	dynamic.SourceOwner, dynamic.ApplicationUseCase = "records", "getRecord"
	dynamic.RequiredPermissions = nil
	dynamic.PermissionPolicyRef = "owner_handler_policy:records.getRecord"
	dynamic.EffectClass, dynamic.HighRiskPolicy = EndpointEffectRead, HighRiskActionNone
	dynamic.IdempotencyDecision, dynamic.AuditClass = "not_applicable", "owner_read_audit_policy"
	definition, err := AuthorizationActionDefinition(dynamic)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || definition.Permission != nil {
		t.Fatalf("dispatcher definition=%#v", definition)
	}
	if definition.HTTP.DisplayRouteTemplate != "" {
		t.Fatalf("generic object wildcard exposed as display route: %#v", definition.HTTP)
	}

	anonymous := dynamic
	anonymous.EndpointIdentity, anonymous.ActionKey = "GET /", "runtime.root.api_info"
	anonymous.SourceOwner, anonymous.ApplicationUseCase = "root", "apiInfo"
	anonymous.PermissionPolicyRef = "anonymous"
	definition, err = AuthorizationActionDefinition(anonymous)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Authorization.Strategy != actioncontract.AuthorizationAnonymous || definition.Authorization.PolicyKey != "" || definition.HTTP.DisplayRouteTemplate != "/" {
		t.Fatalf("anonymous definition=%#v", definition)
	}
}

func TestAuthorizationActionDefinitionPreservesServiceAudienceAndOwnerPolicy(t *testing.T) {
	contract := endpointActionTestContract()
	contract.EndpointIdentity = "POST /dispatch/executions"
	contract.ActionKey = "runtime.dispatch.execution.accept"
	contract.SourceOwner, contract.ApplicationUseCase = "dispatch", "acceptExecution"
	contract.ListenerExposures = []ListenerExposure{ListenerExposurePublic}
	contract.ProtocolAudiences = []string{"scheduler_service_service"}
	contract.RequiredPermissions = nil
	contract.PermissionPolicyRef = "integration_entrypoint_policy:dispatch.execution"
	contract.HighRiskPolicy = HighRiskActionNone
	contract.IdempotencyDecision = "system_key_required"
	definition, err := AuthorizationActionDefinition(contract)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Authorization.Strategy != actioncontract.AuthorizationSigned || definition.Authorization.PolicyKey != "dispatch.execution" || len(definition.Authorization.Audiences) != 1 || definition.Authorization.Audiences[0] != "scheduler_service_service" {
		t.Fatalf("service definition=%#v", definition)
	}
}

func TestGeneratedEndpointContractsHaveUniqueCanonicalActionProjection(t *testing.T) {
	seenActions, seenHTTP := map[string]bool{}, map[string]bool{}
	for endpoint, contract := range EndpointContracts {
		if err := contract.Validate(); err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
		definition, err := AuthorizationActionDefinition(contract)
		if err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
		if seenActions[definition.Key] {
			t.Fatalf("duplicate generated action key %q", definition.Key)
		}
		seenActions[definition.Key] = true
		httpIdentity := definition.HTTP.Method + " " + definition.HTTP.RouteTemplate
		if seenHTTP[httpIdentity] || httpIdentity != endpoint {
			t.Fatalf("HTTP projection %q for endpoint %q", httpIdentity, endpoint)
		}
		seenHTTP[httpIdentity] = true
		if strings.Contains(definition.HTTP.RouteTemplate, "{objectKey}") && definition.HTTP.DisplayRouteTemplate != "" {
			t.Fatalf("%s exposes object wildcard display route", endpoint)
		}
	}
	if len(seenActions) != len(EndpointContracts) || len(seenActions) < 100 {
		t.Fatalf("projected=%d contracts=%d", len(seenActions), len(EndpointContracts))
	}
}
