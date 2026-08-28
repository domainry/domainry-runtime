package contract

// CapabilityKind identifies a reusable business capability attached to an object.
type CapabilityKind string

const (
	CapabilityPromotion    CapabilityKind = "promotion"
	CapabilityPricing      CapabilityKind = "pricing"
	CapabilityLoyalty      CapabilityKind = "loyalty"
	CapabilityInventory    CapabilityKind = "inventory"
	CapabilityApproval     CapabilityKind = "approval"
	CapabilityStateMachine CapabilityKind = "state_machine"
)

// CapabilityConfig describes the runtime parameters of an attached capability.
type CapabilityConfig struct {
	Kind   CapabilityKind `json:"kind"`
	Config map[string]any `json:"config,omitempty"`
}

// CapabilityResult reports the outcome of a capability policy evaluation.
type CapabilityResult struct {
	Allowed bool           `json:"allowed"`
	Reason  string         `json:"reason,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}
