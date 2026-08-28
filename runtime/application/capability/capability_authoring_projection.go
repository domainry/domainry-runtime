package capability

import (
	"sort"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

type CapabilityMetadataAuthoringProjection struct {
	AuthoringProjection   capabilitycontract.CapabilityAuthoringProjection   `json:"authoring_projection"`
	Capabilities          []capabilitycontract.CapabilityKind                `json:"capabilities"`
	AuthoringCapabilities []capabilitycontract.CapabilityAuthoringDefinition `json:"authoring_capabilities"`
}

func RuntimeAuthoringProjection(domainKeys ...string) capabilitycontract.CapabilityAuthoringProjection {
	contract := RuntimeAuthoringCapabilities()
	domains := append([]string(nil), domainKeys...)
	sort.Strings(domains)
	return capabilitycontract.CapabilityAuthoringProjection{
		Mode: "compatibility_projection", Successor: "/tenant-admin/platform-capabilities",
		ContractVersion: contract.ContractVersion, ContractHash: contract.ContractHash, Domains: domains,
	}
}

func CapabilityLegacyObjectKinds() []capabilitycontract.CapabilityKind {
	return []capabilitycontract.CapabilityKind{
		capabilitycontract.CapabilityPromotion,
		capabilitycontract.CapabilityPricing,
		capabilitycontract.CapabilityLoyalty,
		capabilitycontract.CapabilityInventory,
		capabilitycontract.CapabilityApproval,
		capabilitycontract.CapabilityStateMachine,
	}
}
