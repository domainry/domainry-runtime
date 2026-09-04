package profilebindingmodel

const (
	ContractVersion      = "identity-profile-extension"
	MinimumReaderVersion = "identity-profile-extension-reader"
)

type ClaimBinding struct {
	ClaimKey string `json:"claim_key"`
	FieldKey string `json:"field_key"`
}

type BusinessIdentityBinding struct {
	Key                string         `json:"key"`
	StatusField        string         `json:"status_field,omitempty"`
	ActiveStatusValues []string       `json:"active_status_values,omitempty"`
	BlacklistField     string         `json:"blacklist_field,omitempty"`
	Claims             []ClaimBinding `json:"claims,omitempty"`
}

type ClaimProof struct {
	Type     string `json:"type"`
	FieldKey string `json:"field_key"`
}

type Lifecycle struct {
	AllowUnbound           bool         `json:"allow_unbound,omitempty"`
	InvitationChannels     []string     `json:"invitation_channels,omitempty"`
	ClaimProofs            []ClaimProof `json:"claim_proofs,omitempty"`
	RebindRequiresApproval bool         `json:"rebind_requires_approval,omitempty"`
	RebindRevokesSessions  bool         `json:"rebind_revokes_sessions,omitempty"`
}

// Binding is the Runtime-owned metadata contract that joins
// one domain profile object to an Identity user.
type Binding struct {
	ContractVersion          string                  `json:"contract_version"`
	MinReaderVersion         string                  `json:"min_reader_version"`
	ObjectKey                string                  `json:"object_key"`
	IdentityRelationField    string                  `json:"identity_relation_field"`
	Cardinality              string                  `json:"cardinality"`
	BusinessIdentity         BusinessIdentityBinding `json:"business_identity"`
	BindingLifecycle         Lifecycle               `json:"binding_lifecycle,omitempty"`
	SummaryFields            []string                `json:"summary_fields,omitempty"`
	ProfileTabs              []string                `json:"profile_tabs,omitempty"`
	ProfileTabLabels         map[string]string       `json:"profile_tab_labels,omitempty"`
	ProfileTabFields         map[string][]string     `json:"profile_tab_fields,omitempty"`
	ProfileTabRelatedObjects map[string][]string     `json:"profile_tab_related_objects,omitempty"`
	ProfileTabComponents     map[string][]string     `json:"profile_tab_components,omitempty"`
	DefaultVisibility        string                  `json:"default_visibility"`
	RequiredPermissions      []string                `json:"required_permissions,omitempty"`
	StandaloneWorkspace      bool                    `json:"standalone_workspace,omitempty"`
	Provenance               *Provenance             `json:"provenance,omitempty"`
}

type Provenance struct {
	Owner        string   `json:"owner,omitempty"`
	Contributors []string `json:"contributors,omitempty"`
}

// ClaimValue is a business-record fact made available to SDK policy
// evaluation. It is not Identity authorization state.
type ClaimValue struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// Reference identifies the selected Runtime-owned business profile record.
type Reference struct {
	BindingKey string                `json:"binding_key"`
	ObjectKey  string                `json:"object_key"`
	RecordID   string                `json:"record_id"`
	Claims     map[string]ClaimValue `json:"claims,omitempty"`
}
