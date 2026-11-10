package deploymentmodel

import (
	"errors"
	"testing"
)

func TestRuntimeReleaseIdentityErrorEdges(t *testing.T) {
	if _, err := RuntimeRegistrySHA256("v1", make(chan int)); err == nil {
		t.Fatal("unencodable registry descriptors accepted")
	}
	conflict := RuntimeReleaseCohortConflict{Active: RuntimeReleaseIdentity{CombinationSHA256: "active"}, Joining: RuntimeReleaseIdentity{CombinationSHA256: "joining"}}
	if !errors.Is(conflict, ErrRuntimeReleaseConflict) || conflict.Error() == "" {
		t.Fatalf("conflict=%v", conflict)
	}
}

func TestRuntimeReleaseCohortCompatibilityIgnoresDiagnosticRuntimeVersion(t *testing.T) {
	active := RuntimeReleaseIdentity{
		ContractVersion: RuntimeReleaseIdentityVersion, BuildMode: "packaged", RuntimeVersion: "v1.2.3",
		RuntimeextContractVersion: "runtimeext-v1", RuntimeextContractSHA256: "runtimeext-hash",
		ConnectorContractVersion: "connector-v1", ConnectorContractSHA256: "connector-hash",
		ProjectDefinitionRegistrySHA256: "project-hash", ConnectorRegistrySHA256: "registry-hash",
		CombinationSHA256: "active-release-hash",
	}
	joining := active
	joining.RuntimeVersion = "v1.2.4"
	joining.CombinationSHA256 = "joining-release-hash"
	if !active.CohortCompatible(joining) {
		t.Fatal("runtime version split an otherwise compatible release cohort")
	}
	joining.RuntimeextContractSHA256 = "changed-runtimeext-hash"
	if active.CohortCompatible(joining) {
		t.Fatal("runtime extension contract change was accepted as cohort-compatible")
	}
}

func TestRuntimeReleaseCoordinationDependsOnProjectIdentityNotReplicaCount(t *testing.T) {
	identity := RuntimeReleaseIdentity{}
	if identity.Coordinated() {
		t.Fatal("empty Generic Runtime identity enabled release coordination")
	}
	identity.ProjectDefinitionRegistrySHA256 = "project-registry-hash"
	if !identity.Coordinated() {
		t.Fatal("project Runtime identity did not enable release coordination")
	}
}
