package integrationmodel

type IntegrationAPIKey struct {
	Key         string   `json:"key"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Name        string   `json:"name,omitempty"`
	TokenPrefix string   `json:"token_prefix,omitempty"`
	TokenHash   string   `json:"-"`
	ActorID     string   `json:"actor_id"`
	RoleKey     string   `json:"role_key"`
	Scopes      []string `json:"scopes,omitempty"`
	Status      string   `json:"status"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	LastUsedAt  string   `json:"last_used_at,omitempty"`
	CreatedBy   string   `json:"created_by,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	DisabledAt  string   `json:"disabled_at,omitempty"`
}

type IntegrationAPIKeyCreateRequest struct {
	Key       string   `json:"key,omitempty"`
	Name      string   `json:"name,omitempty"`
	ActorID   string   `json:"actor_id"`
	RoleKey   string   `json:"role_key"`
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

type IntegrationAPIKeyCreateResult struct {
	APIKey IntegrationAPIKey `json:"api_key"`
	Token  string            `json:"token"`
}

type IntegrationAPIKeyRotateResult struct {
	APIKey IntegrationAPIKey `json:"api_key"`
	Token  string            `json:"token"`
}

type IntegrationEntrypointWorkflowRequest struct {
	ExternalIdentity IntegrationExternalIdentityResolveRequest `json:"external_identity"`
	Payload          map[string]any                            `json:"payload,omitempty"`
}

type IntegrationEntrypointActionRequest struct {
	ExternalIdentity IntegrationExternalIdentityResolveRequest `json:"external_identity"`
	Data             map[string]any                            `json:"data,omitempty"`
	IdempotencyKey   string                                    `json:"idempotency_key,omitempty"`
}
