package composition

import (
	"context"
	"sync"
	"time"

	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

const FrontendCapabilityManifestVersion = deploymentmodel.FrontendCapabilityManifestVersion

type FrontendCapabilityManifest = deploymentmodel.FrontendCapabilityManifest
type FrontendDeploymentEvidence = deploymentmodel.FrontendDeploymentEvidence
type FrontendCapabilitySupportEntry = deploymentmodel.FrontendCapabilitySupportEntry
type FrontendCapabilitySnapshot = deploymentmodel.FrontendCapabilitySnapshot
type FrontendCapabilityRequirement = deploymentmodel.FrontendCapabilityRequirement
type FrontendCapabilityManifestValidationResult = deploymentmodel.FrontendCapabilityManifestValidationResult
type FrontendCapabilityUsageValidationIssue = deploymentmodel.FrontendCapabilityUsageValidationIssue

func NewFrontendCapabilityApplicationService() *deploymentapplication.DeploymentFrontendCapabilityApplicationService {
	return newDeploymentFrontendCapabilityApplicationService(newCompositionFrontendCapabilityTestStore(), nil)
}

type compositionFrontendCapabilityTestStore struct {
	mu     sync.RWMutex
	values map[string]deploymentmodel.DeploymentFrontendCapabilityState
}

func newCompositionFrontendCapabilityTestStore() *compositionFrontendCapabilityTestStore {
	return &compositionFrontendCapabilityTestStore{values: map[string]deploymentmodel.DeploymentFrontendCapabilityState{}}
}

func (s *compositionFrontendCapabilityTestStore) Get(ctx context.Context, workspaceID string) (deploymentmodel.DeploymentFrontendCapabilityState, bool, error) {
	if err := ctx.Err(); err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[workspaceID]
	return value, ok, nil
}

func (s *compositionFrontendCapabilityTestStore) Put(ctx context.Context, workspaceID string, payload []byte) (deploymentmodel.DeploymentFrontendCapabilityState, error) {
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
