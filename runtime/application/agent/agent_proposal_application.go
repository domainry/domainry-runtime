package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentProposal struct {
	ProposalID     string         `json:"proposal_id"`
	Status         string         `json:"status"`
	Title          string         `json:"title,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	Source         string         `json:"source,omitempty"`
	Reference      string         `json:"reference,omitempty"`
	Actor          string         `json:"actor,omitempty"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	UserID         string         `json:"user_id,omitempty"`
	Role           string         `json:"role,omitempty"`
	Proposed       map[string]any `json:"proposed,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Execution      map[string]any `json:"execution,omitempty"`
	DecisionReason string         `json:"decision_reason,omitempty"`
	DecisionActor  string         `json:"decision_actor,omitempty"`
	CreatedAt      int64          `json:"created_at"`
	UpdatedAt      int64          `json:"updated_at"`
	DecidedAt      int64          `json:"decided_at,omitempty"`
	Audited        bool           `json:"audited"`
}

func (s *AgentApplicationService) ListProposals(ctx context.Context, status string, principal principalmodel.Principal) ([]AgentProposal, error) {
	workspaceID, err := agentQueryWorkspace(principal)
	if err != nil {
		return nil, err
	}
	states, err := s.repository.List(ctx, workspaceID, "proposal", principal.UserID, principal.RoleKey)
	if err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	out := []AgentProposal{}
	for _, state := range states {
		var value AgentProposal
		if json.Unmarshal(state.Payload, &value) != nil || !agentProposalVisible(value, principal) || status != "" && value.Status != status {
			continue
		}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}
func (s *AgentApplicationService) GetProposal(ctx context.Context, proposalID string, principal principalmodel.Principal) (AgentProposal, error) {
	workspaceID, err := agentQueryWorkspace(principal)
	if err != nil {
		return AgentProposal{}, err
	}
	state, ok, err := s.repository.Get(ctx, workspaceID, "proposal", agentStateKey(workspaceID, principal.UserID, principal.RoleKey, agentStatePart(proposalID)))
	if err != nil {
		return AgentProposal{}, err
	}
	if !ok {
		return AgentProposal{}, notFound("agent_dialog.proposal_not_found")
	}
	var value AgentProposal
	if json.Unmarshal(state.Payload, &value) != nil || !agentProposalVisible(value, principal) {
		return AgentProposal{}, notFound("agent_dialog.proposal_not_found")
	}
	return value, nil
}
func (s *AgentApplicationService) StoreProposal(ctx context.Context, record AgentProposal) (AgentProposal, error) {
	scope, err := principalmodel.NewWorkspaceCommandScope(record.WorkspaceID)
	if err != nil {
		return AgentProposal{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	record.WorkspaceID = scope.WorkspaceID().String()
	if record.Status == "" {
		record.Status = "draft"
	}
	now := time.Now().UTC().UnixNano()
	if record.CreatedAt == 0 {
		record.CreatedAt = now
	}
	record.UpdatedAt, record.Audited = now, true
	payload, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	key := agentStateKey(record.WorkspaceID, record.UserID, record.Role, agentStatePart(record.ProposalID))
	err = s.repository.Put(ctx, record.WorkspaceID, agentmodel.AgentStateRecord{Kind: "proposal", Key: key, WorkspaceID: record.WorkspaceID, UserID: record.UserID, RoleKey: record.Role, Payload: payload, UpdatedAt: record.UpdatedAt})
	return record, err
}
func (s *AgentApplicationService) DecideProposal(ctx context.Context, proposalID, decision, reason string, metadata, execution map[string]any, principal principalmodel.Principal) (AgentProposal, error) {
	value, err := s.GetProposal(ctx, proposalID, principal)
	if err != nil {
		return AgentProposal{}, err
	}
	return s.decideProposalCAS(ctx, value, decision, reason, metadata, execution, principal)
}

// decideProposalCAS applies a decision to the exact proposal version observed by
// the caller. Keeping the observed version avoids a stale decision re-reading a
// newer winner and then overwriting it with a second, otherwise valid CAS.
func (s *AgentApplicationService) decideProposalCAS(ctx context.Context, value AgentProposal, decision, reason string, metadata, execution map[string]any, principal principalmodel.Principal) (AgentProposal, error) {
	expectedUpdatedAt := value.UpdatedAt
	now := time.Now().UTC().UnixNano()
	value.Status, value.DecisionActor, value.DecisionReason, value.DecidedAt, value.UpdatedAt, value.Audited = decision, strings.TrimSpace(principal.UserID), strings.TrimSpace(reason), now, now, true
	if metadata != nil {
		value.Metadata = cloneAgentContext(metadata)
	}
	if execution != nil {
		value.Execution = cloneAgentContext(execution)
	}
	return s.storeProposalCAS(ctx, value, expectedUpdatedAt)
}

func (s *AgentApplicationService) storeProposalCAS(ctx context.Context, record AgentProposal, expectedUpdatedAt int64) (AgentProposal, error) {
	payload, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	key := agentStateKey(record.WorkspaceID, record.UserID, record.Role, agentStatePart(record.ProposalID))
	updated, err := s.repository.CompareAndSwap(ctx, record.WorkspaceID, agentmodel.AgentStateRecord{Kind: "proposal", Key: key, WorkspaceID: record.WorkspaceID, UserID: record.UserID, RoleKey: record.Role, Payload: payload, UpdatedAt: record.UpdatedAt}, expectedUpdatedAt)
	if err != nil {
		return record, err
	}
	if !updated {
		return record, apperror.New(apperror.KindConflict, "agent_dialog.proposal_decision_conflict", nil, nil)
	}
	return record, nil
}
func agentProposalVisible(value AgentProposal, principal principalmodel.Principal) bool {
	return value.WorkspaceID == principal.WorkspaceID && value.UserID == principal.UserID && value.Role == principal.RoleKey
}
