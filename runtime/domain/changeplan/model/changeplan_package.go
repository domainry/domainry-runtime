package changeplanmodel

import "encoding/json"

const BusinessSystemPackageVersion = "domain-system-package-v1"

type BusinessSystemPackage struct {
	PackageVersion           string                            `json:"package_version"`
	PackageHash              string                            `json:"package_hash"`
	SourcePlanID             string                            `json:"source_plan_id"`
	SourceRevision           int                               `json:"source_revision"`
	RuntimeVersion           string                            `json:"runtime_version"`
	AuthoringContractVersion string                            `json:"authoring_contract_version"`
	AuthoringContractHash    string                            `json:"authoring_contract_hash"`
	Resources                []BusinessSystemPackageResource   `json:"resources"`
	Dependencies             []BusinessSystemPackageDependency `json:"dependencies"`
	EmptyWorkspaceApplyPlan  BusinessSystemPackageApplyPlan    `json:"empty_workspace_apply_plan"`
	AcceptanceScenarios      []BusinessAcceptanceScenario      `json:"acceptance_scenarios"`
}

type BusinessSystemPackageResource struct {
	ResourceType string          `json:"resource_type"`
	ResourceKey  string          `json:"resource_key"`
	ResourceHash string          `json:"resource_hash"`
	Payload      json.RawMessage `json:"payload"`
}

type BusinessSystemPackageDependency struct {
	FromResourceType string `json:"from_resource_type"`
	FromResourceKey  string `json:"from_resource_key"`
	ToResourceType   string `json:"to_resource_type"`
	ToResourceKey    string `json:"to_resource_key"`
	Reason           string `json:"reason,omitempty"`
}

type BusinessSystemPackageApplyPlan struct {
	Target                   string                                `json:"target"`
	RequiresSnapshotBinding  bool                                  `json:"requires_snapshot_binding"`
	RequiresReferenceBinding bool                                  `json:"requires_reference_graph_binding"`
	Operations               []BusinessSystemPackageApplyOperation `json:"operations"`
}

type BusinessSystemPackageApplyOperation struct {
	Operation    string          `json:"operation"`
	ResourceType string          `json:"resource_type"`
	ResourceKey  string          `json:"resource_key"`
	Payload      json.RawMessage `json:"payload"`
}
