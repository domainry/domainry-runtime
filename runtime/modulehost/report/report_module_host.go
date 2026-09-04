package reportmodulehost

import (
	"context"
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportquery "github.com/domainry/domainry-report-sdk/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

type ReportModuleQueryHostDependencies struct {
	Access          reportcontract.ReportRecordAccess
	ObjectSQL       reportcontract.ReportObjectSQLExecutor
	SnapshotSources reportcontract.ReportSnapshotSourceVersionReader
	ResolveSubject  func(context.Context, reportmodel.ReportAuthority) (principalmodel.Principal, error)
	Audit           func(context.Context, reportmodel.ReportSchema, reportmodel.ReportSummary, principalmodel.Principal) error
}

// ReportModuleQueryHost is the Runtime anti-corruption adapter for Report's
// embedded application ports. It owns Runtime Principal/ObjectSchema/query
// policy translation; Report never imports those Runtime types.
type ReportModuleQueryHost struct {
	dependencies ReportModuleQueryHostDependencies
}

func NewReportModuleQueryHost(dependencies ReportModuleQueryHostDependencies) *ReportModuleQueryHost {
	return &ReportModuleQueryHost{dependencies: dependencies}
}

func (h *ReportModuleQueryHost) ResolveReportSubject(ctx context.Context, authority reportmodel.ReportAuthority) (reportmodel.ReportSubject, error) {
	if h == nil || h.dependencies.ResolveSubject == nil {
		return reportmodel.ReportSubject{}, stableReportHostError(nil)
	}
	principal, err := h.dependencies.ResolveSubject(ctx, authority)
	if err != nil {
		return reportmodel.ReportSubject{}, stableReportHostError(err)
	}
	accessHash, err := ReportAccessScopeHash(principal)
	if err != nil {
		return reportmodel.ReportSubject{}, stableReportHostError(err)
	}
	return reportSubjectFromPrincipal(principal, accessHash), nil
}

func (h *ReportModuleQueryHost) ResolveReportObjectSQLSources(ctx context.Context, report reportmodel.ReportSchema, subject reportmodel.ReportSubject) (map[string]reportmodel.ReportSourceObject, error) {
	if h == nil || h.dependencies.Access == nil || report.ObjectSQLV1 == nil {
		return nil, stableReportHostError(nil)
	}
	principal := RuntimePrincipalFromReportSubject(subject)
	objects := make(map[string]definitionmodel.ObjectSchema, len(report.ObjectSQLV1.SourceObjects))
	for _, rawKey := range report.ObjectSQLV1.SourceObjects {
		key := strings.TrimSpace(rawKey)
		object, err := h.dependencies.Access.ReportObjectForAction(ctx, principal, key, "read")
		if err != nil {
			return nil, stableReportHostError(err)
		}
		objects[key] = object
	}
	projected, err := reportcontract.ReportEngineObjects(objects)
	if err != nil {
		return nil, stableReportHostError(&apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_invalid", Err: err})
	}
	result := make(map[string]reportmodel.ReportSourceObject, len(projected))
	for key, object := range projected {
		result[key] = portableReportObject(object)
	}
	return result, nil
}

func (h *ReportModuleQueryHost) AuthorizeReportObjectSQLPlan(ctx context.Context, _ reportmodel.ReportSchema, plan reportmodel.ReportObjectSQLPlan, subject reportmodel.ReportSubject) error {
	authorizer, ok := h.dependencies.Access.(reportcontract.ReportObjectSQLFieldAuthorizer)
	if h == nil || !ok {
		return stableReportHostError(&apperror.AppError{Kind: apperror.KindInternal, Code: "backend.report.object_sql_field_authorization_unavailable"})
	}
	principal := RuntimePrincipalFromReportSubject(subject)
	for _, source := range plan.Sources {
		object, err := h.dependencies.Access.ReportObjectForAction(ctx, principal, source.ObjectKey, "read")
		if err != nil {
			return stableReportHostError(err)
		}
		for _, field := range source.Fields {
			if err := authorizer.AuthorizeReportObjectSQLField(ctx, principal, object, field); err != nil {
				return stableReportHostError(err)
			}
		}
	}
	return nil
}

func (h *ReportModuleQueryHost) AuthorizeReportExportSource(_ context.Context, objectKey string, subject reportmodel.ReportSubject) error {
	principal := RuntimePrincipalFromReportSubject(subject)
	if !recordpolicy.RecordAllowsObjectAction(principal, objectKey, "export") {
		return stableReportHostError(&apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"})
	}
	return nil
}

func (h *ReportModuleQueryHost) AuthorizeReportExportField(ctx context.Context, objectKey, fieldKey string, subject reportmodel.ReportSubject) (bool, error) {
	if h == nil || h.dependencies.Access == nil {
		return false, stableReportHostError(nil)
	}
	authorizer, ok := h.dependencies.Access.(reportcontract.ReportExportFieldAuthorizer)
	if !ok {
		return false, stableReportHostError(&apperror.AppError{Kind: apperror.KindInternal, Code: "backend.report.export_authorizer_unavailable"})
	}
	masked, err := authorizer.AuthorizeReportExportField(ctx, RuntimePrincipalFromReportSubject(subject), objectKey, fieldKey)
	if err != nil {
		return false, stableReportHostError(err)
	}
	return masked, nil
}

func (h *ReportModuleQueryHost) ExecuteReportObjectSQL(ctx context.Context, request reportmodel.ReportObjectSQLExecutionRequest) (reportmodel.ReportObjectSQLExecutionResult, error) {
	if h == nil || h.dependencies.Access == nil || h.dependencies.ObjectSQL == nil {
		return reportmodel.ReportObjectSQLExecutionResult{}, stableReportHostError(nil)
	}
	principal := RuntimePrincipalFromReportSubject(request.Subject)
	objects := make(map[string]definitionmodel.ObjectSchema, len(request.Plan.Sources))
	queries := make(map[string]recordmodel.RecordListQuery, len(request.Plan.Sources))
	for _, source := range request.Plan.Sources {
		object, err := h.dependencies.Access.ReportObjectForAction(ctx, principal, source.ObjectKey, "read")
		if err != nil {
			return reportmodel.ReportObjectSQLExecutionResult{}, stableReportHostError(err)
		}
		query := recordmodel.RecordListQuery{Page: 1, PageSize: reportQueryPageSize, SelectFields: append([]string(nil), source.Fields...)}
		query = h.dependencies.Access.NormalizeReportListQuery(ctx, object, query, principal)
		objects[source.Alias], queries[source.Alias] = object, reportIncludeAuthorizationProjection(query)
	}
	result, err := h.dependencies.ObjectSQL.ExecuteReportObjectSQL(ctx, reportcontract.ReportObjectSQLExecutionRequest{
		WorkspaceID: principal.WorkspaceID, CrossWorkspaceAggregate: reportmodel.ReportCrossWorkspaceAggregate(request.Report),
		Plan: request.Plan, Objects: objects, Queries: queries, Parameters: request.Parameters, Timeout: request.Timeout,
		PageCursor: request.PageCursor, PagePosition: request.PagePosition, PageSize: request.PageSize,
	})
	if err != nil {
		return reportmodel.ReportObjectSQLExecutionResult{}, stableReportHostError(&apperror.AppError{Kind: apperror.KindInternal, Code: "backend.report.object_sql_query_failed", Err: err})
	}
	return reportmodel.ReportObjectSQLExecutionResult{Rows: result.Rows, HasMore: result.HasMore, Total: result.Total, TotalKnown: result.TotalKnown, NextCursor: result.NextCursor}, nil
}

func (h *ReportModuleQueryHost) ReadReportSourceVersion(ctx context.Context, report reportmodel.ReportSchema, subject reportmodel.ReportSubject) (reportmodel.ReportSnapshotSourceVersion, error) {
	if h == nil || h.dependencies.SnapshotSources == nil || h.dependencies.Access == nil {
		return reportmodel.ReportSnapshotSourceVersion{}, stableReportHostError(nil)
	}
	principal := RuntimePrincipalFromReportSubject(subject)
	request, err := h.reportSourceVersionRequest(ctx, report, principal)
	if err != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, stableReportHostError(err)
	}
	result, err := h.dependencies.SnapshotSources.ReadReportSnapshotSourceVersion(ctx, request)
	if err != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, stableReportHostError(&apperror.AppError{Kind: apperror.KindInternal, Code: "backend.report.export_source_version_failed", Err: err})
	}
	return result, nil
}

func (h *ReportModuleQueryHost) reportSourceVersionRequest(ctx context.Context, report reportmodel.ReportSchema, principal principalmodel.Principal) (reportcontract.ReportSnapshotSourceVersionRequest, error) {
	request := reportcontract.ReportSnapshotSourceVersionRequest{WorkspaceID: principal.WorkspaceID}
	if report.ObjectSQLV1 == nil {
		return request, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_required"}
	}
	objectsByKey := make(map[string]definitionmodel.ObjectSchema, len(report.ObjectSQLV1.SourceObjects))
	for _, rawKey := range report.ObjectSQLV1.SourceObjects {
		key := strings.TrimSpace(rawKey)
		object, err := h.dependencies.Access.ReportObjectForAction(ctx, principal, key, "read")
		if err != nil {
			return request, err
		}
		objectsByKey[key] = object
	}
	plan, err := reportcontract.CompileReportObjectSQL(*report.ObjectSQLV1, objectsByKey)
	if err != nil {
		return request, reportObjectSQLHostError(err)
	}
	authorizer, ok := h.dependencies.Access.(reportcontract.ReportObjectSQLFieldAuthorizer)
	if !ok {
		return request, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.report.object_sql_field_authorization_unavailable"}
	}
	request.Objects = make(map[string]definitionmodel.ObjectSchema, len(plan.Sources))
	request.Queries = make(map[string]recordmodel.RecordListQuery, len(plan.Sources))
	for _, source := range plan.Sources {
		object := objectsByKey[source.ObjectKey]
		for _, field := range source.Fields {
			if err := authorizer.AuthorizeReportObjectSQLField(ctx, principal, object, field); err != nil {
				return request, err
			}
		}
		query := recordmodel.RecordListQuery{Page: 1, PageSize: reportQueryPageSize, SelectFields: append([]string(nil), source.Fields...)}
		query = h.dependencies.Access.NormalizeReportListQuery(ctx, object, query, principal)
		request.Objects[source.Alias], request.Queries[source.Alias] = object, reportIncludeAuthorizationProjection(query)
	}
	return request, nil
}

func (h *ReportModuleQueryHost) AppendReportExecution(ctx context.Context, report reportmodel.ReportSchema, summary reportmodel.ReportSummary, subject reportmodel.ReportSubject) error {
	if h == nil || h.dependencies.Audit == nil {
		return stableReportHostError(nil)
	}
	return stableReportHostError(h.dependencies.Audit(ctx, report, summary, RuntimePrincipalFromReportSubject(subject)))
}

func portableReportObject(object reportquery.Object) reportmodel.ReportSourceObject {
	fields := make([]reportmodel.ReportSourceField, 0, len(object.Fields))
	for _, field := range object.Fields {
		fields = append(fields, reportmodel.ReportSourceField{Key: field.Key, Type: field.Type, Precision: field.Precision, Scale: field.Scale})
	}
	return reportmodel.ReportSourceObject{Key: object.Key, Fields: fields}
}

func reportObjectSQLHostError(err error) error {
	var planErr *reportmodel.ReportObjectSQLPlanError
	if errors.As(err, &planErr) {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: planErr.Code, Params: planErr.Params, Err: planErr}
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_invalid", Err: err}
}

func reportSubjectFromPrincipal(principal principalmodel.Principal, accessHash string) reportmodel.ReportSubject {
	subject := reportmodel.ReportSubject{
		Principal: principal.Principal, RequestID: principal.RequestID, CorrelationID: principal.CorrelationID,
		CausationID: principal.CausationID, AccessScopeHash: accessHash,
		TrustedProcess: principal.SystemScope.Valid(), ProcessCapabilities: append([]string(nil), principal.SystemCapabilities...),
	}
	for _, profile := range principal.BusinessProfiles {
		subject.BusinessProfiles = append(subject.BusinessProfiles, portableBusinessProfile(profile))
	}
	if principal.ActiveBusinessProfile != nil {
		profile := portableBusinessProfile(*principal.ActiveBusinessProfile)
		subject.ActiveBusinessProfile = &profile
	}
	subject.BusinessClaims = portableBusinessClaims(principal.BusinessClaims)
	return subject
}

func RuntimePrincipalFromReportSubject(subject reportmodel.ReportSubject) principalmodel.Principal {
	principal := principalmodel.Principal{Principal: subject.Principal, RequestID: subject.RequestID, CorrelationID: subject.CorrelationID, CausationID: subject.CausationID}
	if subject.TrustedProcess {
		principal.SystemScope = principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "embedded report execution")
		principal.SystemCapabilities = append([]string(nil), subject.ProcessCapabilities...)
	}
	for _, profile := range subject.BusinessProfiles {
		principal.BusinessProfiles = append(principal.BusinessProfiles, runtimeBusinessProfile(profile))
	}
	if subject.ActiveBusinessProfile != nil {
		profile := runtimeBusinessProfile(*subject.ActiveBusinessProfile)
		principal.ActiveBusinessProfile = &profile
	}
	principal.BusinessClaims = runtimeBusinessClaims(subject.BusinessClaims)
	return principal
}

