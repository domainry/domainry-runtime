package auditmodel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

const (
	AuditEventClassBusiness   = "business"
	AuditEventClassGovernance = "governance"
	AuditEventClassOperations = "operations"
)

var auditOperationsClassMarkers = []string{
	"break_glass", "support_session", "security", "recovery", "retry", "dead_letter", "lease", "fencing",
	"worker", "infrastructure", "runtime_operation", "scheduler_run", "job_run", "integration_event",
	"integration_outbox", "workflow_execution", "cleanup_job",
}

var auditGovernanceClassMarkers = []string{
	"identity", "role", "permission", "menu", "metadata", "definition", "change_plan", "policy",
	"connection", "secret", "configuration", "notification_template", "localized_text", "api_key",
}

func AuditEventClassMarkers(class string) []string {
	switch strings.TrimSpace(class) {
	case AuditEventClassOperations:
		return append([]string(nil), auditOperationsClassMarkers...)
	case AuditEventClassGovernance:
		return append([]string(nil), auditGovernanceClassMarkers...)
	default:
		return nil
	}
}

func ClassifyAuditEvent(event AuditEvent) string {
	value := strings.ToLower(strings.Join([]string{event.Event, event.ObjectKey}, " "))
	for _, marker := range auditOperationsClassMarkers {
		if strings.Contains(value, marker) {
			return AuditEventClassOperations
		}
	}
	for _, marker := range auditGovernanceClassMarkers {
		if strings.Contains(value, marker) {
			return AuditEventClassGovernance
		}
	}
	return AuditEventClassBusiness
}

type AuditEvent struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Event       string         `json:"event"`
	ObjectKey   string         `json:"object_key,omitempty"`
	RecordID    string         `json:"record_id,omitempty"`
	ActorID     string         `json:"actor_id"`
	RoleKey     string         `json:"role_key"`
	Summary     string         `json:"summary"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Before      map[string]any `json:"before,omitempty"`
	After       map[string]any `json:"after,omitempty"`
	CreatedAt   string         `json:"created_at"`
}

type AuditEventQuery struct {
	ObjectKey   string
	RecordID    string
	Event       string
	Class       string
	ActorID     string
	RoleKey     string
	RequestID   string
	CreatedFrom string
	CreatedTo   string
	Limit       int
	Cursor      string
}

type AuditEventCursor struct {
	CreatedAt string
	ID        string
}

type auditEventCursorPayload struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func EncodeAuditEventCursor(event AuditEvent) string {
	payload, err := json.Marshal(auditEventCursorPayload{
		Version:   1,
		CreatedAt: strings.TrimSpace(event.CreatedAt),
		ID:        strings.TrimSpace(event.ID),
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeAuditEventCursor(value string) (AuditEventCursor, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 2048 {
		return AuditEventCursor{}, errors.New("audit event cursor is empty or too large")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return AuditEventCursor{}, errors.New("audit event cursor is not base64url")
	}
	var payload auditEventCursorPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return AuditEventCursor{}, errors.New("audit event cursor is not valid JSON")
	}
	payload.CreatedAt = strings.TrimSpace(payload.CreatedAt)
	payload.ID = strings.TrimSpace(payload.ID)
	if payload.Version != 1 || payload.CreatedAt == "" || payload.ID == "" || len(payload.CreatedAt) > 128 || len(payload.ID) > 512 {
		return AuditEventCursor{}, errors.New("audit event cursor payload is invalid")
	}
	return AuditEventCursor{CreatedAt: payload.CreatedAt, ID: payload.ID}, nil
}

type AuditOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type AuditOptionQuery struct {
	Field       string
	Query       string
	ObjectKey   string
	CreatedFrom string
	CreatedTo   string
	Limit       int
}
