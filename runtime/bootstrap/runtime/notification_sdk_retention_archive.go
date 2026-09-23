package runtime

import (
	"context"
	"fmt"
	"sync"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-notification-sdk/modulehost"
)

// notificationSDKRetentionArchiveStore is bound after Lifecycle opens. The
// proxy lets Notification start earlier without creating its own archive
// table; calls fail closed until the shared Lifecycle capability is mounted.
type notificationSDKRetentionArchiveStore struct {
	mu     sync.RWMutex
	target lifecyclecontract.ArchiveStore
}

func (s *notificationSDKRetentionArchiveStore) Bind(target lifecyclecontract.ArchiveStore) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.target = target
	s.mu.Unlock()
}

func (s *notificationSDKRetentionArchiveStore) store() lifecyclecontract.ArchiveStore {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.target
}

func (s *notificationSDKRetentionArchiveStore) Archived(ctx context.Context, workspaceID, sourceTable, resourceID, policyKey string) (bool, error) {
	target := s.store()
	if target == nil {
		return false, fmt.Errorf("shared Lifecycle retention archive store is unavailable")
	}
	return target.Archived(ctx, workspaceID, sourceTable, resourceID, policyKey)
}

func (s *notificationSDKRetentionArchiveStore) ArchivePayload(ctx context.Context, owner string, job modulehost.RetentionArchiveJob, policy modulehost.RetentionArchivePolicy, sourceTable, resourceID string, payload []byte) (bool, error) {
	target := s.store()
	if target == nil {
		return false, fmt.Errorf("shared Lifecycle retention archive store is unavailable")
	}
	return target.ArchivePayload(ctx, owner,
		lifecyclemodel.CleanupJob{ID: job.ID, WorkspaceID: job.WorkspaceID, PolicyKey: policy.Key, PolicyVersion: policy.Version, UpdatedAt: job.ArchivedAt},
		lifecyclemodel.PolicyVersion{WorkspaceID: job.WorkspaceID, Policy: lifecyclemodel.RetentionPolicy{Key: policy.Key, Version: policy.Version, Owner: owner}},
		sourceTable, resourceID, payload)
}

var _ modulehost.RetentionArchiveStore = (*notificationSDKRetentionArchiveStore)(nil)
