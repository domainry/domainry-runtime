package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentSession struct {
	ExternalSessionID string         `json:"external_session_id"`
	AgentSessionID    string         `json:"agent_session_id,omitempty"`
	Title             string         `json:"title"`
	LastSummary       string         `json:"last_summary,omitempty"`
	Mode              string         `json:"mode,omitempty"`
	WorkspaceID       string         `json:"workspace_id,omitempty"`
	Surface           string         `json:"surface,omitempty"`
	ObjectKey         string         `json:"object_key,omitempty"`
	RecordID          string         `json:"record_id,omitempty"`
	UserID            string         `json:"user_id,omitempty"`
	Role              string         `json:"role,omitempty"`
	Archived          bool           `json:"archived"`
	Context           map[string]any `json:"context,omitempty"`
	CreatedAt         int64          `json:"created_at"`
	UpdatedAt         int64          `json:"updated_at"`
}

type AgentSessionUpsertRequest struct {
	ExternalSessionID string         `json:"external_session_id,omitempty"`
	AgentSessionID    string         `json:"agent_session_id,omitempty"`
	Title             string         `json:"title,omitempty"`
	LastSummary       string         `json:"last_summary,omitempty"`
	Mode              string         `json:"mode,omitempty"`
	Context           map[string]any `json:"context,omitempty"`
	Archived          bool           `json:"archived,omitempty"`
}

type AgentSessionQuery struct {
	Search, Surface, ObjectKey, RecordID string
	IncludeArchived                      bool
	Limit                                int
}

func (s *AgentApplicationService) ListSessions(ctx context.Context, query AgentSessionQuery, principal principalmodel.Principal) ([]AgentSession, error) {
	workspaceID, err := agentQueryWorkspace(principal)
	if err != nil {
		return nil, err
	}
	states, err := s.repository.List(ctx, workspaceID, "session", principal.UserID, principal.RoleKey)
	if err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	out := []AgentSession{}
	for _, state := range states {
		var record AgentSession
		if json.Unmarshal(state.Payload, &record) != nil || !agentSessionVisible(record, principal) || record.Archived && !query.IncludeArchived {
			continue
		}
		if query.Surface != "" && record.Surface != query.Surface || query.ObjectKey != "" && record.ObjectKey != query.ObjectKey || query.RecordID != "" && record.RecordID != query.RecordID || search != "" && !agentSessionMatches(record, search) {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *AgentApplicationService) UpsertSession(ctx context.Context, request AgentSessionUpsertRequest, principal principalmodel.Principal) (AgentSession, error) {
	workspaceID, err := agentCommandWorkspace(principal)
	if err != nil {
		return AgentSession{}, err
	}
	now := time.Now().UTC().UnixNano()
	externalID := strings.TrimSpace(request.ExternalSessionID)
	if externalID == "" {
		externalID = "session:" + agentStatePart(principal.UserID) + ":" + strconv.FormatInt(now, 36)
	}
	record := AgentSession{ExternalSessionID: externalID, AgentSessionID: strings.TrimSpace(request.AgentSessionID), Title: agentSessionTitle(request.Title, request.LastSummary), LastSummary: agentSessionSummary(request.LastSummary), Mode: strings.TrimSpace(request.Mode), WorkspaceID: workspaceID, UserID: principal.UserID, Role: principal.RoleKey, Archived: request.Archived, Context: cloneAgentContext(request.Context), CreatedAt: now, UpdatedAt: now}
	record.Surface, record.ObjectKey, record.RecordID = agentContextString(record.Context, "surface"), agentContextString(record.Context, "object_key"), agentContextString(record.Context, "record_id")
	key := agentStateKey(record.WorkspaceID, record.UserID, record.Role, externalID)
	if state, exists, err := s.repository.Get(ctx, workspaceID, "session", key); err != nil {
		return AgentSession{}, err
	} else if exists {
		var previous AgentSession
		_ = json.Unmarshal(state.Payload, &previous)
		record.CreatedAt = previous.CreatedAt
		if record.AgentSessionID == "" {
			record.AgentSessionID = previous.AgentSessionID
		}
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return AgentSession{}, err
	}
	if err := s.repository.Put(ctx, workspaceID, agentmodel.AgentStateRecord{Kind: "session", Key: key, WorkspaceID: record.WorkspaceID, UserID: record.UserID, RoleKey: record.Role, Payload: payload, UpdatedAt: record.UpdatedAt}); err != nil {
		return AgentSession{}, err
	}
	return record, nil
}

func (s *AgentApplicationService) SetSessionArchived(ctx context.Context, externalID string, archived bool, principal principalmodel.Principal) (AgentSession, error) {
	workspaceID, err := agentCommandWorkspace(principal)
	if err != nil {
		return AgentSession{}, err
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return AgentSession{}, badRequest("agent_dialog.session_id_required")
	}
	key := agentStateKey(workspaceID, principal.UserID, principal.RoleKey, externalID)
	state, ok, err := s.repository.Get(ctx, workspaceID, "session", key)
	if err != nil {
		return AgentSession{}, err
	}
	if !ok {
		return AgentSession{}, notFound("agent_dialog.session_not_found")
	}
	var record AgentSession
	if json.Unmarshal(state.Payload, &record) != nil || !agentSessionVisible(record, principal) {
		return AgentSession{}, notFound("agent_dialog.session_not_found")
	}
	record.Archived, record.UpdatedAt = archived, time.Now().UTC().UnixNano()
	payload, _ := json.Marshal(record)
	if err := s.repository.Put(ctx, workspaceID, agentmodel.AgentStateRecord{Kind: "session", Key: key, WorkspaceID: record.WorkspaceID, UserID: record.UserID, RoleKey: record.Role, Payload: payload, UpdatedAt: record.UpdatedAt}); err != nil {
		return AgentSession{}, err
	}
	return record, nil
}

func agentStatePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "anonymous"
	}
	return strings.ReplaceAll(value, ":", "_")
}
func agentStateKey(workspaceID, userID, role, id string) string {
	return agentStatePart(workspaceID) + ":" + agentStatePart(userID) + ":" + agentStatePart(role) + ":" + id
}
func agentSessionVisible(record AgentSession, principal principalmodel.Principal) bool {
	return agentStatePart(record.WorkspaceID) == agentStatePart(principal.WorkspaceID) && agentStatePart(record.UserID) == agentStatePart(principal.UserID) && agentStatePart(record.Role) == agentStatePart(principal.RoleKey)
}
func agentSessionMatches(record AgentSession, search string) bool {
	return strings.Contains(strings.ToLower(record.Title+" "+record.LastSummary+" "+record.ExternalSessionID+" "+record.AgentSessionID), search)
}
func agentSessionTitle(title, summary string) string {
	if value := strings.TrimSpace(title); value != "" {
		return truncateAgentText(value, 120)
	}
	if value := strings.TrimSpace(summary); value != "" {
		return truncateAgentText(value, 120)
	}
	return "New conversation"
}
func agentSessionSummary(value string) string {
	return truncateAgentText(strings.TrimSpace(value), 500)
}
func truncateAgentText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
func cloneAgentContext(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
func agentContextString(value map[string]any, key string) string {
	text := strings.TrimSpace(fmt.Sprint(value[key]))
	if text == "<nil>" {
		return ""
	}
	return text
}
