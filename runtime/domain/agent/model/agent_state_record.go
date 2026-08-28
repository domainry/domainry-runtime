package agentmodel

import "encoding/json"

type AgentStateRecord struct {
	Kind        string          `json:"kind"`
	Key         string          `json:"key"`
	WorkspaceID string          `json:"workspace_id"`
	UserID      string          `json:"user_id"`
	RoleKey     string          `json:"role_key"`
	Payload     json.RawMessage `json:"payload"`
	UpdatedAt   int64           `json:"updated_at"`
}
