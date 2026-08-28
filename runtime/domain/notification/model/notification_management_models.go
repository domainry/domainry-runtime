package notificationmodel

type NotificationTemplateRecord struct {
	Key              string                `json:"key"`
	Draft            *NotificationTemplate `json:"draft,omitempty"`
	Published        *NotificationTemplate `json:"published,omitempty"`
	PublishedVersion int                   `json:"published_version"`
	Status           string                `json:"status"`
	UpdatedBy        string                `json:"updated_by,omitempty"`
	CreatedAt        string                `json:"created_at,omitempty"`
	UpdatedAt        string                `json:"updated_at,omitempty"`
}

type NotificationTemplateVersion struct {
	TemplateKey string               `json:"template_key"`
	Version     int                  `json:"version"`
	Template    NotificationTemplate `json:"template"`
	ContentHash string               `json:"content_hash"`
	PublishedBy string               `json:"published_by,omitempty"`
	PublishedAt string               `json:"published_at"`
}

type NotificationPublicationRequest struct {
	ID               string               `json:"id"`
	TemplateKey      string               `json:"template_key"`
	Snapshot         NotificationTemplate `json:"snapshot"`
	CandidateHash    string               `json:"candidate_hash"`
	DraftUpdatedAt   string               `json:"draft_updated_at"`
	Status           string               `json:"status"`
	ScheduledFor     string               `json:"scheduled_for,omitempty"`
	RequestedBy      string               `json:"requested_by"`
	RequestedAt      string               `json:"requested_at"`
	ReviewedBy       string               `json:"reviewed_by,omitempty"`
	ReviewedAt       string               `json:"reviewed_at,omitempty"`
	PublishedVersion int                  `json:"published_version,omitempty"`
	Failure          string               `json:"failure,omitempty"`
	LeaseOwner       string               `json:"lease_owner,omitempty"`
	LeaseExpiresAt   string               `json:"lease_expires_at,omitempty"`
	FencingToken     int64                `json:"fencing_token,omitempty"`
	UpdatedAt        string               `json:"updated_at"`
}

type NotificationPublicationTransition struct {
	Status               string
	ScheduledFor         string
	ReviewedBy           string
	ReviewedAt           string
	PublishedVersion     int
	Failure              string
	ExpectedLeaseOwner   string
	ExpectedFencingToken int64
	UpdatedAt            string
}

type NotificationDeliveryPolicy struct {
	Enabled                bool     `json:"enabled"`
	QuietHoursEnabled      bool     `json:"quiet_hours_enabled"`
	QuietStart             string   `json:"quiet_start"`
	QuietEnd               string   `json:"quiet_end"`
	Timezone               string   `json:"timezone"`
	MaxPerRecipientPerHour int      `json:"max_per_recipient_per_hour"`
	DedupeWindowSeconds    int      `json:"dedupe_window_seconds"`
	FallbackChannels       []string `json:"fallback_channels,omitempty"`
	UpdatedBy              string   `json:"updated_by,omitempty"`
	UpdatedAt              string   `json:"updated_at,omitempty"`
}

type NotificationRecipientPreference struct {
	RecipientKey      string          `json:"recipient_key"`
	EnabledChannels   map[string]bool `json:"enabled_channels"`
	MutedTemplateKeys []string        `json:"muted_template_keys,omitempty"`
	UpdatedBy         string          `json:"updated_by,omitempty"`
	UpdatedAt         string          `json:"updated_at,omitempty"`
}

type NotificationDeliveryReservation struct {
	ID, RecipientKey, TemplateKey, Channel, DedupeKey, CreatedAt string
}

type NotificationDeliveryEvaluationRequest struct {
	WorkspaceID string
	TemplateKey string
	Channel     string
	Recipients  []string
	DedupeKey   string
}

type NotificationDeliveryDecision struct {
	DeliverAfter  string   `json:"deliver_after,omitempty"`
	FallbackOrder []string `json:"fallback_order,omitempty"`
}
