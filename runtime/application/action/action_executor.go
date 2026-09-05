package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type ActionExecutionResult struct {
	Record  *actionmodel.ActionResult
	Object  *actionmodel.ActionObjectResult
	Commits []transactionmodel.RecordMutationCommit
}

type BusinessHandlerExecutionDependencies struct {
	RuntimeRevision           string
	ProjectRevision           string
	ApplicationSchemaRevision string
	ResolveMetadataRevision   func(context.Context, principalmodel.Principal) (string, error)
	GetRecord                 func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	GetRecordForUpdate        func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	ListRecords               func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	PlanCreateMutation        func(context.Context, string, map[string]any, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	PlanUpdateMutation        func(context.Context, string, string, map[string]any, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	PlanConditionalUpdate     func(context.Context, string, string, transactionmodel.ConditionalUpdateInput, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	PlanDeleteMutation        func(context.Context, string, string, string, principalmodel.Principal) ([]transactionmodel.MutationPlan, error)
	PlanRestoreMutation       func(context.Context, string, string, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	ValidateDurableIntent     func(context.Context, runtimeext.DurableIntent, principalmodel.Principal) error
	CompileNotification       func(context.Context, string, runtimeext.NotificationIntent, principalmodel.Principal) (notificationmodel.NotificationEvent, error)
	VerifyFileClean           func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
}

type BusinessHandlerExecutor struct {
	dependencies BusinessHandlerExecutionDependencies
}

func NewBusinessHandlerExecutor(dependencies BusinessHandlerExecutionDependencies) *BusinessHandlerExecutor {
	dependencies.RuntimeRevision = strings.TrimSpace(dependencies.RuntimeRevision)
	dependencies.ProjectRevision = strings.TrimSpace(dependencies.ProjectRevision)
	dependencies.ApplicationSchemaRevision = strings.TrimSpace(dependencies.ApplicationSchemaRevision)
	return &BusinessHandlerExecutor{dependencies: dependencies}
}

func (e *BusinessHandlerExecutor) execute(ctx context.Context, governed governedActionExecution) (ActionExecutionResult, error) {
	invocation, action := governed.invocation, governed.entry.Definition
	binding, payload, executionID := governed.entry.HandlerBinding, governed.payload, governed.executionID
	if e == nil || binding.Handler == nil {
		return ActionExecutionResult{}, apperror.New(apperror.KindInternal, "backend.action.business_handler_executor_required", nil, map[string]string{"action": action.Key})
	}
	descriptor := binding.Descriptor
	if strings.TrimSpace(descriptor.ActionKey) != strings.TrimSpace(action.Key) {
		return ActionExecutionResult{}, apperror.New(apperror.KindInternal, "backend.action.handler_contract_mismatch", nil, map[string]string{"action": action.Key})
	}
	rawInput, err := json.Marshal(businessHandlerInput(action, payload))
	if err != nil {
		return ActionExecutionResult{}, apperror.New(apperror.KindBadRequest, "backend.action.payload_invalid", err, nil)
	}
	if governed.unitOfWork == nil {
		return ActionExecutionResult{}, apperror.New(apperror.KindInternal, "backend.action.execution_phase_required", nil, map[string]string{"action": action.Key})
	}
	identity, err := e.executionIdentity(ctx, invocation, action, descriptor, executionID)
	if err != nil {
		return ActionExecutionResult{}, err
	}
	session := &businessActionExecution{
		dependencies:       e.dependencies,
		identity:           identity,
		principal:          toRuntimeextPrincipal(invocation.Principal),
		workspace:          runtimeext.Workspace{ID: invocation.Principal.WorkspaceID},
		invocation:         invocation,
		action:             action,
		unitOfWork:         governed.unitOfWork,
		connectorGrants:    append([]runtimeext.ActionConnectorCapability(nil), descriptor.ConnectorCapabilities...),
		notificationGrants: append([]string(nil), descriptor.NotificationEventTypes...),
		fileGrants:         append([]string(nil), descriptor.FileCapabilities...),
		objectGrants:       append([]runtimeext.ActionObjectCapability(nil), descriptor.ObjectCapabilities...),
	}
	rawOutput, err := binding.Handler.Invoke(ctx, session, rawInput)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ActionExecutionResult{}, err
		}
		var appError *apperror.AppError
		var businessConflict *mutation.PolicyConflictError
		var conflict *mutation.MutationConflictError
		var transient *mutation.TransactionTransientError
		if errors.As(err, &appError) || errors.As(err, &businessConflict) || errors.As(err, &conflict) || errors.As(err, &transient) || mutation.IsTransactionCommitUnknown(err) {
			return ActionExecutionResult{}, err
		}
		var businessError *runtimeext.BusinessError
		if errors.As(err, &businessError) && businessError.Valid() {
			kind := apperror.KindBadRequest
			if businessError.Code == "backend.generated.action_output_invalid" {
				kind = apperror.KindInternal
			}
			return ActionExecutionResult{}, apperror.FromError(kind, businessError)
		}
		return ActionExecutionResult{}, apperror.New(apperror.KindInternal, "backend.action.handler_failed", err, map[string]string{"action": action.Key})
	}
	output, err := decodeBusinessHandlerOutput(rawOutput)
	if err != nil {
		return ActionExecutionResult{}, apperror.New(apperror.KindInternal, "backend.action.handler_output_invalid", err, map[string]string{"action": action.Key})
	}
	commits, err := session.canonicalCommits()
	if err != nil {
		return ActionExecutionResult{}, err
	}
	if invocation.RecordID != "" {
		record, resolveErr := session.resolveInvocationRecord(ctx, action.ObjectKey, invocation.RecordID)
		if resolveErr != nil {
			return ActionExecutionResult{}, resolveErr
		}
		result := actionmodel.ActionResult{
			ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: record.ID,
			Message: "backend.action.executed", Record: record, Output: output,
			CreatedRecords: session.created, UpdatedRecords: session.updated, DeletedRecords: session.deleted, RestoredRecords: session.restored,
		}
		return ActionExecutionResult{Record: &result, Commits: commits}, nil
	}
	result := actionmodel.ActionObjectResult{
		ActionKey: action.Key, ObjectKey: action.ObjectKey, Status: "success", Message: "backend.action.executed", Output: output,
		CreatedRecords: session.created, UpdatedRecords: session.updated, DeletedRecords: session.deleted, RestoredRecords: session.restored,
	}
	return ActionExecutionResult{Object: &result, Commits: commits}, nil
}

type businessActionExecution struct {
	dependencies       BusinessHandlerExecutionDependencies
	identity           runtimeext.ExecutionIdentity
	principal          runtimeext.Principal
	workspace          runtimeext.Workspace
	invocation         actionmodel.ActionInvocation
	action             definitionmodel.ActionSchema
	unitOfWork         *actionUnitOfWork
	connectorGrants    []runtimeext.ActionConnectorCapability
	notificationGrants []string
	fileGrants         []string
	objectGrants       []runtimeext.ActionObjectCapability
	plans              []transactionmodel.MutationPlan
	intents            []runtimeext.DurableIntent
	notifications      []notificationmodel.NotificationEvent
	mutatedRecords     map[string]recordmodel.Record
	observedRecords    map[string]recordmodel.Record
	created            []actionmodel.ActionObjectRecordRef
	updated            []actionmodel.ActionObjectRecordRef
	deleted            []actionmodel.ActionObjectRecordRef
	restored           []actionmodel.ActionObjectRecordRef
}

var _ runtimeext.ActionExecution = (*businessActionExecution)(nil)
var _ runtimeext.RecordNotificationRecipientExecution = (*businessActionExecution)(nil)

func (e *businessActionExecution) Identity() runtimeext.ExecutionIdentity { return e.identity }
func (e *businessActionExecution) Principal() runtimeext.Principal {
	return cloneRuntimeextPrincipal(e.principal)
}
func (e *businessActionExecution) Workspace() runtimeext.Workspace { return e.workspace }
func (e *businessActionExecution) mutatedRecord(objectKey, recordID string) (recordmodel.Record, bool) {
	record, ok := e.mutatedRecords[strings.TrimSpace(objectKey)+"\x00"+strings.TrimSpace(recordID)]
	return record, ok
}

func (e *businessActionExecution) observeRecord(objectKey string, record recordmodel.Record) {
	if e.observedRecords == nil {
		e.observedRecords = map[string]recordmodel.Record{}
	}
	e.observedRecords[strings.TrimSpace(objectKey)+"\x00"+strings.TrimSpace(record.ID)] = record
}

func (e *businessActionExecution) QueryRecords(ctx context.Context, query runtimeext.RecordQuery) (runtimeext.RecordQueryResult, error) {
	if !query.Valid() {
		return runtimeext.RecordQueryResult{}, apperror.New(apperror.KindBadRequest, "backend.action.query_invalid", nil, nil)
	}
	if !actionEffectAllows(e.action.EffectSet, query.ObjectKey, false) {
		return runtimeext.RecordQueryResult{}, apperror.New(apperror.KindForbidden, "backend.action.effect_authority_denied", nil, map[string]string{"object": query.ObjectKey})
	}
	authorizationPrincipal := actionReadEffectAuthorizationPrincipal(e.invocation.Principal, e.action.EffectSet, e.action, query.ObjectKey)
	if query.Operation == runtimeext.QueryGetForUpdate {
		var err error
		ctx, err = e.unitOfWork.beginWriting(ctx)
		if err != nil {
			return runtimeext.RecordQueryResult{}, err
		}
	} else {
		ctx = e.unitOfWork.executionContext(ctx)
	}
	switch query.Operation {
	case runtimeext.QueryGet:
		if e.dependencies.GetRecord == nil {
			return runtimeext.RecordQueryResult{}, missingExecutorPort("get_record")
		}
		record, err := e.dependencies.GetRecord(ctx, query.ObjectKey, query.RecordID, authorizationPrincipal)
		if err != nil {
			return runtimeext.RecordQueryResult{}, err
		}
		e.observeRecord(query.ObjectKey, record)
		return runtimeext.RecordQueryResult{Records: []runtimeext.Record{toRuntimeextRecord(query.ObjectKey, record)}, Exists: true, Count: 1}, nil
	case runtimeext.QueryGetForUpdate:
		if e.dependencies.GetRecordForUpdate == nil {
			return runtimeext.RecordQueryResult{}, missingExecutorPort("get_record_for_update")
		}
		record, err := e.dependencies.GetRecordForUpdate(ctx, query.ObjectKey, query.RecordID, authorizationPrincipal)
		if err != nil {
			return runtimeext.RecordQueryResult{}, err
		}
		e.observeRecord(query.ObjectKey, record)
		return runtimeext.RecordQueryResult{Records: []runtimeext.Record{toRuntimeextRecord(query.ObjectKey, record)}, Exists: true, Count: 1}, nil
	default:
		if e.dependencies.ListRecords == nil {
			return runtimeext.RecordQueryResult{}, missingExecutorPort("list_records")
		}
		filterExpression, err := actionRecordFilterExpression(query.Filters)
		if err != nil {
			return runtimeext.RecordQueryResult{}, err
		}
		pageSize := query.Limit
		if pageSize <= 0 {
			pageSize = 100
		}
		if query.Operation == runtimeext.QueryExists || query.Operation == runtimeext.QueryCount {
			pageSize = 1
		}
		sorts := make([]recordmodel.RecordSortRule, 0, len(query.Sorts))
		for _, sort := range query.Sorts {
			field, direction := strings.TrimSpace(sort.Field), strings.ToLower(strings.TrimSpace(sort.Direction))
			if field == "" || (direction != "asc" && direction != "desc") {
				return runtimeext.RecordQueryResult{}, apperror.New(apperror.KindBadRequest, "backend.action.query_sort_invalid", nil, map[string]string{"field": field, "direction": direction})
			}
			sorts = append(sorts, recordmodel.RecordSortRule{Field: field, Direction: direction})
		}
		projection := make([]string, 0, len(query.Projection))
		for _, raw := range query.Projection {
			field := strings.TrimSpace(raw)
			if field == "" {
				return runtimeext.RecordQueryResult{}, apperror.New(apperror.KindBadRequest, "backend.action.query_projection_invalid", nil, nil)
			}
			projection = append(projection, field)
		}
		page, err := e.dependencies.ListRecords(ctx, query.ObjectKey, recordmodel.RecordListQuery{
			Page: 1, PageSize: pageSize, AfterID: strings.TrimSpace(query.AfterID), FilterExpression: filterExpression,
			Sort: sorts, SelectFields: projection,
		}, authorizationPrincipal)
		if err != nil {
			return runtimeext.RecordQueryResult{}, err
		}
		result := runtimeext.RecordQueryResult{Exists: len(page.Items) > 0, Count: int64(page.Total)}
		if query.Operation == runtimeext.QueryList {
			result.Records = make([]runtimeext.Record, 0, len(page.Items))
			for _, record := range page.Items {
				e.observeRecord(query.ObjectKey, record)
				result.Records = append(result.Records, toRuntimeextRecord(query.ObjectKey, record))
			}
		}
		return result, nil
	}
}

func actionRecordFilterExpression(filters []runtimeext.Filter) (*recordmodel.RecordFilterExpression, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	children := make([]recordmodel.RecordFilterExpression, 0, len(filters))
	for _, filter := range filters {
		expression, err := actionRecordFilterNode(filter)
		if err != nil {
			return nil, err
		}
		children = append(children, expression)
	}
	if len(children) == 1 {
		return &children[0], nil
	}
	return &recordmodel.RecordFilterExpression{Operator: "and", Children: children}, nil
}

func actionRecordFilterNode(filter runtimeext.Filter) (recordmodel.RecordFilterExpression, error) {
	field := strings.TrimSpace(filter.Field)
	operator := strings.ToLower(strings.TrimSpace(filter.Operator))
	if operator == "" {
		operator = "eq"
	}
	expression := recordmodel.RecordFilterExpression{Operator: operator}
	switch operator {
	case "and", "or":
		if field != "" || filter.Value != nil || len(filter.Values) != 0 || len(filter.Children) < 2 {
			return recordmodel.RecordFilterExpression{}, actionQueryFilterInvalid(field, operator)
		}
	case "not":
		if field != "" || filter.Value != nil || len(filter.Values) != 0 || len(filter.Children) != 1 {
			return recordmodel.RecordFilterExpression{}, actionQueryFilterInvalid(field, operator)
		}
	case "eq", "ne", "gt", "gte", "lt", "lte":
		if field == "" || filter.Value == nil || len(filter.Values) != 0 || len(filter.Children) != 0 {
			return recordmodel.RecordFilterExpression{}, actionQueryFilterInvalid(field, operator)
		}
		expression.Field, expression.Value = field, filter.Value
	case "in", "not_in":
		if field == "" || filter.Value != nil || len(filter.Values) == 0 || len(filter.Children) != 0 {
			return recordmodel.RecordFilterExpression{}, actionQueryFilterInvalid(field, operator)
		}
		expression.Field, expression.Values = field, append([]any(nil), filter.Values...)
	case "is_null", "is_not_null":
		if field == "" || filter.Value != nil || len(filter.Values) != 0 || len(filter.Children) != 0 {
			return recordmodel.RecordFilterExpression{}, actionQueryFilterInvalid(field, operator)
		}
		expression.Field = field
	default:
		return recordmodel.RecordFilterExpression{}, apperror.New(apperror.KindBadRequest, "backend.action.query_operator_unsupported", nil, map[string]string{"operator": operator})
	}
	for _, child := range filter.Children {
		converted, err := actionRecordFilterNode(child)
		if err != nil {
			return recordmodel.RecordFilterExpression{}, err
		}
		expression.Children = append(expression.Children, converted)
	}
	return expression, nil
}

func actionQueryFilterInvalid(field, operator string) error {
	return apperror.New(apperror.KindBadRequest, "backend.action.query_filter_invalid", nil, map[string]string{"field": field, "operator": operator})
}

func actionEffectAllows(set *definitionmodel.ActionEffectSet, objectKey string, write bool) bool {
	if set == nil {
		return false
	}
	effects := set.Read
	if write {
		effects = set.Write
	}
	for _, effect := range effects {
		if strings.TrimSpace(effect.ObjectKey) == strings.TrimSpace(objectKey) {
			return true
		}
	}
	return false
}

func actionEffectAuthority(set *definitionmodel.ActionEffectSet) map[string][]string {
	result := map[string][]string{}
	if set == nil {
		return result
	}
	for _, effect := range set.Write {
		fields := append([]string(nil), effect.Fields...)
		if len(fields) == 0 {
			// An operation-level capability without a narrower compiler-owned
			// field list authorizes the operation's policy-validated fields.
			fields = []string{"*"}
		}
		result[strings.TrimSpace(effect.ObjectKey)] = fields
	}
	return result
}

func toRuntimeextPrincipal(principal principalmodel.Principal) runtimeext.Principal {
	result := runtimeext.Principal{UserID: principal.UserID, RoleKey: principal.RoleKey, OrgID: principal.OrgID, RequestID: principal.RequestID, CorrelationID: principal.CorrelationID, CausationID: principal.CausationID, AuthorizationRevision: principal.AuthorizationRevision, Known: principal.Known}
	if principal.ActiveBusinessProfile != nil {
		result.ActiveBusinessProfile = &runtimeext.BusinessProfileReference{
			BindingKey: principal.ActiveBusinessProfile.BindingKey,
			ObjectKey:  principal.ActiveBusinessProfile.ObjectKey,
			RecordID:   principal.ActiveBusinessProfile.RecordID,
		}
	}
	return result
}

func cloneRuntimeextPrincipal(principal runtimeext.Principal) runtimeext.Principal {
	result := principal
	if principal.ActiveBusinessProfile != nil {
		profile := *principal.ActiveBusinessProfile
		result.ActiveBusinessProfile = &profile
	}
	return result
}

func toRuntimeextRecord(objectKey string, record recordmodel.Record) runtimeext.Record {
	return runtimeext.Record{ID: record.ID, ObjectKey: objectKey, Fields: actionCloneMap(record.Data), UpdatedAt: record.UpdatedAt}
}

func missingExecutorPort(operation string) error {
	return apperror.New(apperror.KindInternal, "backend.internal", nil, map[string]string{"operation": operation})
}

func actionOwnerResolutionError(entry ActionCatalogEntry) error {
	return apperror.New(apperror.KindInternal, "backend.action.owner_unresolved", entry.ResolutionError, map[string]string{"action": entry.Definition.Key, "detail": fmt.Sprint(entry.ResolutionError)})
}
