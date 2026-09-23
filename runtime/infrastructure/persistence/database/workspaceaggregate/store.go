package workspaceaggregate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	aggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/shopspring/decimal"
)

type Store struct {
	runtime *database.RuntimeStore
}

func NewStore(store *database.RuntimeStore) *Store { return &Store{runtime: store} }

var _ aggregatecontract.Catalog = (*Store)(nil)
var _ aggregatecontract.ActiveResolver = (*Store)(nil)
var _ aggregatecontract.UsageResolver = (*Store)(nil)
var _ aggregatecontract.Repository = (*Store)(nil)

func (store *Store) ListActive(ctx context.Context, scope principalmodel.SystemScope, limit int) ([]aggregatecontract.Workspace, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil {
		return nil, fmt.Errorf("Workspace aggregate catalog is unavailable")
	}
	if !scope.Valid() || scope.Kind != principalmodel.SystemScopeInstallation {
		return nil, fmt.Errorf("installation scope is required")
	}
	if limit <= 0 || limit > runtimeext.CrossWorkspaceAggregateMaximumWorkspaces {
		return nil, fmt.Errorf("Workspace aggregate catalog limit is invalid")
	}
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").
		Columns("id", "canonical_code").Where(query.Equal("status", "active")).
		OrderBy(query.Ascending("canonical_code"), query.Ascending("id")).Limit(limit + 1).Build()
	if err != nil {
		return nil, fmt.Errorf("build Workspace aggregate catalog query: %w", err)
	}
	rows, err := store.runtime.DB().QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query Workspace aggregate catalog: %w", err)
	}
	defer rows.Close()
	result := make([]aggregatecontract.Workspace, 0, limit+1)
	for rows.Next() {
		var workspace aggregatecontract.Workspace
		if err := rows.Scan(&workspace.ID, &workspace.CanonicalCode); err != nil {
			return nil, err
		}
		workspace.ID, workspace.CanonicalCode = strings.TrimSpace(workspace.ID), strings.TrimSpace(workspace.CanonicalCode)
		if _, err := principalmodel.NewWorkspaceID(workspace.ID); err != nil || workspace.CanonicalCode == "" {
			return nil, fmt.Errorf("invalid installation Workspace catalog row")
		}
		result = append(result, workspace)
	}
	return result, rows.Err()
}

func (store *Store) ResolveActive(ctx context.Context, scope principalmodel.SystemScope, workspaceIDs []string) (map[string]aggregatecontract.Workspace, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil {
		return nil, fmt.Errorf("Workspace catalog is unavailable")
	}
	if !scope.Valid() || scope.Kind != principalmodel.SystemScopeInstallation {
		return nil, fmt.Errorf("installation scope is required")
	}
	if len(workspaceIDs) == 0 || len(workspaceIDs) > runtimeext.WorkspaceIdentityUsageMaximumPageSize {
		return nil, fmt.Errorf("Workspace catalog resolve limit is invalid")
	}
	executor := database.ActionExecutionTransaction(ctx)
	if executor == nil {
		return nil, fmt.Errorf("Action transaction is required for Workspace catalog resolve")
	}
	values := make([]any, 0, len(workspaceIDs))
	seen := make(map[string]bool, len(workspaceIDs))
	for _, raw := range workspaceIDs {
		id, err := principalmodel.NewWorkspaceID(strings.TrimSpace(raw))
		if err != nil || seen[id.String()] {
			return nil, fmt.Errorf("Workspace catalog resolve scope is invalid")
		}
		seen[id.String()] = true
		values = append(values, id.String())
	}
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Alias("workspace").
		Projections(
			query.Project(query.QualifiedColumn("workspace", "id")),
			query.Project(query.QualifiedColumn("workspace", "canonical_code")),
			query.Project(query.QualifiedColumn("workspace", "name")),
			query.Project(query.QualifiedColumn("workspace", "plan")),
			query.Project(query.QualifiedColumn("workspace", "included_user_limit")),
			query.Project(query.QualifiedColumn("workspace", "max_user_limit")),
		).
		Where(query.And(
			query.InExpression(query.QualifiedColumn("workspace", "id"), values...),
			query.EqualValue(query.QualifiedColumn("workspace", "status"), "active"),
		)).Build()
	if err != nil {
		return nil, fmt.Errorf("build Workspace catalog resolve query: %w", err)
	}
	rows, err := executor.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query Workspace catalog resolve: %w", err)
	}
	defer rows.Close()
	result := make(map[string]aggregatecontract.Workspace, len(workspaceIDs))
	for rows.Next() {
		var workspace aggregatecontract.Workspace
		if err := rows.Scan(
			&workspace.ID, &workspace.CanonicalCode, &workspace.DisplayName, &workspace.CommercialPlan,
			&workspace.IncludedUserLimit, &workspace.MaxUserLimit,
		); err != nil {
			return nil, err
		}
		workspace.ID, workspace.CanonicalCode = strings.TrimSpace(workspace.ID), strings.TrimSpace(workspace.CanonicalCode)
		workspace.DisplayName, workspace.CommercialPlan = strings.TrimSpace(workspace.DisplayName), strings.TrimSpace(workspace.CommercialPlan)
		if !seen[workspace.ID] || workspace.CanonicalCode == "" || workspace.DisplayName == "" || workspace.CommercialPlan == "" ||
			workspace.IncludedUserLimit < 0 || workspace.MaxUserLimit < workspace.IncludedUserLimit {
			return nil, fmt.Errorf("invalid installation Workspace catalog row")
		}
		result[workspace.ID] = workspace
	}
	return result, rows.Err()
}

