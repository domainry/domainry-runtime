package contract

import endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"

type CapabilityDiscoveryIndex struct {
	ContractVersion         string `json:"contract_version"`
	EndpointContractVersion string `json:"endpoint_contract_version"`
	RuntimeVersion          string `json:"runtime_version"`
	ContractHash            string `json:"contract_hash"`
	InstanceHash            string `json:"instance_hash"`
	EndpointContractCount   int    `json:"endpoint_contract_count"`
	EndpointContractsHash   string `json:"endpoint_contracts_hash"`
	// EndpointContracts is an explicit expansion. The default index keeps only
	// count and digest so discovery clients do not load every HTTP policy while
	// choosing an authoring capability.
	EndpointContracts []endpointmodel.RuntimeEndpointContractV1 `json:"endpoint_contracts,omitempty"`
	Domains           []CapabilityDomainSummary                 `json:"domains"`
}

type CapabilityDomainSummary struct {
	Key             string `json:"key"`
	CapabilityCount int    `json:"capability_count"`
	DetailEndpoint  string `json:"detail_endpoint"`
}

type CapabilityDomainDetail struct {
	ContractVersion         string              `json:"contract_version"`
	EndpointContractVersion string              `json:"endpoint_contract_version"`
	RuntimeVersion          string              `json:"runtime_version"`
	ContractHash            string              `json:"contract_hash"`
	InstanceHash            string              `json:"instance_hash"`
	Key                     string              `json:"key"`
	Capabilities            []CapabilitySummary `json:"capabilities"`
}

type CapabilitySummary struct {
	Key                string   `json:"key"`
	Status             string   `json:"status"`
	Lifecycle          string   `json:"lifecycle"`
	Requires           []string `json:"requires,omitempty"`
	ValidationEndpoint string   `json:"validation_endpoint,omitempty"`
	DetailEndpoint     string   `json:"detail_endpoint"`
}

type CapabilityDetail struct {
	ContractVersion string            `json:"contract_version"`
	RuntimeVersion  string            `json:"runtime_version"`
	ContractHash    string            `json:"contract_hash"`
	InstanceHash    string            `json:"instance_hash"`
	Domain          string            `json:"domain"`
	Selection       map[string]string `json:"selection,omitempty"`
	// Instance contains only references selected by the caller. An unselected
	// capability detail never regrows the workspace-wide authoring instance.
	Instance   *CapabilityAuthoringInstance  `json:"instance,omitempty"`
	Capability CapabilityAuthoringDefinition `json:"capability"`
}

type CapabilityReferenceResult struct {
	Kind         string   `json:"kind"`
	Scope        string   `json:"scope,omitempty"`
	InstanceHash string   `json:"instance_hash"`
	Values       []string `json:"values"`
}
