package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

type runtimeReleaseRepositoryStub struct {
	claim     deploymentmodel.RuntimeReleaseCohortClaim
	lease     deploymentmodel.RuntimeReleaseCohortLease
	claimErr  error
	heartbeat int
	releases  int
}

func (r *runtimeReleaseRepositoryStub) ClaimRuntimeRelease(_ context.Context, claim deploymentmodel.RuntimeReleaseCohortClaim) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	r.claim = claim
	return r.lease, r.claimErr
}

func (r *runtimeReleaseRepositoryStub) HeartbeatRuntimeRelease(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time, duration time.Duration) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	r.heartbeat++
	lease.ExpiresAt = now.Add(duration)
	return lease, nil
}

func (r *runtimeReleaseRepositoryStub) ReleaseRuntimeRelease(context.Context, deploymentmodel.RuntimeReleaseCohortLease) error {
	r.releases++
	return nil
}

func validRuntimeReleaseIdentity(t *testing.T, marker string) deploymentmodel.RuntimeReleaseIdentity {
	t.Helper()
	hash := func(value string) string {
		if value == "" {
			value = marker
		}
		return strings.Repeat(value[:1], 64)
	}
	identity := deploymentmodel.RuntimeReleaseIdentity{
		ContractVersion: deploymentmodel.RuntimeReleaseIdentityVersion, BuildMode: "development", RuntimeVersion: "runtime-" + marker,
		RuntimeextContractVersion: "runtimeext-v1", RuntimeextContractSHA256: hash("a"),
		ConnectorContractVersion: "connector-v1", ConnectorContractSHA256: hash("b"),
		DomainSDKContractVersion: "domain-sdk-v1", DomainSDKContractSHA256: hash("c"), DomainSDKGeneratorVersion: "generator-v1", DomainSDKBuildConstraint: "sdk-build-" + marker,
		MetadataSnapshotSHA256: hash(marker), GeneratedSDKSHA256: hash("d"), HandlerRegistrySHA256: hash("e"), ConnectorRegistrySHA256: hash("f"),
	}
	combination, err := RuntimeReleaseCombinationSHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.CombinationSHA256 = combination
	return identity
}

func TestRuntimeReleaseCohortServiceValidatesAndDelegatesLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	repository := &runtimeReleaseRepositoryStub{lease: deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-1", CombinationSHA256: strings.Repeat("1", 64), Generation: 4}}
	service := NewDeploymentRuntimeReleaseCohortApplicationService(repository)
	identity := validRuntimeReleaseIdentity(t, "1")
	lease, err := service.Join(t.Context(), "runtime-1", identity, now)
	if err != nil || lease.InstanceID != "runtime-1" || repository.claim.Identity != identity || repository.claim.Now != now || repository.claim.LeaseDuration <= service.HeartbeatInterval() {
		t.Fatalf("lease=%+v claim=%+v error=%v", lease, repository.claim, err)
	}
	if _, err := service.Heartbeat(t.Context(), lease, now.Add(time.Second)); err != nil || repository.heartbeat != 1 {
		t.Fatalf("heartbeat count=%d error=%v", repository.heartbeat, err)
	}
	if err := service.Leave(t.Context(), lease); err != nil || repository.releases != 1 {
		t.Fatalf("release count=%d error=%v", repository.releases, err)
	}
	if lease, err := service.Join(t.Context(), "runtime-generic", deploymentmodel.RuntimeReleaseIdentity{}, now); err != nil || lease.InstanceID != "" || repository.claim.InstanceID != "runtime-1" {
		t.Fatalf("generic lease=%+v claim=%+v error=%v", lease, repository.claim, err)
	}
	genericDiagnostics := deploymentmodel.RuntimeReleaseIdentity{ContractVersion: deploymentmodel.RuntimeReleaseIdentityVersion, RuntimeVersion: "generic", CombinationSHA256: strings.Repeat("9", 64)}
	if lease, err := service.Join(t.Context(), "runtime-generic-diagnostics", genericDiagnostics, now); err != nil || lease.InstanceID != "" || repository.claim.InstanceID != "runtime-1" {
		t.Fatalf("generic diagnostics lease=%+v claim=%+v error=%v", lease, repository.claim, err)
	}
	partialProject := genericDiagnostics
	partialProject.MetadataSnapshotSHA256 = strings.Repeat("8", 64)
	if _, err := service.Join(t.Context(), "runtime-partial-project", partialProject, now); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
		t.Fatalf("partial project identity error=%v", err)
	}
}

func TestRuntimeReleaseIdentityValidationRejectsComponentAndCombinationDrift(t *testing.T) {
	identity := validRuntimeReleaseIdentity(t, "2")
	if err := ValidateRuntimeReleaseIdentity(identity); err != nil {
		t.Fatal(err)
	}
	componentDrift := identity
	componentDrift.MetadataSnapshotSHA256 = strings.Repeat("9", 64)
	if err := ValidateRuntimeReleaseIdentity(componentDrift); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) || !strings.Contains(err.Error(), "combination") {
		t.Fatalf("component drift error=%v", err)
	}
	malformed := identity
	malformed.HandlerRegistrySHA256 = "not-a-hash"
	if err := ValidateRuntimeReleaseIdentity(malformed); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) {
		t.Fatalf("malformed identity error=%v", err)
	}
}

func TestRuntimeReleaseAdmissionFailsClosedIrreversibly(t *testing.T) {
	admission := &RuntimeReleaseAdmission{}
	if err := admission.Check(); err != nil {
		t.Fatal(err)
	}
	first := errors.New("heartbeat failed")
	admission.Fail(first)
	admission.Fail(errors.New("later error"))
	if err := admission.Check(); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseAdmission) || !strings.Contains(err.Error(), first.Error()) || strings.Contains(err.Error(), "later error") {
		t.Fatalf("admission error=%v", err)
	}
}
