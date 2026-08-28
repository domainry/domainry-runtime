package surfacemodel

import "testing"

func validRuntimeEndpointContract() RuntimeEndpointContractV1 {
	return RuntimeEndpointContractV1{
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
		AuditClass:          "mutation",
	}
}

func TestRuntimeEndpointContractValidationEdges(t *testing.T) {
	if err := validRuntimeEndpointContract().Validate(); err != nil {
		t.Fatalf("valid endpoint contract=%v", err)
	}
	mutations := []func(*RuntimeEndpointContractV1){
		func(v *RuntimeEndpointContractV1) { v.ContractVersion = "old" },
		func(v *RuntimeEndpointContractV1) { v.EndpointIdentity = " " },
		func(v *RuntimeEndpointContractV1) { v.Projections = nil },
		func(v *RuntimeEndpointContractV1) { v.Projections[0].Surface = "unknown" },
		func(v *RuntimeEndpointContractV1) { v.Projections[0].ActorAudience = ActorAudienceBusinessActor },
		func(v *RuntimeEndpointContractV1) { v.Projections[0].ExposureClass = ExposureClassPublic },
		func(v *RuntimeEndpointContractV1) { v.EffectClass = "unknown" },
		func(v *RuntimeEndpointContractV1) { v.HighRiskPolicy = "unknown" },
		func(v *RuntimeEndpointContractV1) { v.PermissionPolicyRef = " " },
		func(v *RuntimeEndpointContractV1) { v.IdempotencyDecision = "" },
		func(v *RuntimeEndpointContractV1) { v.AuditClass = "" },
	}
	for index, mutate := range mutations {
		value := validRuntimeEndpointContract()
		mutate(&value)
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid case %d succeeded: %+v", index, value)
		}
	}

	readAnonymous := validRuntimeEndpointContract()
	readAnonymous.EffectClass = EndpointEffectRead
	readAnonymous.PermissionPolicyRef = "anonymous"
	readAnonymous.IdempotencyDecision = ""
	readAnonymous.AuditClass = ""
	if err := readAnonymous.Validate(); err != nil {
		t.Fatalf("anonymous read=%v", err)
	}
	protocolOnly := readAnonymous
	protocolOnly.Projections = nil
	protocolOnly.ProtocolAudiences = []string{"integration"}
	protocolOnly.PermissionPolicyRef = "integration_entrypoint_policy:connector.receive"
	if err := protocolOnly.Validate(); err != nil {
		t.Fatalf("protocol-only contract=%v", err)
	}

	for _, policy := range []string{
		"integration_entrypoint_policy:",
		"integration_entrypoint_policy:receive",
		"static_permission:",
		"static_permission:runtime.read",
		"owner_handler_policy:",
		"owner_handler_policy:owner",
	} {
		value := readAnonymous
		value.PermissionPolicyRef = policy
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid policy %q succeeded", policy)
		}
	}
	noProtocol := readAnonymous
	noProtocol.PermissionPolicyRef = "integration_entrypoint_policy:connector.receive"
	if err := noProtocol.Validate(); err == nil {
		t.Fatal("integration policy without protocol audience succeeded")
	}
	for _, policy := range []string{"integration_entrypoint_policy:", "integration_entrypoint_policy:receive"} {
		value := readAnonymous
		value.ProtocolAudiences = []string{"integration"}
		value.PermissionPolicyRef = policy
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid bound integration policy %q succeeded", policy)
		}
	}
	static := readAnonymous
	static.PermissionPolicyRef = "static_permission:runtime.read"
	static.RequiredPermissions = []string{" runtime.read "}
	if err := static.Validate(); err != nil {
		t.Fatalf("static permission=%v", err)
	}
}

func TestRuntimeSurfaceContractValidationEdges(t *testing.T) {
	route := RuntimeSurfaceContractV1{
		ContractVersion: ContractVersion, SubjectKind: ContractSubjectRoute, Identity: "route",
		Surface: ProductSurfaceAdminConsole, Shell: ShellClassPlatformAdmin,
		ActorAudiences: []ActorAudience{ActorAudiencePlatformAdmin}, ExposureClass: ExposureClassPlatformAdmin,
		HighRiskPolicy: HighRiskActionNone,
	}
	for index, mutate := range []func(*RuntimeSurfaceContractV1){
		func(v *RuntimeSurfaceContractV1) { v.ContractVersion = "old" },
		func(v *RuntimeSurfaceContractV1) { v.Identity = " " },
		func(v *RuntimeSurfaceContractV1) { v.ActorAudiences = nil },
		func(v *RuntimeSurfaceContractV1) { v.Shell = ShellClassSourceOwnedBusiness },
		func(v *RuntimeSurfaceContractV1) { v.EffectClass = EndpointEffectRead },
		func(v *RuntimeSurfaceContractV1) { v.SubjectKind = "unknown" },
	} {
		value := route
		mutate(&value)
		if err := value.Validate(); err == nil {
			t.Fatalf("invalid route case %d succeeded", index)
		}
	}
	readEndpoint := route
	readEndpoint.SubjectKind = ContractSubjectEndpoint
	readEndpoint.Identity = "GET /runtime"
	readEndpoint.Shell = ""
	readEndpoint.EffectClass = EndpointEffectRead
	if err := readEndpoint.Validate(); err != nil {
		t.Fatalf("read endpoint=%v", err)
	}
	invalidEndpoint := readEndpoint
	invalidEndpoint.EffectClass = ""
	if err := invalidEndpoint.Validate(); err == nil {
		t.Fatal("empty endpoint effect succeeded")
	}
	writeEndpoint := readEndpoint
	writeEndpoint.EffectClass = EndpointEffectWrite
	writeEndpoint.RequiredPermissions = []string{" ", "runtime.write"}
	if err := writeEndpoint.Validate(); err != nil {
		t.Fatalf("write endpoint=%v", err)
	}
}

func TestRuntimeSurfaceContractCollectionHelpers(t *testing.T) {
	if containsAudience([]ActorAudience{ActorAudienceBusinessActor}, ActorAudiencePlatformAdmin) {
		t.Fatal("unexpected audience match")
	}
	if !containsString([]string{" other ", " expected "}, "expected") ||
		containsString([]string{"other"}, "expected") {
		t.Fatal("string membership mismatch")
	}
	keys := trimmedPermissionKeys([]string{" ", " read "})
	if len(keys) != 1 || keys[0] != "read" {
		t.Fatalf("keys=%v", keys)
	}
}
