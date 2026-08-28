package integrationmodel

type WebPushSubscription struct {
	ID           string `json:"id"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	UserID       string `json:"user_id"`
	EndpointHash string `json:"endpoint_hash"`
	Endpoint     string `json:"-"`
	P256DH       string `json:"-"`
	Auth         string `json:"-"`
	Status       string `json:"status"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	RevokedAt    string `json:"revoked_at,omitempty"`
}

type WebPushSubscriptionUpsertRequest struct {
	Endpoint  string `json:"endpoint"`
	P256DH    string `json:"p256dh"`
	Auth      string `json:"auth"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type WebPushReadiness struct {
	Ready         bool   `json:"ready"`
	PublicKey     string `json:"public_key,omitempty"`
	ConnectionKey string `json:"connection_key,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
}
