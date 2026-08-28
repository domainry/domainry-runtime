package deploymentmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// RuntimeReleaseIdentityVersion is the canonical Deployment process build/composition
// identity compared by the shared release-cohort admission gate.
const RuntimeReleaseIdentityVersion = "domainry-runtime-release-identity-v2"

// RuntimeReleaseIdentity binds one Runtime binary, project source/SDK,
// Metadata Snapshot and frozen Handler/Connector registries. It is deliberately
// owned below transport because deployment admission and HTTP diagnostics both
// consume the same immutable fact set.
type RuntimeReleaseIdentity struct {
	ContractVersion           string `json:"contract_version"`
	BuildMode                 string `json:"build_mode"`
	RuntimeVersion            string `json:"runtime_version"`
	RuntimeextContractVersion string `json:"runtimeext_contract_version"`
	RuntimeextContractSHA256  string `json:"runtimeext_contract_sha256"`
	ConnectorContractVersion  string `json:"connector_contract_version"`
	ConnectorContractSHA256   string `json:"connector_contract_sha256"`
	DomainSDKContractVersion  string `json:"domain_sdk_contract_version"`
	DomainSDKContractSHA256   string `json:"domain_sdk_contract_sha256"`
	DomainSDKGeneratorVersion string `json:"domain_sdk_generator_version"`
	DomainSDKBuildConstraint  string `json:"domain_sdk_build_constraint"`
	MetadataSnapshotSHA256    string `json:"metadata_snapshot_sha256"`
	GeneratedSDKSHA256        string `json:"generated_sdk_sha256"`
	ProjectModule             string `json:"project_module"`
	VerificationReceiptSHA256 string `json:"verification_receipt_sha256"`
	ProjectInputSHA256        string `json:"project_input_sha256"`
	ProjectSourceSHA256       string `json:"project_source_sha256"`
	HandlerCatalogSHA256      string `json:"handler_catalog_sha256"`
	HandlerRegistrySHA256     string `json:"handler_registry_sha256"`
	ConnectorRegistrySHA256   string `json:"connector_registry_sha256"`
	SigningKeyID              string `json:"signing_key_id"`
	SigningPublicKeySHA256    string `json:"signing_public_key_sha256"`
	CombinationSHA256         string `json:"combination_sha256"`
}

func (i RuntimeReleaseIdentity) Coordinated() bool {
	// The repository's generic Runtime has no project Domain SDK/Snapshot and
	// therefore no project cohort to compare. Any project-scoped fact activates
	// strict validation so a partial identity cannot disguise itself as generic.
	return i.DomainSDKContractVersion != "" || i.DomainSDKContractSHA256 != "" || i.DomainSDKGeneratorVersion != "" || i.DomainSDKBuildConstraint != "" || i.MetadataSnapshotSHA256 != "" || i.GeneratedSDKSHA256 != ""
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
