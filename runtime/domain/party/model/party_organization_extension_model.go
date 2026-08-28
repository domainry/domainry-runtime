package partymodel

const (
	OrganizationExtensionTerritory = "territory"
	OrganizationExtensionTeam      = "team"
	OrganizationExtensionStore     = "store"
	OrganizationExtensionWarehouse = "warehouse"
)

type OrganizationExtension struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Code               string `json:"code"`
	Name               string `json:"name"`
	ClaimValue         string `json:"claim_value,omitempty"`
	ParentID           string `json:"parent_id,omitempty"`
	OrganizationUnitID string `json:"organization_unit_id,omitempty"`
	Status             string `json:"status"`
}

type OrganizationExtensionMembership struct {
	ID                 string `json:"id"`
	ExtensionID        string `json:"extension_id"`
	WorkforceProfileID string `json:"workforce_profile_id"`
	EffectiveFrom      string `json:"effective_from,omitempty"`
	EffectiveTo        string `json:"effective_to,omitempty"`
	Status             string `json:"status"`
}

// OrganizationScopeFacts are Runtime-owned Party extension facts projected
// into the Identity SDK subject when an application assembles a principal.
// They are not an authorization model and do not grant access by themselves.
type OrganizationScopeFacts struct {
	TeamIDs      []string `json:"team_ids"`
	StoreIDs     []string `json:"store_ids"`
	TerritoryIDs []string `json:"territory_ids"`
	WarehouseIDs []string `json:"warehouse_ids"`
}