func (store *Store) ResolveUsageWorkspace(ctx context.Context, scope principalmodel.SystemScope, canonicalCode string, expectedRevision int64) (aggregatecontract.Workspace, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil {
		return aggregatecontract.Workspace{}, fmt.Errorf("Workspace usage catalog is unavailable")
	}
	if !scope.Valid() || scope.Kind != principalmodel.SystemScopeInstallation {
		return aggregatecontract.Workspace{}, fmt.Errorf("installation scope is required")
	}
	canonicalCode = strings.TrimSpace(canonicalCode)
	if canonicalCode == "" || expectedRevision < 1 {
		return aggregatecontract.Workspace{}, fmt.Errorf("Workspace usage scope is invalid")
	}
	executor := database.ActionExecutionTransaction(ctx)
	if executor == nil {
		return aggregatecontract.Workspace{}, fmt.Errorf("Action transaction is required for Workspace usage resolve")
	}
	builder := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Alias("workspace").
		Projections(workspaceUsageProjections()...).
		Where(query.EqualExpressions(query.QualifiedColumn("workspace", "canonical_code"), query.Value(canonicalCode))).Limit(1)
	var err error
	builder, err = store.runtime.RuntimeProfile().ApplyClaimLock(builder, false)
	if err != nil {
		return aggregatecontract.Workspace{}, fmt.Errorf("lock Workspace usage scope: %w", err)
	}
	statement, arguments, err := builder.Build()
	if err != nil {
		return aggregatecontract.Workspace{}, fmt.Errorf("build Workspace usage resolve query: %w", err)
	}
	workspace, err := scanWorkspaceUsage(executor.QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return aggregatecontract.Workspace{}, aggregatecontract.ErrUsageWorkspaceNotFound
	}
	if err != nil {
		return aggregatecontract.Workspace{}, fmt.Errorf("query Workspace usage scope: %w", err)
	}
	if workspace.Revision != expectedRevision {
		return aggregatecontract.Workspace{}, aggregatecontract.ErrUsageWorkspaceRevisionConflict
	}
	if workspace.Status != "active" {
		return aggregatecontract.Workspace{}, aggregatecontract.ErrUsageWorkspaceInactive
	}
	if err := validateWorkspaceUsage(workspace, canonicalCode); err != nil {
		return aggregatecontract.Workspace{}, err
	}
	return workspace, nil
}

type workspaceUsageScanner interface{ Scan(...any) error }

func scanWorkspaceUsage(scanner workspaceUsageScanner) (aggregatecontract.Workspace, error) {
	var workspace aggregatecontract.Workspace
	err := scanner.Scan(
		&workspace.ID, &workspace.CanonicalCode, &workspace.DisplayName, &workspace.Status, &workspace.Revision,
		&workspace.CommercialPlan, &workspace.IncludedUserLimit, &workspace.MaxUserLimit,
		&workspace.IncludedCustomerLimit, &workspace.MaxCustomerLimit, &workspace.IncludedStoreLimit, &workspace.MaxStores,
		&workspace.ContractDate, &workspace.BillingDay, &workspace.BillingContactName, &workspace.BillingContactPhone,
		&workspace.BillingContactEmail, &workspace.BillingContactAddress, &workspace.BillingContactNotes, &workspace.CommercialRevision,
	)
	return workspace, err
}

