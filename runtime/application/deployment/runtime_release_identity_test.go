package deployment

import (
	"strings"
	"testing"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func TestRuntimeReleaseIdentityContainsOnlyRuntimeAndFrozenRegistries(t *testing.T) {
	identity := deploymentmodel.RuntimeReleaseIdentity{
		ContractVersion: deploymentmodel.RuntimeReleaseIdentityVersion,
		BuildMode:       "development", RuntimeVersion: "test",
		RuntimeextContractVersion: "runtimeext-v45", RuntimeextContractSHA256: strings.Repeat("a", 64),
		ConnectorContractVersion: "connector-v1", ConnectorContractSHA256: strings.Repeat("b", 64),
		ProjectDefinitionRegistrySHA256: strings.Repeat("c", 64), ConnectorRegistrySHA256: strings.Repeat("d", 64),
	}
	identity.CombinationSHA256, _ = RuntimeReleaseCombinationSHA256(identity)
	if err := ValidateRuntimeReleaseIdentity(identity); err != nil {
		t.Fatal(err)
	}
	identity.ProjectDefinitionRegistrySHA256 = "bad"
	if err := ValidateRuntimeReleaseIdentity(identity); err == nil {
		t.Fatal("malformed project registry identity was accepted")
	}
}
