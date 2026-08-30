package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentMemoryStateRepository struct {
	mu     sync.RWMutex
	values map[string]agentmodel.AgentStateRecord
}

func NewAgentMemoryStateRepository() *AgentMemoryStateRepository {
	return &AgentMemoryStateRepository{values: map[string]agentmodel.AgentStateRecord{}}
}

func agentStateMemoryKey(workspaceID, kind, key string) string {
	return strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(kind) + "\x00" + strings.TrimSpace(key)
}

func (r *AgentMemoryStateRepository) Put(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord) error {
	return r.PutBatch(ctx, workspaceID, []agentmodel.AgentStateRecord{value})
}

func (r *AgentMemoryStateRepository) PutBatch(ctx context.Context, workspaceID string, values []agentmodel.AgentStateRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	workspaceID = workspace.String()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, value := range values {
		if strings.TrimSpace(value.WorkspaceID) != workspaceID {
			return fmt.Errorf("agent state workspace %q does not match repository workspace %q", value.WorkspaceID, workspaceID)
		}
		value.Payload = append(json.RawMessage(nil), value.Payload...)
		r.values[agentStateMemoryKey(workspaceID, value.Kind, value.Key)] = value
	}
	return nil
}

func (r *AgentMemoryStateRepository) CompareAndSwap(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord, expectedUpdatedAt int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	key := agentStateMemoryKey(workspace.String(), value.Kind, value.Key)
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.values[key]
	if !ok || current.UpdatedAt != expectedUpdatedAt {
		return false, nil
	}
	value.Payload = append(json.RawMessage(nil), value.Payload...)
	r.values[key] = value
	return true, nil
}

func (r *AgentMemoryStateRepository) Get(ctx context.Context, workspaceID, kind, key string) (agentmodel.AgentStateRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return agentmodel.AgentStateRecord{}, false, err
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return agentmodel.AgentStateRecord{}, false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[agentStateMemoryKey(workspace.String(), kind, key)]
	value.Payload = append(json.RawMessage(nil), value.Payload...)
	return value, ok, nil
}

func (r *AgentMemoryStateRepository) List(ctx context.Context, workspaceID, kind, userID, roleKey string) ([]agentmodel.AgentStateRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID = workspace.String()
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []agentmodel.AgentStateRecord{}
	for _, value := range r.values {
		if value.Kind != kind || value.WorkspaceID != workspaceID || (userID != "" && value.UserID != userID) || (roleKey != "" && value.RoleKey != roleKey) {
			continue
		}
		value.Payload = append(json.RawMessage(nil), value.Payload...)
		out = append(out, value)
	}
	return out, nil
}
