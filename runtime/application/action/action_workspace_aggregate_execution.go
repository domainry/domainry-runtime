package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	workspaceaggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
)

const (
	workspaceAggregateMaximumFilters      = 32
	workspaceAggregateMaximumFilterValues = 100
	workspaceAggregateAuditTimeout        = 2 * time.Second
)

type WorkspaceAggregateAudit struct {
	ActionKey      string
	ObjectKey      string
	CapabilityKey  string
	Outcome        string
	ErrorCode      string
	ScopeSHA256    string
	WorkspaceCount int
	SourceRowCount int64
	ResultRowCount int
	Principal      principalmodel.Principal
}

func (e *businessActionExecution) CrossWorkspaceAggregate(ctx context.Context, request runtimeext.CrossWorkspaceAggregateRequest) (runtimeext.CrossWorkspaceAggregateResult, error) {
	capabilityKey := strings.TrimSpace(request.CapabilityKey)
	auditValue := WorkspaceAggregateAudit{ActionKey: strings.TrimSpace(e.action.Key), CapabilityKey: capabilityKey, Principal: e.invocation.Principal}
	fail := func(kind apperror.ErrorKind, code, outcome string, cause error) (runtimeext.CrossWorkspaceAggregateResult, error) {
		auditValue.Outcome, auditValue.ErrorCode = outcome, code
		if auditErr := e.auditWorkspaceAggregate(ctx, auditValue); auditErr != nil {
			return runtimeext.CrossWorkspaceAggregateResult{}, apperror.New(apperror.KindInternal, "backend.action.cross_workspace_aggregate_audit_failed", auditErr, map[string]string{"action": auditValue.ActionKey, "capability": capabilityKey})
		}
		return runtimeext.CrossWorkspaceAggregateResult{}, apperror.New(kind, code, cause, map[string]string{"action": auditValue.ActionKey, "capability": capabilityKey})
	}
	if e == nil || e.unitOfWork == nil || e.Phase() != runtimeext.ExecutionPhasePrewrite {
		return fail(apperror.KindConflict, "backend.action.cross_workspace_aggregate_phase_forbidden", "denied", nil)
	}
	if !ActionAllowed(e.invocation.Principal, e.action) {
		return fail(apperror.KindForbidden, "backend.action.cross_workspace_aggregate_permission_denied", "denied", nil)
	}
	grant, granted := e.crossWorkspaceAggregateGrant(capabilityKey)
	if !granted {
		return fail(apperror.KindForbidden, "backend.action.cross_workspace_aggregate_grant_denied", "denied", nil)
	}
	auditValue.ObjectKey = grant.ObjectKey
	if !actionEffectAllows(e.action.EffectSet, grant.ObjectKey, false) {
		return fail(apperror.KindForbidden, "backend.action.effect_authority_denied", "denied", nil)
	}
	if e.dependencies.ObjectForKey == nil || e.dependencies.NormalizeAggregateQuery == nil || e.dependencies.WorkspaceAggregateCatalog == nil || e.dependencies.WorkspaceAggregateRepository == nil {
		return fail(apperror.KindInternal, "backend.action.cross_workspace_aggregate_unavailable", "error", nil)
	}
	object, ok := e.dependencies.ObjectForKey(grant.ObjectKey)
	if !ok {
		return fail(apperror.KindInternal, "backend.action.cross_workspace_aggregate_object_missing", "error", nil)
	}
	if !definitionmodel.EffectiveObjectCapabilities(object).Read {
		return fail(apperror.KindForbidden, "backend.action.cross_workspace_aggregate_object_read_denied", "denied", nil)
	}
	authorizationPrincipal := actionReadEffectAuthorizationPrincipal(e.invocation.Principal, e.action.EffectSet, e.action, grant.ObjectKey)
	if err := validateWorkspaceAggregateFields(grant, object, authorizationPrincipal); err != nil {
		return fail(apperror.KindForbidden, "backend.action.cross_workspace_aggregate_field_denied", "denied", err)
	}
	filterExpression, err := workspaceAggregateFilterExpression(request.Filters, grant.Filters)
	if err != nil {
		return fail(apperror.KindBadRequest, "backend.action.cross_workspace_aggregate_filter_invalid", "denied", err)
	}
	resultLimit := request.Limit
	if resultLimit == 0 {
		resultLimit = grant.MaxResultRows
	}
	if resultLimit < 1 || resultLimit > grant.MaxResultRows {
		return fail(apperror.KindBadRequest, "backend.action.cross_workspace_aggregate_result_limit_invalid", "denied", nil)
	}
	query := e.dependencies.NormalizeAggregateQuery(object, recordmodel.RecordListQuery{FilterExpression: filterExpression}, authorizationPrincipal)
	if query.AuthorizationDiagnostic != nil || query.AuthorizationMode == recordmodel.RecordQueryAuthorizationDeny {
		return fail(apperror.KindForbidden, "backend.action.cross_workspace_aggregate_data_scope_denied", "denied", nil)
	}
	timeout := time.Duration(grant.TimeoutMilliseconds) * time.Millisecond
	queryContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	workspaces, err := e.dependencies.WorkspaceAggregateCatalog.ListActive(queryContext, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "execute Action cross-Workspace aggregate"), grant.MaxWorkspaces)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(queryContext.Err(), context.DeadlineExceeded) {
			return fail(apperror.KindUnavailable, "backend.action.cross_workspace_aggregate_timeout", "timeout", err)
		}
		return fail(apperror.KindInternal, "backend.action.cross_workspace_aggregate_catalog_failed", "error", err)
	}
	if len(workspaces) > grant.MaxWorkspaces {
		auditValue.WorkspaceCount = len(workspaces)
		return fail(apperror.KindConflict, "backend.action.cross_workspace_aggregate_workspace_limit_exceeded", "limit_exceeded", &workspaceaggregatecontract.LimitExceededError{Kind: workspaceaggregatecontract.LimitWorkspaces})
	}
	auditValue.WorkspaceCount, auditValue.ScopeSHA256 = len(workspaces), workspaceAggregateScopeHash(workspaces)
	persistenceResult, err := e.dependencies.WorkspaceAggregateRepository.Aggregate(queryContext, workspaceaggregatecontract.Query{
		Workspaces: workspaces, Object: object, Dimensions: grant.Dimensions, Measures: grant.Measures, RecordQuery: query,
		MaxSourceRows: grant.MaxSourceRows, MaxResultRows: resultLimit,
	})
	if err != nil {
		var limit *workspaceaggregatecontract.LimitExceededError
		if errors.As(err, &limit) {
			auditValue.SourceRowCount = limit.SourceRowCount
			if limit.Kind == workspaceaggregatecontract.LimitResultRows {
				auditValue.ResultRowCount = int(limit.Observed)
			}
			code := "backend.action.cross_workspace_aggregate_limit_exceeded"
			if limit.Kind == workspaceaggregatecontract.LimitSourceRows {
				code = "backend.action.cross_workspace_aggregate_source_limit_exceeded"
			}
			if limit.Kind == workspaceaggregatecontract.LimitResultRows {
				code = "backend.action.cross_workspace_aggregate_result_limit_exceeded"
			}
			return fail(apperror.KindConflict, code, "limit_exceeded", err)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(queryContext.Err(), context.DeadlineExceeded) {
			return fail(apperror.KindUnavailable, "backend.action.cross_workspace_aggregate_timeout", "timeout", err)
		}
		return fail(apperror.KindInternal, "backend.action.cross_workspace_aggregate_failed", "error", err)
	}
	auditValue.SourceRowCount, auditValue.ResultRowCount, auditValue.Outcome = persistenceResult.SourceRowCount, len(persistenceResult.Rows), "success"
	if err := e.auditWorkspaceAggregate(ctx, auditValue); err != nil {
		return runtimeext.CrossWorkspaceAggregateResult{}, apperror.New(apperror.KindInternal, "backend.action.cross_workspace_aggregate_audit_failed", err, map[string]string{"action": auditValue.ActionKey, "capability": capabilityKey})
	}
	result := runtimeext.CrossWorkspaceAggregateResult{WorkspaceCount: len(workspaces), SourceRowCount: persistenceResult.SourceRowCount, Rows: make([]runtimeext.CrossWorkspaceAggregateRow, len(persistenceResult.Rows))}
	for index, values := range persistenceResult.Rows {
		cloned := make(map[string]string, len(values))
		for key, value := range values {
			cloned[key] = value
		}
		result.Rows[index] = runtimeext.CrossWorkspaceAggregateRow{Values: cloned}
	}
	return result, nil
}