func workspaceUsageProjections() []query.Projection {
	result := make([]query.Projection, 0, 20)
	for _, column := range []string{"id", "canonical_code", "name", "status", "revision"} {
		result = append(result, query.Project(query.QualifiedColumn("workspace", column)))
	}
	for _, column := range []string{
		"plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit",
		"included_store_limit", "max_stores", "contract_date", "billing_day", "billing_contact_name",
		"billing_contact_phone", "billing_contact_email", "billing_contact_address", "billing_contact_notes", "commercial_revision",
	} {
		result = append(result, query.Project(query.QualifiedColumn("workspace", column)))
	}
	return result
}

func validateWorkspaceUsage(workspace aggregatecontract.Workspace, canonicalCode string) error {
	workspace.ID, workspace.CanonicalCode = strings.TrimSpace(workspace.ID), strings.TrimSpace(workspace.CanonicalCode)
	workspace.DisplayName, workspace.CommercialPlan = strings.TrimSpace(workspace.DisplayName), strings.TrimSpace(workspace.CommercialPlan)
	if _, err := principalmodel.NewWorkspaceID(workspace.ID); err != nil || workspace.CanonicalCode != canonicalCode || workspace.DisplayName == "" ||
		workspace.Status != "active" || workspace.Revision < 1 || workspace.CommercialRevision < 1 || workspace.CommercialPlan == "" ||
		workspace.IncludedUserLimit < 0 || workspace.MaxUserLimit < workspace.IncludedUserLimit ||
		workspace.IncludedCustomerLimit < 0 || workspace.MaxCustomerLimit < workspace.IncludedCustomerLimit ||
		workspace.IncludedStoreLimit < 1 || workspace.MaxStores < workspace.IncludedStoreLimit || workspace.BillingDay < 1 || workspace.BillingDay > 31 {
		return fmt.Errorf("invalid installation Workspace usage catalog row")
	}
	if _, err := time.Parse("2006-01-02", workspace.ContractDate); err != nil {
		return fmt.Errorf("invalid installation Workspace usage commercial contract date: %w", err)
	}
	return nil
}

func (store *Store) Aggregate(ctx context.Context, request aggregatecontract.Query) (aggregatecontract.Result, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil {
		return aggregatecontract.Result{}, fmt.Errorf("Workspace aggregate store is unavailable")
	}
	if len(request.Workspaces) == 0 || request.MaxSourceRows <= 0 || request.MaxResultRows <= 0 {
		return aggregatecontract.Result{}, fmt.Errorf("Workspace aggregate query is invalid")
	}
	workspaceIDs := make([]string, 0, len(request.Workspaces))
	codes := make(map[string]string, len(request.Workspaces))
	for _, workspace := range request.Workspaces {
		id, err := principalmodel.NewWorkspaceID(workspace.ID)
		if err != nil || strings.TrimSpace(workspace.CanonicalCode) == "" {
			return aggregatecontract.Result{}, fmt.Errorf("Workspace aggregate scope is invalid")
		}
		workspaceIDs = append(workspaceIDs, id.String())
		codes[id.String()] = strings.TrimSpace(workspace.CanonicalCode)
	}
	if request.RecordQuery.AuthorizationDiagnostic != nil {
		return aggregatecontract.Result{}, fmt.Errorf("Workspace aggregate authorization is invalid")
	}
	normalizedFilter, err := recordvalidation.RecordNormalizeFilterExpression(request.Object, request.RecordQuery.FilterExpression)
	if err != nil {
		return aggregatecontract.Result{}, fmt.Errorf("normalize Workspace aggregate filter: %w", err)
	}
	request.RecordQuery.FilterExpression = normalizedFilter
	queryValue := recordpersistence.RecordQueryDatabaseValues(store.runtime.RuntimeProfile(), request.Object, request.RecordQuery)
	if queryValue.ScopeExpression != nil {
		preserved := querypersistence.PreserveScopeRelations(*queryValue.ScopeExpression)
		queryValue.ScopeExpression = &preserved
	}
	predicate, err := querypersistence.BuildWorkspaceSetPredicate(store.runtime, workspaceIDs, queryValue)
	if err != nil {
		return aggregatecontract.Result{}, err
	}
	tx, err := store.runtime.DB().BeginTx(ctx, &sql.TxOptions{Isolation: store.runtime.RuntimeProfile().RecordReadIsolation(), ReadOnly: true})
	if err != nil {
		return aggregatecontract.Result{}, fmt.Errorf("begin Workspace aggregate snapshot: %w", err)
	}
	defer tx.Rollback()
	countStatement, countArguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), request.Object.Key).
		Projections(query.Project(query.CountAll())).Where(predicate).Build()
	if err != nil {
		return aggregatecontract.Result{}, err
	}
	var sourceRows int64
	if err := tx.QueryRowContext(ctx, countStatement, countArguments...).Scan(&sourceRows); err != nil {
		return aggregatecontract.Result{}, fmt.Errorf("count Workspace aggregate source: %w", err)
	}
	if sourceRows > int64(request.MaxSourceRows) {
		return aggregatecontract.Result{}, &aggregatecontract.LimitExceededError{Kind: aggregatecontract.LimitSourceRows, Observed: sourceRows, SourceRowCount: sourceRows}
	}
	projections, groups, orders, outputs, transforms, err := store.aggregateShape(request.Object, request.Dimensions, request.Measures)
	if err != nil {
		return aggregatecontract.Result{}, err
	}
	builder := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), request.Object.Key).
		Projections(projections...).Where(predicate).Limit(request.MaxResultRows + 1)
	if len(groups) > 0 {
		builder.GroupBy(groups...).OrderBy(orders...)
	}
	statement, arguments, err := builder.Build()
	if err != nil {
		return aggregatecontract.Result{}, err
	}
	statement, err = store.applyDimensionTransforms(statement, transforms)
	if err != nil {
		return aggregatecontract.Result{}, err
	}
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return aggregatecontract.Result{}, fmt.Errorf("query Workspace aggregate: %w", err)
	}
	defer rows.Close()
	result := aggregatecontract.Result{Rows: make([]map[string]string, 0, request.MaxResultRows), SourceRowCount: sourceRows}
	for rows.Next() {
		raw := make([]any, len(outputs))
		destinations := make([]any, len(raw))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return aggregatecontract.Result{}, err
		}
		if len(result.Rows) == request.MaxResultRows {
			return aggregatecontract.Result{}, &aggregatecontract.LimitExceededError{Kind: aggregatecontract.LimitResultRows, Observed: int64(len(result.Rows) + 1), SourceRowCount: sourceRows}
		}
		row := map[string]string{}
		for index, output := range outputs {
			if raw[index] == nil {
				continue
			}
			value, normalizeErr := store.normalizeResult(output, raw[index], codes)
			if normalizeErr != nil {
				return aggregatecontract.Result{}, normalizeErr
			}
			row[output.key] = value
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return aggregatecontract.Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return aggregatecontract.Result{}, err
	}
	return result, nil
}

