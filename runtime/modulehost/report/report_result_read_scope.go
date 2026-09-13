package reportmodulehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	model "github.com/domainry/domainry-report-sdk/model"
	"github.com/domainry/domainry-report-sdk/modulehost"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// ReadReportResultScope resolves precisely the same sources, fields and row
// predicates as source-version reading, without executing ObjectSQL. Hash the
// effective projection, not a global grant revision: losing query.execute must
// not invalidate identical source data that this reader is still allowed to see.
func (h *ReportModuleQueryHost) ReadReportResultScope(ctx context.Context, report model.ReportSchema, subject model.ReportSubject) (string, error) {
	if h == nil || h.dependencies.Access == nil {
		return "", stableReportHostError(nil)
	}
	principal := RuntimePrincipalFromReportSubject(subject)
	request, err := h.reportSourceVersionRequest(ctx, report, principal)
	if err != nil {
		return "", stableReportHostError(err)
	}
	projections := make(map[string]any, len(request.Queries))
	for alias, query := range request.Queries {
		// These fields are intentionally excluded from RecordListQuery JSON;
		// explicitly include the authorization facts in this private digest.
		projections[alias] = struct {
			Query                                           recordmodel.RecordListQuery
			Mode                                            recordmodel.RecordQueryAuthorizationMode
			Scope                                           any
			Root, OwnerOrganization, Locale, FallbackLocale string
		}{query, query.AuthorizationMode, resultScopeExpression(query.ScopeExpression), query.RootObjectKey, query.OwnerOrganizationScopeID, query.Locale, query.FallbackLocale}
	}
	raw, err := json.Marshal([]any{request.WorkspaceID, request.Objects, projections, principal.UserID, principal.OrgID, canonicalReportAccessSlice(principal.OrgScopeIDs), principal.SupportOrgID, canonicalReportAccessSlice(principal.SupportOrgScopeIDs), canonicalReportAccessSlice(principal.ReportingScopeUserIDs), canonicalReportAccessSlice(principal.BusinessProfiles), principal.ActiveBusinessProfile, principal.BusinessClaims, principal.SystemScope.Kind})
	if err != nil {
		return "", stableReportHostError(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func resultScopeExpression(scope *recordmodel.RecordScopeExpression) any {
	if scope == nil {
		return nil
	}
	children := make([]any, 0, len(scope.Children))
	for i := range scope.Children {
		children = append(children, resultScopeExpression(&scope.Children[i]))
	}
	return []any{scope.Operator, scope.FieldKey, scope.Path, canonicalReportAccessSlice(scope.Values), scope.RelationExists, children}
}

var _ modulehost.ResultReadScopeReader = (*ReportModuleQueryHost)(nil)
