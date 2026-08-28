package deployment

import (
	"context"
	"sync"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
)

type deploymentFrontendCapabilityTestStore struct {
	mu     sync.RWMutex
	values map[string]deploymentmodel.DeploymentFrontendCapabilityState
}

func newDeploymentFrontendCapabilityTestStore() deploymentrepository.DeploymentFrontendCapabilityRepository {
	return &deploymentFrontendCapabilityTestStore{values: map[string]deploymentmodel.DeploymentFrontendCapabilityState{}}
}

func (s *deploymentFrontendCapabilityTestStore) Get(ctx context.Context, workspaceID string) (deploymentmodel.DeploymentFrontendCapabilityState, bool, error) {
	if err := ctx.Err(); err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[workspaceID]
	return value, ok, nil
}

func (s *deploymentFrontendCapabilityTestStore) Put(ctx context.Context, workspaceID string, payload []byte) (deploymentmodel.DeploymentFrontendCapabilityState, error) {
	if err := ctx.Err(); err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.values[workspaceID]
	value.WorkspaceID, value.Revision, value.ManifestJSON, value.UpdatedAt = workspaceID, value.Revision+1, append([]byte(nil), payload...), time.Now().UTC().Format(time.RFC3339Nano)
	s.values[workspaceID] = value
	return value, nil
}