type aggregateOutput struct {
	key       string
	field     definitionmodel.FieldSchema
	workspace bool
	decimal   bool
}

type aggregateDimensionTransform struct {
	sentinel string
	fragment string
}

func (store *Store) aggregateShape(object definitionmodel.ObjectSchema, dimensions []runtimeext.CrossWorkspaceAggregateDimension, measures []runtimeext.CrossWorkspaceAggregateMeasure) ([]query.Projection, []query.Expression, []query.Order, []aggregateOutput, []aggregateDimensionTransform, error) {
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	projections := make([]query.Projection, 0, len(dimensions)+len(measures))
	groups := make([]query.Expression, 0, len(dimensions))
	orders := make([]query.Order, 0, len(dimensions))
	outputs := make([]aggregateOutput, 0, len(dimensions)+len(measures))
	transforms := make([]aggregateDimensionTransform, 0, len(dimensions))
	for _, dimension := range dimensions {
		if dimension.Field == runtimeext.CrossWorkspaceDimensionWorkspace {
			if dimension.Transform != nil {
				return nil, nil, nil, nil, nil, fmt.Errorf("Workspace aggregate dimension cannot be transformed")
			}
			expression := query.Column("workspace_id")
			projections = append(projections, query.ProjectAs(expression, dimension.Key))
			groups = append(groups, expression)
			orders = append(orders, query.AscendingExpression(expression))
			outputs = append(outputs, aggregateOutput{key: dimension.Key, workspace: true})
			continue
		}
		field, ok := fields[dimension.Field]
		if !ok {
			return nil, nil, nil, nil, nil, fmt.Errorf("aggregate dimension field is missing")
		}
		expression, _, err := store.fieldExpression(field)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if dimension.Transform != nil {
			if !dimension.Transform.Valid() || field.Type != "date" && field.Type != "datetime" {
				return nil, nil, nil, nil, nil, fmt.Errorf("aggregate date bucket requires a date or datetime field")
			}
			dateBucket := dimension.Transform.DateBucket
			fragment, transformErr := store.runtime.RuntimeProfile().ReportDateBucket(store.runtime.RuntimeRenderer().Identifier(field.Key), dateBucket.Grain, dateBucket.TimeZone, field.Type == "date")
			if transformErr != nil {
				return nil, nil, nil, nil, nil, fmt.Errorf("build aggregate date bucket: %w", transformErr)
			}
			sentinel := fmt.Sprintf("__runtime_date_bucket_%d", len(transforms))
			expression = query.Column(sentinel)
			transforms = append(transforms, aggregateDimensionTransform{sentinel: sentinel, fragment: fragment})
		}
		projections = append(projections, query.ProjectAs(expression, dimension.Key))
		groups = append(groups, expression)
		orders = append(orders, query.AscendingExpression(expression))
		outputs = append(outputs, aggregateOutput{key: dimension.Key, field: field, decimal: field.Type == "currency" || field.Type == "percent"})
	}
	for _, measure := range measures {
		expression := query.CountAll()
		output := aggregateOutput{key: measure.Key}
		if measure.Operation != runtimeext.AggregateCount {
			field, ok := fields[measure.Field]
			if !ok {
				return nil, nil, nil, nil, nil, fmt.Errorf("aggregate measure field is missing")
			}
			fieldExpression, exactMinor, fieldErr := store.fieldExpression(field)
			if fieldErr != nil {
				return nil, nil, nil, nil, nil, fieldErr
			}
			switch measure.Operation {
			case runtimeext.AggregateSum:
				if exactMinor {
					expression = query.Func("runtime_decimal_sum_minor", fieldExpression)
				} else {
					expression = query.Sum(fieldExpression)
				}
			case runtimeext.AggregateAvg:
				if exactMinor {
					expression = query.Func("runtime_decimal_avg_minor", fieldExpression)
				} else {
					expression = query.Avg(fieldExpression)
				}
			case runtimeext.AggregateMin:
				expression = query.Min(fieldExpression)
			case runtimeext.AggregateMax:
				expression = query.Max(fieldExpression)
			default:
				return nil, nil, nil, nil, nil, fmt.Errorf("aggregate operation is invalid")
			}
			output.field, output.decimal = field, field.Type == "currency" || field.Type == "percent"
		}
		projections = append(projections, query.ProjectAs(expression, measure.Key))
		outputs = append(outputs, output)
	}
	return projections, groups, orders, outputs, transforms, nil
}

