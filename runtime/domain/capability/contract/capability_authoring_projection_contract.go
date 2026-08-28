package contract

type CapabilityAuthoringProjection struct {
	Mode            string   `json:"mode"`
	Successor       string   `json:"successor"`
	ContractVersion string   `json:"contract_version"`
	ContractHash    string   `json:"contract_hash"`
	Domains         []string `json:"domains"`
}
