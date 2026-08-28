package agent

import (
	"context"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func (s *AgentTaskRunStore) ListAgentTaskRunsForWorker(ctx context.Context, scope principalmodel.SystemScope, filter agentrepository.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	if err := requireAgentTaskWorkerScope(scope); err != nil {
		return nil, err
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := "SELECT " + s.store.Identifier("scope_key") + " FROM " + s.store.TableIdentifier("runtime_worker_queue_scopes") + " WHERE " + s.store.Identifier("queue_kind") + " = " + s.store.Placeholder(1) + " ORDER BY " + s.store.Identifier("updated_at") + " DESC LIMIT " + s.store.Placeholder(2)
	rows, err := s.db.QueryContext(ctx, query, agentTaskWorkerQueueKind, min(256, max(32, limit*2)))
	if err != nil {
		return nil, err
	}
	workspaces := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		workspaces = append(workspaces, workspaceID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	out := []agentmodel.AgentTaskRun{}
	for _, workspaceID := range workspaces {
		workspaceCtx := requestcontext.WithWorkspaceID(ctx, workspaceID)
		runs, err := s.List(workspaceCtx, workspaceID, filter)
		if err != nil {
			return nil, err
		}
		out = append(out, runs...)
	}
	return capacity.FairOrder(out, limit, func(run agentmodel.AgentTaskRun) string { return run.WorkspaceID }), nil
}

func (s *AgentTaskRunStore) ClaimNextAgentTaskRunForWorker(ctx context.Context, scope principalmodel.SystemScope, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	if err := requireAgentTaskWorkerScope(scope); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	workspaces, err := s.store.WorkerQueueScopePage(ctx, s.db, agentTaskWorkerQueueKind, 64)
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	for _, workspaceID := range workspaces {
		workspaceCtx := requestcontext.WithWorkspaceID(ctx, workspaceID)
		claim, found, err := s.ClaimNext(workspaceCtx, workspaceID, owner, now, duration)
		if err != nil || found {
			return claim, found, err
		}
	}
	return agentrepository.AgentTaskClaim{}, false, nil
}

func requireAgentTaskWorkerScope(scope principalmodel.SystemScope) error {
	if !scope.Valid() || scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
		return principalmodel.ErrSystemScopeRequired
	}
	return nil
}

var _ agentrepository.AgentTaskRunSystemWorkerRepository = (*AgentTaskRunStore)(nil)