func (store *Store) applyDimensionTransforms(statement string, transforms []aggregateDimensionTransform) (string, error) {
	for _, transform := range transforms {
		identifier := store.runtime.RuntimeRenderer().Identifier(transform.sentinel)
		if strings.Count(statement, identifier) == 0 || strings.TrimSpace(transform.fragment) == "" {
			return "", fmt.Errorf("aggregate date bucket placeholder is missing")
		}
		// domainry-orm has no dialect-profile expression hook. This bounded
		// substitution is therefore the local raw-SQL exception: both the
		// placeholder and fragment are Runtime-created, the column is rendered
		// as an identifier, and grain/timezone were validated static metadata.
		// No Handler invocation value or project SQL enters the statement.
		statement = strings.ReplaceAll(statement, identifier, "("+transform.fragment+")")
	}
	return statement, nil
}

func (store *Store) fieldExpression(field definitionmodel.FieldSchema) (query.Expression, bool, error) {
	expression := query.Expression(query.Column(field.Key))
	if store.runtime.RuntimeProfile().OrderedDecimalTextStorage() && (field.Type == "currency" || field.Type == "percent") {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return nil, false, err
		}
		expression = query.Func("runtime_decimal_minor", expression, query.Value(config.Precision))
		return expression, true, nil
	}
	return expression, false, nil
}

func (store *Store) normalizeResult(output aggregateOutput, raw any, workspaceCodes map[string]string) (string, error) {
	if bytes, ok := raw.([]byte); ok {
		raw = string(bytes)
	}
	text := strings.TrimSpace(fmt.Sprint(raw))
	if output.workspace {
		code, ok := workspaceCodes[text]
		if !ok {
			return "", fmt.Errorf("aggregate returned a Workspace outside its scope")
		}
		return code, nil
	}
	if output.decimal {
		config, err := recordmodel.RecordNormalizeDecimalConfig(output.field.Config)
		if err != nil {
			return "", err
		}
		if store.runtime.RuntimeProfile().OrderedDecimalTextStorage() {
			minor, ok := new(big.Int).SetString(text, 10)
			if !ok {
				return "", fmt.Errorf("invalid exact aggregate result")
			}
			return decimal.NewFromBigInt(minor, -config.Scale).StringFixed(config.Scale), nil
		}
		return recordmodel.RecordNormalizeDecimal(text, config)
	}
	return text, nil
}
