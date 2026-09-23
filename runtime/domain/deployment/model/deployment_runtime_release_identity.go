package deploymentmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// RuntimeReleaseIdentityVersion is the canonical Deployment process build/composition
// identity compared by the shared release-cohort admission gate.
const RuntimeReleaseIdentityVersion = "domainry-runtime-release-identity-v3"

// RuntimeReleaseIdentity binds one Runtime binary to the frozen code-owned
// project definition and Connector registries. Generated SDK, Builder receipt,
// Blueprint and source-artifact identities are intentionally absent.
type RuntimeReleaseIdentity struct {
	ContractVersion                 string `json:"contract_version"`
	BuildMode                       string `json:"build_mode"`
	RuntimeVersion                  string `json:"runtime_version"`
	RuntimeextContractVersion       string `json:"runtimeext_contract_version"`
	RuntimeextContractSHA256        string `json:"runtimeext_contract_sha256"`
	ConnectorContractVersion        string `json:"connector_contract_version"`
	ConnectorContractSHA256         string `json:"connector_contract_sha256"`
	ProjectDefinitionRegistrySHA256 string `json:"project_definition_registry_sha256"`
	ConnectorRegistrySHA256         string `json:"connector_registry_sha256"`
	CombinationSHA256               string `json:"combination_sha256"`
}

func (i RuntimeReleaseIdentity) Coordinated() bool {
	return i.ProjectDefinitionRegistrySHA256 != ""
}

// RuntimeRegistrySHA256 hashes the frozen public descriptor inventory used by
// both runtimehost identity publication and Runtime readiness drift checks.
func RuntimeRegistrySHA256(contractVersion string, descriptors any) (string, error) {
	payload, err := json.Marshal(struct {
		ContractVersion string `json:"contract_version"`
		Descriptors     any    `json:"descriptors"`
	}{ContractVersion: contractVersion, Descriptors: descriptors})
	if err != nil {
		return "", fmt.Errorf("encode registry identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