func (e *businessActionExecution) crossWorkspaceAggregateGrant(key string) (runtimeext.CrossWorkspaceAggregateCapability, bool) {
	for _, capability := range e.aggregateGrants {
		if strings.TrimSpace(capability.Key) == key {
			return capability, true
		}
	}
	return runtimeext.CrossWorkspaceAggregateCapability{}, false
}

func (e *businessActionExecution) auditWorkspaceAggregate(ctx context.Context, value WorkspaceAggregateAudit) error {
	if e == nil || e.dependencies.AuditWorkspaceAggregate == nil {
		return errors.New("Workspace aggregate audit is unavailable")
	}
	auditContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceAggregateAuditTimeout)
	defer cancel()
	return e.dependencies.AuditWorkspaceAggregate(auditContext, value)
}

func workspaceAggregateScopeHash(workspaces []workspaceaggregatecontract.Workspace) string {
	ids := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		ids = append(ids, strings.TrimSpace(workspace.ID))
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	return hex.EncodeToString(sum[:])
}

func workspaceAggregateFilterExpression(filters []runtimeext.CrossWorkspaceAggregateFilter, grants []runtimeext.CrossWorkspaceAggregateFilterCapability) (*recordmodel.RecordFilterExpression, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	if len(filters) > workspaceAggregateMaximumFilters {
		return nil, errors.New("too many aggregate filters")
	}
	allowed := map[string]map[string]bool{}
	for _, grant := range grants {
		operators := map[string]bool{}
		for _, operator := range grant.Operators {
			operators[strings.TrimSpace(operator)] = true
		}
		allowed[strings.TrimSpace(grant.Field)] = operators
	}
	children := make([]recordmodel.RecordFilterExpression, 0, len(filters))
	for _, filter := range filters {
		field, operator := strings.TrimSpace(filter.Field), strings.TrimSpace(filter.Operator)
		if field == "" || field == "workspace_id" || !allowed[field][operator] {
			return nil, errors.New("aggregate filter is not granted")
		}
		expression := recordmodel.RecordFilterExpression{Field: field, Operator: operator}
		switch operator {
		case "eq", "ne", "gt", "gte", "lt", "lte":
			if filter.Value == nil || len(filter.Values) != 0 {
				return nil, errors.New("aggregate scalar filter value is invalid")
			}
			expression.Value = filter.Value
		case "in", "not_in":
			if filter.Value != nil || len(filter.Values) == 0 || len(filter.Values) > workspaceAggregateMaximumFilterValues {
				return nil, errors.New("aggregate set filter values are invalid")
			}
			expression.Values = append([]any(nil), filter.Values...)
		case "is_null", "is_not_null":
			if filter.Value != nil || len(filter.Values) != 0 {
				return nil, errors.New("aggregate null filter is invalid")
			}
		default:
			return nil, errors.New("aggregate filter operator is invalid")
		}
		children = append(children, expression)
	}
	if len(children) == 1 {
		return &children[0], nil
	}
	return &recordmodel.RecordFilterExpression{Operator: "and", Children: children}, nil
}