// ReportAuthorityFromRuntimePrincipal is used only by trusted Runtime
// orchestration such as Scheduler. Public HTTP requests must carry the
// original access token and be resolved by ResolveReportSubject.
func ReportAuthorityFromRuntimePrincipal(principal principalmodel.Principal) (reportmodel.ReportAuthority, error) {
	accessHash, err := ReportAccessScopeHash(principal)
	if err != nil {
		return reportmodel.ReportAuthority{}, err
	}
	subject := reportSubjectFromPrincipal(principal, accessHash)
	return reportmodel.ReportAuthority{RequestID: principal.RequestID, Subject: &subject}, nil
}

func portableBusinessProfile(profile profilebindingmodel.Reference) reportmodel.ReportBusinessProfile {
	return reportmodel.ReportBusinessProfile{BindingKey: profile.BindingKey, ObjectKey: profile.ObjectKey, ProfileID: profile.RecordID, Claims: portableBusinessClaims(profile.Claims)}
}

func runtimeBusinessProfile(profile reportmodel.ReportBusinessProfile) profilebindingmodel.Reference {
	return profilebindingmodel.Reference{BindingKey: profile.BindingKey, ObjectKey: profile.ObjectKey, RecordID: profile.ProfileID, Claims: runtimeBusinessClaims(profile.Claims)}
}

