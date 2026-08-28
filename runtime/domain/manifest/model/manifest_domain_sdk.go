package manifestmodel

// GeneratedDomainSDKIdentity is the deployment target identity that a
// project-specific Runtime binary must match before activating a manifest.
type GeneratedDomainSDKIdentity struct {
	ContractVersion          string `json:"contract_version"`
	ContractSHA256           string `json:"contract_sha256"`
	GeneratorVersion         string `json:"generator_version"`
	MetadataSnapshotSHA256   string `json:"metadata_snapshot_sha256"`
	RuntimeextContractSHA256 string `json:"runtimeext_contract_sha256"`
	BuildConstraint          string `json:"build_constraint"`
	ArtifactSHA256           string `json:"artifact_sha256"`
}
