package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func TestRuntimeReleaseCohortLifecycleEdges(t *testing.T) {
	identity := validRuntimeReleaseIdentity(t, "e")
	now := time.Now()
	var nilService *DeploymentRuntimeReleaseCohortApplicationService
	if _, err := nilService.Join(t.Context(), "runtime", identity, now); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
		t.Fatalf("nil service join error=%v", err)
	}
	if _, err := NewDeploymentRuntimeReleaseCohortApplicationService(nil).Join(t.Context(), "runtime", identity, now); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
		t.Fatalf("nil repository join error=%v", err)
	}
	service := NewDeploymentRuntimeReleaseCohortApplicationService(&runtimeReleaseRepositoryStub{})
	for _, instanceID := range []string{"", " runtime"} {
		if _, err := service.Join(t.Context(), instanceID, identity, now); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
			t.Fatalf("instance=%q error=%v", instanceID, err)
		}
	}
	emptyLease := deploymentmodel.RuntimeReleaseCohortLease{}
	if got, err := service.Heartbeat(t.Context(), emptyLease, now); err != nil || got != emptyLease {
		t.Fatalf("heartbeat=%+v error=%v", got, err)
	}
	if err := nilService.Leave(t.Context(), deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime"}); err != nil {
		t.Fatalf("nil service leave=%v", err)
	}
	if err := NewDeploymentRuntimeReleaseCohortApplicationService(nil).Leave(t.Context(), deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime"}); err != nil {
		t.Fatalf("nil repository leave=%v", err)
	}
	if err := service.Leave(t.Context(), emptyLease); err != nil {
		t.Fatalf("empty lease leave=%v", err)
	}
	if interval := nilService.HeartbeatInterval(); interval != defaultRuntimeReleaseHeartbeat {
		t.Fatalf("nil interval=%v", interval)
	}
	service.heartbeat = 0
	if interval := service.HeartbeatInterval(); interval != defaultRuntimeReleaseHeartbeat {
		t.Fatalf("zero interval=%v", interval)
	}
}

func TestRuntimeReleaseIdentityValidationEdges(t *testing.T) {
	valid := validRuntimeReleaseIdentity(t, "a")
	tests := []deploymentmodel.RuntimeReleaseIdentity{
		func() deploymentmodel.RuntimeReleaseIdentity {
			value := valid
			value.ContractVersion = "old"
			return value
		}(),
		func() deploymentmodel.RuntimeReleaseIdentity { value := valid; value.BuildMode = "other"; return value }(),
		func() deploymentmodel.RuntimeReleaseIdentity {
			value := valid
			value.BuildMode = "packaged"
			value.CombinationSHA256, _ = RuntimeReleaseCombinationSHA256(value)
			return value
		}(),
		func() deploymentmodel.RuntimeReleaseIdentity { value := valid; value.RuntimeVersion = ""; return value }(),
		func() deploymentmodel.RuntimeReleaseIdentity {
			value := valid
			value.RuntimeVersion = " runtime"
			return value
		}(),
		func() deploymentmodel.RuntimeReleaseIdentity {
			value := valid
			value.RuntimeextContractSHA256 = strings.Repeat("z", 64)
			return value
		}(),
		func() deploymentmodel.RuntimeReleaseIdentity {
			value := valid
			value.RuntimeextContractSHA256 = strings.Repeat("A", 64)
			return value
		}(),
	}
	for index, identity := range tests {
		err := ValidateRuntimeReleaseIdentity(identity)
		if index == 2 {
			if err != nil {
				t.Fatalf("packaged identity error=%v", err)
			}
			continue
		}
		if !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
			t.Fatalf("case=%d error=%v", index, err)
		}
	}
}

func TestRuntimeReleaseAdmissionNilAndNoopEdges(t *testing.T) {
	var admission *RuntimeReleaseAdmission
	admission.Fail(errors.New("ignored"))
	if err := admission.Check(); err != nil {
		t.Fatalf("nil admission error=%v", err)
	}
	live := &RuntimeReleaseAdmission{}
	live.Fail(nil)
	if err := live.Check(); err != nil {
		t.Fatalf("nil failure recorded=%v", err)
	}
}

