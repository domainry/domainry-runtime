package auditmodel

// AuditBusinessExportFilter is the closed filter vocabulary for the
// Runtime-owned business audit report source. It is never interpreted as SQL.
type AuditBusinessExportFilter struct {
	Event       string `json:"event,omitempty"`
	ObjectKey   string `json:"object_key,omitempty"`
	RecordID    string `json:"record_id,omitempty"`
	ActorID     string `json:"actor_id,omitempty"`
	RoleKey     string `json:"role_key,omitempty"`
	Result      string `json:"result,omitempty"`
	CreatedFrom string `json:"created_from,omitempty"`
	CreatedTo   string `json:"created_to,omitempty"`
}

type AuditBusinessExportRequest struct {
	Filters AuditBusinessExportFilter `json:"filters"`
	Format  string                    `json:"format,omitempty"`
}

// AuditBusinessExportArtifact is the immutable, Runtime-owned final-byte
// record. TokenHash is persisted instead of the bearer token.
type AuditBusinessExportArtifact struct {
	ID                       string                    `json:"id"`
	WorkspaceID              string                    `json:"workspace_id"`
	RequesterUserID          string                    `json:"requester_user_id"`
	RoleKey                  string                    `json:"role_key"`
	IdempotencyKey           string                    `json:"idempotency_key"`
	Filters                  AuditBusinessExportFilter `json:"filters"`
	ScopeSHA256              string                    `json:"scope_sha256"`
	AuthorizationScopeSHA256 string                    `json:"authorization_scope_sha256"`
	TokenSHA256              string                    `json:"token_sha256"`
	Filename                 string                    `json:"filename"`
	ContentSHA256            string                    `json:"content_sha256"`
	RowCount                 int                       `json:"row_count"`
	Content                  []byte                    `json:"-"`
	AuditIdentity            string                    `json:"audit_identity"`
	Status                   string                    `json:"status"`
	CreatedAt                string                    `json:"created_at"`
	ExpiresAt                string                    `json:"expires_at"`
	DownloadCount            int                       `json:"download_count"`
	LastDownloadedAt         string                    `json:"last_downloaded_at,omitempty"`
}
