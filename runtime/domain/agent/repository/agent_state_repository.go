package repository

import (
	"context"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

type AgentStateRepository interface {
	List(ctx context.Context, workspaceID, kind, userID, roleKey string) ([]agentmodel.AgentStateRecord, error)
	Get(ctx context.Context, workspaceID, kind, key string) (agentmodel.AgentStateRecord, bool, error)
	Put(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord) error
	PutBatch(ctx context.Context, workspaceID string, values []agentmodel.AgentStateRecord) error
	CompareAndSwap(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord, expectedUpdatedAt int64) (bool, error)
}
