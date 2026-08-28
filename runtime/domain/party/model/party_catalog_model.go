package partymodel

type JobCatalogItem struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Family      string `json:"family,omitempty"`
	Level       string `json:"level,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
}

type Position struct {
	ID                 string `json:"id"`
	Code               string `json:"code"`
	Name               string `json:"name"`
	JobCatalogItemID   string `json:"job_catalog_item_id"`
	OrganizationUnitID string `json:"organization_unit_id,omitempty"`
	Headcount          int    `json:"headcount"`
	EffectiveFrom      string `json:"effective_from,omitempty"`
	EffectiveTo        string `json:"effective_to,omitempty"`
	Status             string `json:"status"`
}
