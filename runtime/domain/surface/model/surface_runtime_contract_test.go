package surfacemodel

import "testing"

func TestRuntimeSurfaceContractV1ValidatesRouteAndEndpoint(t *testing.T) {
	route := RuntimeSurfaceContractV1{
		ContractVersion:     ContractVersion,
		SubjectKind:         ContractSubjectRoute,
		Identity:            "identity.users",
		Surface:             ProductSurfaceAdminConsole,
		Shell:               ShellClassPlatformAdmin,
		ActorAudiences:      []ActorAudience{ActorAudiencePlatformAdmin},
		RequiredPermissions: []string{"identity.users.read"},
		ExposureClass:       ExposureClassPlatformAdmin,
		HighRiskPolicy:      HighRiskActionNone,
	}
	if err := route.Validate(); err != nil {
		t.Fatalf("valid route contract: %v", err)
	}

	endpoint := RuntimeSurfaceContractV1{
		ContractVersion:     ContractVersion,
		SubjectKind:         ContractSubjectEndpoint,
		Identity:            "POST /identity/users",
		Surface:             ProductSurfaceAdminConsole,
		ActorAudiences:      []ActorAudience{ActorAudiencePlatformAdmin},
		RequiredPermissions: []string{"identity.users.write"},
		ExposureClass:       ExposureClassPlatformAdmin,
		EffectClass:         EndpointEffectWrite,
		HighRiskPolicy:      HighRiskActionReasonRequired,
	}
	if err := endpoint.Validate(); err != nil {
		t.Fatalf("valid endpoint contract: %v", err)
	}
}

func TestRuntimeSurfaceContractV1FailsClosed(t *testing.T) {
	base := RuntimeSurfaceContractV1{
		ContractVersion:     ContractVersion,
		SubjectKind:         ContractSubjectEndpoint,
		Identity:            "POST /operations/retry",
		Surface:             ProductSurfaceAdminConsole,
		ActorAudiences:      []ActorAudience{ActorAudiencePlatformAdmin},
		RequiredPermissions: []string{"runtime_ops.operation.retry"},
		ExposureClass:       ExposureClassPlatformAdmin,
		EffectClass:         EndpointEffectWrite,
		HighRiskPolicy:      HighRiskActionConfirmRequired,
	}
	tests := []struct {
		name   string
		mutate func(*RuntimeSurfaceContractV1)
	}{
		{"unknown Surface", func(value *RuntimeSurfaceContractV1) { value.Surface = "unknown" }},
		{"wrong audience", func(value *RuntimeSurfaceContractV1) {
			value.ActorAudiences = []ActorAudience{ActorAudienceBusinessActor}
		}},
		{"public Ops", func(value *RuntimeSurfaceContractV1) { value.ExposureClass = ExposureClassPublic }},
		{"write without permission", func(value *RuntimeSurfaceContractV1) { value.RequiredPermissions = nil }},
		{"unknown high risk policy", func(value *RuntimeSurfaceContractV1) { value.HighRiskPolicy = "unknown" }},
		{"endpoint with shell", func(value *RuntimeSurfaceContractV1) { value.Shell = ShellClassPlatformAdmin }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatalf("contract unexpectedly valid: %+v", value)
			}
		})
	}
}

func TestRuntimeEndpointContractV1FailsClosedOnUnboundPermissionPolicy(t *testing.T) {
	base := RuntimeEndpointContractV1{
		ContractVersion:  ContractVersion,
		EndpointIdentity: "POST /operations/example",
		Projections: []RuntimeEndpointSurfaceProjectionV1{{
			Surface: ProductSurfaceAdminConsole, ActorAudience: ActorAudiencePlatformAdmin,
			ExposureClass: ExposureClassPlatformAdmin,
		}},
		PermissionPolicyRef: "owner_handler_policy:operations.example",
		EffectClass:         EndpointEffectWrite,
		HighRiskPolicy:      HighRiskActionReasonRequired,
		IdempotencyDecision: "required",
		AuditClass:          "mutation_audit_required",
	}
	for _, test := range []struct {
		name   string
		mutate func(*RuntimeEndpointContractV1)
	}{
		{name: "unsupported policy", mutate: func(value *RuntimeEndpointContractV1) { value.PermissionPolicyRef = "client_supplied" }},
		{name: "unbound owner policy", mutate: func(value *RuntimeEndpointContractV1) { value.PermissionPolicyRef = "owner_handler_policy:example" }},
		{name: "static permission missing declaration", mutate: func(value *RuntimeEndpointContractV1) {
			value.PermissionPolicyRef = "static_permission:runtime_ops.recover"
		}},
		{name: "anonymous write", mutate: func(value *RuntimeEndpointContractV1) { value.PermissionPolicyRef = "anonymous" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected endpoint contract validation failure")
			}
		})
	}
}
