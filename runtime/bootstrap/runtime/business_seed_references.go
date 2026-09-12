package runtime

import (
	"context"
	"sort"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	businessseed "github.com/domainry/domainry-runtime/runtime/application/seed/business"
	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"
)

// BusinessSeedReferenceCandidate is a non-secret record identity supplied by
// the trusted project host after tenant initialization. Runtime still verifies
// the candidate through the workspace-scoped owner projection before use.
type BusinessSeedReferenceCandidate struct {
	WorkspaceID     string
	TargetObjectKey string
	RecordID        string
	SourceKind      string
}

// ProjectStartupOptions carries trusted, process-local bootstrap inputs that
// do not belong in public or persisted Runtime configuration.
type ProjectStartupOptions struct {
	BusinessSeedReferenceCandidates []BusinessSeedReferenceCandidate
	ProjectNavigationCatalog        identitysdk.ProjectNavigationCatalog
	// AnalysisTableSource is a trusted public data-owner port assembled by the
	// project host. Runtime and Report never import the source implementation.
	AnalysisTableSource reportmodulehost.AnalysisTableSource
}

type identityBusinessSeedReferenceResolver struct {
	projection  identitysdk.Projection
	application identitysdk.ApplicationScope
	candidates  []BusinessSeedReferenceCandidate
}

func newIdentityBusinessSeedReferenceResolver(cfgWorkspaceID, applicationKey string, projection identitysdk.Projection, candidates []BusinessSeedReferenceCandidate) businessseed.BaselineReferenceResolver {
	cloned := append([]BusinessSeedReferenceCandidate(nil), candidates...)
	return &identityBusinessSeedReferenceResolver{
		projection: projection,
		application: identitysdk.ApplicationScope{
			TenantID:       identitysdk.TenantID(strings.TrimSpace(cfgWorkspaceID)),
			WorkspaceID:    identitysdk.WorkspaceID(strings.TrimSpace(cfgWorkspaceID)),
			ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(applicationKey)),
		},
		candidates: cloned,
	}
}

func (resolver *identityBusinessSeedReferenceResolver) ResolveBaselineReference(ctx context.Context, request businessseed.BaselineReferenceRequest) (string, error) {
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	targetObjectKey := strings.TrimSpace(request.TargetObjectKey)
	matchingTarget := 0
	workspaceScopedTarget := 0
	scopedByID := map[string]BusinessSeedReferenceCandidate{}
	preferredRecordID := strings.TrimSpace(request.PreferredRecordID)
	for _, candidate := range resolver.candidates {
		if strings.TrimSpace(candidate.TargetObjectKey) != targetObjectKey {
			continue
		}
		matchingTarget++
		if strings.TrimSpace(candidate.WorkspaceID) != workspaceID {
			continue
		}
		recordID := strings.TrimSpace(candidate.RecordID)
		if recordID == "" || strings.TrimSpace(candidate.SourceKind) == "" {
			continue
		}
		workspaceScopedTarget++
		if preferredRecordID != "" && recordID != preferredRecordID {
			continue
		}
		if _, found := scopedByID[recordID]; !found {
			scopedByID[recordID] = candidate
		}
	}
	scoped := make([]BusinessSeedReferenceCandidate, 0, len(scopedByID))
	for _, candidate := range scopedByID {
		scoped = append(scoped, candidate)
	}
	sort.Slice(scoped, func(left, right int) bool {
		return strings.TrimSpace(scoped[left].RecordID) < strings.TrimSpace(scoped[right].RecordID)
	})
	fail := func(reason string) error {
		return &businessseed.BaselineReferenceResolutionError{
			WorkspaceID: workspaceID, ObjectKey: request.ObjectKey, FieldKey: request.FieldKey,
			TargetObjectKey: targetObjectKey, Reason: reason, CandidateCount: matchingTarget,
			ScopedCount: workspaceScopedTarget,
		}
	}
	if workspaceID == "" || workspaceID != string(resolver.application.WorkspaceID) || !resolver.application.WorkspaceID.Valid() || !resolver.application.ApplicationKey.Valid() {
		return "", fail("application_scope_mismatch")
	}
	if resolver.projection == nil {
		return "", fail("identity_projection_unavailable")
	}
	if len(scoped) == 0 {
		if preferredRecordID != "" && workspaceScopedTarget > 0 {
			return "", fail("preferred_candidate_unavailable")
		}
		return "", fail("no_scoped_candidate")
	}
	for _, candidate := range scoped {
		recordID := strings.TrimSpace(candidate.RecordID)
		var found bool
		var resolvedID string
		var err error
		switch targetObjectKey {
		case definitioncontract.IdentityUserObjectKey:
			var user identitysdk.User
			user, found, err = resolver.projection.FindUser(ctx, identitysdk.UserLookup{
				Application: resolver.application, UserID: identitysdk.SubjectID(recordID),
			})
			resolvedID = strings.TrimSpace(user.ID)
		case definitioncontract.IdentityOrganizationUnitObjectKey:
			var organization identitysdk.OrganizationUnit
			organization, found, err = resolver.projection.FindOrganizationUnit(ctx, identitysdk.OrganizationUnitLookup{
				Application: resolver.application, OrgID: recordID,
			})
			resolvedID = strings.TrimSpace(organization.ID)
		default:
			return "", fail("unsupported_external_target")
		}
		if err != nil {
			return "", fail("candidate_verification_failed")
		}
		if found && resolvedID == recordID {
			return recordID, nil
		}
	}
	return "", fail("no_verified_candidate")
}
