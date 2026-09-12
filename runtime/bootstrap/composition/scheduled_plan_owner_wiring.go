package composition

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

// resolveCurrentScheduledPlanOwner is the shared authorization seam for every
// user-owned scheduled target. It resolves Identity at dispatch time and never
// treats Scheduler's persisted owner snapshot as current authority.
func resolveCurrentScheduledPlanOwner(ctx context.Context, productKey string, principals identitysdk.PrincipalResolver, owner schedulersdk.ScheduledPlanOwner, productDeniedCode, principalDeniedCode string) (identitysdk.Principal, error) {
	if principals == nil {
		return identitysdk.Principal{}, apperror.New(apperror.KindUnavailable, "backend.dispatch.scheduled_identity_unavailable", nil, nil)
	}
	if owner.Validate() != nil {
		return identitysdk.Principal{}, apperror.New(apperror.KindBadRequest, principalDeniedCode, nil, nil)
	}
	if strings.TrimSpace(owner.ProductKey) != strings.TrimSpace(productKey) {
		return identitysdk.Principal{}, apperror.New(apperror.KindForbidden, productDeniedCode, nil, nil)
	}
	resolution, err := principals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(owner.UserID)})
	if err != nil {
		return identitysdk.Principal{}, err
	}
	principal := resolution.Principal
	if !principal.Known || principal.WorkspaceID != owner.WorkspaceID || principal.UserID != owner.UserID ||
		principal.User.ID != owner.UserID || string(resolution.AccessBundle.Subject.WorkspaceID) != owner.WorkspaceID ||
		string(resolution.AccessBundle.Subject.SubjectID) != owner.UserID {
		return identitysdk.Principal{}, apperror.New(apperror.KindForbidden, principalDeniedCode, nil, nil)
	}
	return principal, nil
}