func validateWorkspaceAggregateFields(grant runtimeext.CrossWorkspaceAggregateCapability, object definitionmodel.ObjectSchema, principal principalmodel.Principal) error {
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	check := func(key string) (definitionmodel.FieldSchema, error) {
		field, ok := fields[key]
		if !ok || !recordpolicy.RecordCanReadObjectFieldForPrincipal(principal, object, field) || recordpolicy.RecordFieldReadMaskedForPrincipal(principal, object.Key, field.Key) {
			return definitionmodel.FieldSchema{}, errors.New("aggregate field is not readable")
		}
		return field, nil
	}
	for _, dimension := range grant.Dimensions {
		if dimension.Field == runtimeext.CrossWorkspaceDimensionWorkspace {
			continue
		}
		field, err := check(dimension.Field)
		if err != nil {
			return err
		}
		if dimension.Transform != nil {
			if !dimension.Transform.Valid() || field.Type != "date" && field.Type != "datetime" {
				return errors.New("aggregate date bucket requires a date or datetime field")
			}
			continue
		}
		switch field.Type {
		case "boolean", "currency", "date", "datetime", "integer", "number", "percent", "select", "text":
		default:
			return errors.New("aggregate dimension type is unsupported")
		}
	}
	for _, measure := range grant.Measures {
		if measure.Operation == runtimeext.AggregateCount {
			continue
		}
		field, err := check(measure.Field)
		if err != nil {
			return err
		}
		switch measure.Operation {
		case runtimeext.AggregateSum, runtimeext.AggregateAvg:
			switch field.Type {
			case "currency", "integer", "number", "percent":
			default:
				return errors.New("aggregate numeric operation requires a numeric field")
			}
		case runtimeext.AggregateMin, runtimeext.AggregateMax:
			switch field.Type {
			case "currency", "date", "datetime", "integer", "number", "percent", "select", "text":
			default:
				return errors.New("aggregate ordered operation requires an ordered field")
			}
		default:
			return errors.New("aggregate operation is invalid")
		}
	}
	for _, filter := range grant.Filters {
		if _, err := check(filter.Field); err != nil {
			return err
		}
	}
	return nil
}
