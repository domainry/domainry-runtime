package service

import (
	"context"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	collectionplatform "github.com/domainry/domainry-foundation/collection"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

type RecordQueryPolicyDependencies struct {
	Objects               func() []definitionmodel.ObjectSchema
	Reports               func() []reportmodel.ReportSchema
	CandidateScopeMatches func(context.Context, string, recordmodel.Record, recordmodel.RecordScopeExpression) (bool, error)
}

// RecordQueryPolicyDomainService applies record query policy.
type RecordQueryPolicyDomainService struct {
	dependencies RecordQueryPolicyDependencies
}

func NewRecordQueryPolicyDomainService(dependencies RecordQueryPolicyDependencies) *RecordQueryPolicyDomainService {
	return &RecordQueryPolicyDomainService{dependencies: dependencies}
}

func (s *RecordQueryPolicyDomainService) ObjectForAction(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	objectKey = strings.TrimSpace(objectKey)
	object, ok := queryPolicyObjectMap(s.objects())[objectKey]
	if !ok {
		return definitionmodel.ObjectSchema{}, queryPolicyError(apperror.KindNotFound, "backend.object.not_found")
	}
	if !principal.Known {
		return definitionmodel.ObjectSchema{}, queryPolicyError(apperror.KindForbidden, "backend.role.unknown")
	}
	if allowed, handled := recordpolicy.RecordSDKAllowsObjectAction(principal, objectKey, action); handled {
		if !allowed {
			return definitionmodel.ObjectSchema{}, queryPolicyError(apperror.KindForbidden, "backend.permission.denied")
		}
		return object, nil
	}
	if !principal.SystemScope.Valid() || !principal.Allows(objectKey, action) {
		return definitionmodel.ObjectSchema{}, queryPolicyError(apperror.KindForbidden, "backend.permission.denied")
	}
	return object, nil
}

func (s *RecordQueryPolicyDomainService) EnsureReportSnapshotAccess(object definitionmodel.ObjectSchema, action string, principal principalmodel.Principal) error {
	if !s.isReportSnapshotObject(object) {
		return nil
	}
	if !principal.Known {
		return queryPolicyError(apperror.KindForbidden, "backend.role.unknown")
	}
	if allowed, handled := recordpolicy.RecordSDKAllowsObjectAction(principal, object.Key, action); handled {
		if !allowed {
			return queryPolicyError(apperror.KindForbidden, "backend.report.permission_denied")
		}
		return nil
	}
	if !principal.SystemScope.Valid() || !principal.Allows(object.Key, action) {
		return queryPolicyError(apperror.KindForbidden, "backend.report.permission_denied")
	}
	return nil
}

func (s *RecordQueryPolicyDomainService) NormalizeListQuery(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	query = recordvalidation.RecordNormalizeListQuery(object, query, principal)
	if expression, err, handled := RecordCompileSDKDataScopeExpression(object, s.objects(), principal, "read"); handled {
		query.Scope = "custom"
		query.RootObjectKey = object.Key
		query.ScopeExpression = expression
		if err != nil {
			query.ScopeDiagnostic = &recordmodel.RecordScopeDiagnostic{Code: "backend.policy.expression_invalid", ObjectKey: object.Key, Scope: query.Scope, Detail: err.Error()}
		}
		return query
	}
	if principal.SystemScope.Valid() && principal.Allows(object.Key, "read") {
		query.Scope = "all_records"
		query.ScopeExpression = nil
		return query
	}
	query.Scope = "custom"
	query.RootObjectKey = object.Key
	query.ScopeExpression = denyAllRecordScopeExpression()
	return query
}

func (s *RecordQueryPolicyDomainService) CanAccessRecord(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	if principal.AccessBundle != nil {
		return recordpolicy.RecordCanAccess(principal, object, record)
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, "read")
}

func (s *RecordQueryPolicyDomainService) CanWriteRecordScope(principal principalmodel.Principal, object definitionmodel.ObjectSchema, data map[string]any) bool {
	if principal.AccessBundle != nil {
		return recordpolicy.RecordCanWriteScope(principal, object, data)
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, "update")
}

// CanWriteCandidateScope evaluates custom relation predicates against the
// candidate plus persisted related records. Create authorization cannot query
// the root record because it has not been inserted yet.
func (s *RecordQueryPolicyDomainService) CanWriteCandidateScope(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, candidate recordmodel.Record) (bool, error) {
	return s.CanAccessPersistedRecordScope(ctx, principal, object, candidate, true)
}

// CanAccessRecordAction preserves the exact SDK action through record-scope
// evaluation. Explicit Runtime system principals are authorized only by their
// process-owned capabilities.
func (s *RecordQueryPolicyDomainService) CanAccessRecordAction(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, action string) (bool, error) {
	if principal.AccessBundle != nil {
		return s.canAccessSDKRecordScope(ctx, principal, object, record, normalizeSDKScopeAction(action))
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, normalizeSDKScopeAction(action)), nil
}

// CanAccessPersistedRecordScope evaluates direct and relation-path data scope
// against the current Action transaction. The in-memory policy is insufficient
// for a relation path because the root record contains only the related ID.
func (s *RecordQueryPolicyDomainService) CanAccessPersistedRecordScope(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, write bool) (bool, error) {
	if principal.AccessBundle != nil {
		action := "read"
		if write {
			action = "update"
		}
		return s.canAccessSDKRecordScope(ctx, principal, object, record, action)
	}
	action := "read"
	if write {
		action = "update"
	}
	return principal.SystemScope.Valid() && principal.Allows(object.Key, action), nil
}

func (s *RecordQueryPolicyDomainService) canAccessSDKRecordScope(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, action string) (bool, error) {
	expression, err, handled := RecordCompileSDKDataScopeExpression(object, s.objects(), principal, action)
	if !handled {
		return false, nil
	}
	if err != nil {
		return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.policy.expression_invalid", Err: err}
	}
	if expression == nil {
		return false, nil
	}
	if !sdkScopeExpressionHasRelation(*expression) {
		return directSDKScopeExpressionMatches(*expression, record), nil
	}
	if s.dependencies.CandidateScopeMatches == nil {
		return false, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.policy.candidate_scope_evaluator_unavailable"}
	}
	return s.dependencies.CandidateScopeMatches(ctx, principal.WorkspaceID, record, *expression)
}

func (s *RecordQueryPolicyDomainService) isReportSnapshotObject(object definitionmodel.ObjectSchema) bool {
	_, ok := RecordReportObjectKeySet(s.reports())[strings.TrimSpace(object.Key)]
	return ok
}

func RecordReportObjectKeySet(reports []reportmodel.ReportSchema) map[string]struct{} {
	out := map[string]struct{}{}
	for _, report := range reports {
		for _, objectKey := range reportmodel.ReportDatasetSnapshotObjectKeys(report.Dataset) {
			if objectKey = strings.TrimSpace(objectKey); objectKey != "" {
				out[objectKey] = struct{}{}
			}
		}
	}
	return out
}

func queryPolicyObjectMap(objects []definitionmodel.ObjectSchema) map[string]definitionmodel.ObjectSchema {
	return collectionplatform.IndexBy(objects, func(object definitionmodel.ObjectSchema) string { return object.Key })
}

func queryPolicyError(kind apperror.ErrorKind, code string) error {
	return &apperror.AppError{Kind: kind, Code: code}
}

func (s *RecordQueryPolicyDomainService) objects() []definitionmodel.ObjectSchema {
	if s.dependencies.Objects == nil {
		return nil
	}
	return s.dependencies.Objects()
}

func (s *RecordQueryPolicyDomainService) reports() []reportmodel.ReportSchema {
	if s.dependencies.Reports == nil {
		return nil
	}
	return s.dependencies.Reports()
}