func portableBusinessClaims(values map[string]profilebindingmodel.ClaimValue) map[string]reportmodel.ReportBusinessClaim {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]reportmodel.ReportBusinessClaim, len(values))
	for key, value := range values {
		result[key] = reportmodel.ReportBusinessClaim{Type: value.Type, Value: value.Value}
	}
	return result
}

func runtimeBusinessClaims(values map[string]reportmodel.ReportBusinessClaim) map[string]profilebindingmodel.ClaimValue {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]profilebindingmodel.ClaimValue, len(values))
	for key, value := range values {
		result[key] = profilebindingmodel.ClaimValue{Type: value.Type, Value: value.Value}
	}
	return result
}

func stableReportHostError(err error) error {
	if err == nil {
		return &reportsdk.Error{StatusCode: 500, Code: "backend.internal_error"}
	}
	var stable *reportsdk.Error
	if errors.As(err, &stable) {
		return stable
	}
	status := 500
	switch apperror.KindOf(err) {
	case apperror.KindBadRequest:
		status = 400
	case apperror.KindForbidden:
		status = 403
	case apperror.KindNotFound:
		status = 404
	case apperror.KindConflict:
		status = 409
	case apperror.KindRateLimited:
		status = 429
	case apperror.KindUnavailable:
		status = 503
	}
	return &reportsdk.Error{StatusCode: status, Code: apperror.CodeOf(err), Params: apperror.ParamsOf(err), Cause: err}
}

// StableReportHostError is shared by Runtime's Report host adapters so every
// transport-independent port preserves the same SDK error contract.
func StableReportHostError(err error) error { return stableReportHostError(err) }

var _ reportmodulehost.SubjectResolver = (*ReportModuleQueryHost)(nil)
var _ reportmodulehost.ObjectSQLExecutor = (*ReportModuleQueryHost)(nil)
var _ reportmodulehost.SourceVersionReader = (*ReportModuleQueryHost)(nil)
var _ reportmodulehost.ExecutionAudit = (*ReportModuleQueryHost)(nil)
var _ reportmodulehost.ExportAuthorization = (*ReportModuleQueryHost)(nil)
