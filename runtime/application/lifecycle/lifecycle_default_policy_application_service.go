package lifecycle

import (
	"context"
	"fmt"
	"time"

	lifecyclepolicy "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func (s *LifecycleApplicationService) InstallDefaultPolicies(ctx context.Context, workspaceID string, principal principalmodel.Principal, now time.Time) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("lifecycle repository unavailable")
	}
	if err := lifecycleAuthorizeWorkspaceOrSystem(principal, workspaceID, PermissionPolicyManage); err != nil {
		return err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	for _, version := range lifecyclepolicy.DefaultPolicyCatalog(workspaceID, principal.UserID, now) {
		if _, found, err := s.repository.LatestPolicy(ctx, workspaceID, version.Policy.Key); err != nil {
			return err
		} else if found {
			continue
		}
		if err := s.repository.SavePolicy(ctx, version); err != nil {
			return err
		}
	}
	return s.audit(ctx, workspaceID, "lifecycle.policy.defaults_installed", principal.UserID, workspaceID, "", map[string]any{"workspace_id": workspaceID})
}