func TestRuntimeReleaseIntegrityFailureEdges(t *testing.T) {
	var integrity *RuntimeReleaseIntegrity
	for name, check := range map[string]func(context.Context) error{
		"build": integrity.BuildReadiness, "signature": integrity.SignatureReadiness,
		"schema": integrity.SchemaReadiness, "registry": integrity.RegistryReadiness,
	} {
		if err := check(t.Context()); err != nil {
			t.Fatalf("%s nil readiness=%v", name, err)
		}
	}

	identity := validRuntimeReleaseIdentity(t, "i")
	identity.BuildMode = "packaged"
	unverified := NewRuntimeReleaseIntegrity(identity, RuntimeReleaseArtifactEvidence{}, "schema", func(context.Context) (string, error) { return "schema", nil }, nil, nil)
	if err := unverified.BuildReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseBuildIntegrity) {
		t.Fatalf("unverified build=%v", err)
	}
	if err := unverified.SignatureReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSignatureIntegrity) {
		t.Fatalf("unverified signature=%v", err)
	}

	missingSchema := NewRuntimeReleaseIntegrity(identity, RuntimeReleaseArtifactEvidence{Verified: true}, "", nil, nil, nil)
	if err := missingSchema.SchemaReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSchemaIntegrity) {
		t.Fatalf("missing schema=%v", err)
	}
	missingSchema.schemaRevision = func(context.Context) (string, error) { return "schema", nil }
	if err := missingSchema.SchemaReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSchemaIntegrity) {
		t.Fatalf("empty expected schema=%v", err)
	}
	missingSchema.expectedSchemaRevision = "schema"
	missingSchema.schemaRevision = nil
	if err := missingSchema.SchemaReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSchemaIntegrity) {
		t.Fatalf("nil schema reader=%v", err)
	}
	loadErr := errors.New("schema unavailable")
	missingSchema.schemaRevision = func(context.Context) (string, error) { return "", loadErr }
	if err := missingSchema.SchemaReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSchemaIntegrity) || !strings.Contains(err.Error(), loadErr.Error()) {
		t.Fatalf("schema load=%v", err)
	}
	missingSchema.schemaRevision = func(context.Context) (string, error) { return "", nil }
	if err := missingSchema.SchemaReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseSchemaIntegrity) {
		t.Fatalf("empty schema=%v", err)
	}

	mutableHandlers := runtimeext.NewBusinessHandlerRegistry()
	frozenHandlers := runtimeext.NewBusinessHandlerRegistry()
	frozenHandlers.Freeze()
	mutableConnectors := connector.NewRegistry()
	frozenConnectors := connector.NewRegistry()
	frozenConnectors.Freeze()
	for _, service := range []*RuntimeReleaseIntegrity{
		NewRuntimeReleaseIntegrity(identity, RuntimeReleaseArtifactEvidence{Verified: true}, "schema", func(context.Context) (string, error) { return "schema", nil }, mutableHandlers, frozenConnectors),
		NewRuntimeReleaseIntegrity(identity, RuntimeReleaseArtifactEvidence{Verified: true}, "schema", func(context.Context) (string, error) { return "schema", nil }, frozenHandlers, nil),
		NewRuntimeReleaseIntegrity(identity, RuntimeReleaseArtifactEvidence{Verified: true}, "schema", func(context.Context) (string, error) { return "schema", nil }, frozenHandlers, mutableConnectors),
	} {
		if err := service.RegistryReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseRegistryIntegrity) {
			t.Fatalf("mutable registry error=%v", err)
		}
	}
	handlerHash, _ := deploymentmodel.RuntimeRegistrySHA256("domainry-handler-registry-v1", frozenHandlers.Descriptors())
	registryDrift := NewRuntimeReleaseIntegrity(identity, RuntimeReleaseArtifactEvidence{Verified: true}, "schema", func(context.Context) (string, error) { return "schema", nil }, frozenHandlers, frozenConnectors)
	registryDrift.identity.HandlerRegistrySHA256 = handlerHash
	registryDrift.identity.ConnectorRegistrySHA256 = strings.Repeat("0", 64)
	if err := registryDrift.RegistryReadiness(t.Context()); !errors.Is(err, ErrRuntimeReleaseRegistryIntegrity) {
		t.Fatalf("connector registry drift=%v", err)
	}
}
