package query

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportengine "github.com/domainry/domainry-report/query/engine"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

func (s *ReportDomainService) executeReportDataset(ctx context.Context, report reportmodel.ReportSchema, plan reportmodel.ReportDatasetPlan, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if s == nil || s.dependencies.Access == nil || s.dependencies.Records == nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.execution_unavailable", nil)
	}
	objects := make(map[string]definitionmodel.ObjectSchema, len(plan.AliasObjects))
	records := make(map[string][]recordmodel.Record, len(plan.AliasObjects))
	for _, alias := range reportDatasetAliasOrder(report.Dataset) {
		objectKey := plan.AliasObjects[alias]
		object, err := s.dependencies.Access.ReportObjectForAction(ctx, principal, objectKey, "read")
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		objects[alias] = object
	}

	rootAlias := strings.TrimSpace(report.Dataset.Source.Alias)
	for index := range report.Dataset.Measures {
		if report.Dataset.Measures[index].Operation == "count" && strings.TrimSpace(report.Dataset.Measures[index].SourceAlias) == "" {
			report.Dataset.Measures[index].SourceAlias = rootAlias
		}
	}
	rows := []reportDatasetRow{}
	if s.dependencies.DatasetRows != nil && reportDatasetPushdownAllowed(ctx, s.dependencies.Access, principal, objects) {
		queries := make(map[string]recordmodel.RecordListQuery, len(objects))
		for _, alias := range reportDatasetAliasOrder(report.Dataset) {
			query := reportAliasReadQuery(report.Dataset, alias)
			query.Page, query.PageSize = 1, reportDatasetPageSize
			query = s.dependencies.Access.NormalizeReportListQuery(ctx, objects[alias], query, principal)
			queries[alias] = reportIncludeAuthorizationProjection(query)
		}
		readRows, err := s.dependencies.DatasetRows.ReadReportDatasetRows(ctx, reportcontract.ReportDatasetRowReadRequest{WorkspaceID: principal.WorkspaceID, Plan: plan, Objects: objects, Queries: queries})
		if err != nil {
			return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.query_failed", err)
		}
		rows = make([]reportDatasetRow, 0, len(readRows))
		for _, readRow := range readRows {
			row := reportDatasetRow{}
			for alias, record := range readRow.Records {
				recordCopy := record
				row[alias] = &recordCopy
			}
			rows = append(rows, row)
		}
		for _, join := range report.Dataset.Joins {
			if err := reportValidateJoinedCardinality(rows, join); err != nil {
				return reportmodel.ReportSummary{}, err
			}
		}
	} else {
		for _, alias := range reportDatasetAliasOrder(report.Dataset) {
			items, err := s.readReportAliasRecords(ctx, principal, objects[alias], report.Dataset, alias)
			if err != nil {
				return reportmodel.ReportSummary{}, err
			}
			records[alias] = items
		}
		rows = make([]reportDatasetRow, 0, len(records[rootAlias]))
		for index := range records[rootAlias] {
			record := records[rootAlias][index]
			rows = append(rows, reportDatasetRow{rootAlias: &record})
		}
		for _, join := range report.Dataset.Joins {
			if err := reportValidateJoinCardinality(rows, records[strings.TrimSpace(join.Alias)], join); err != nil {
				return reportmodel.ReportSummary{}, err
			}
			rows = reportJoinDatasetRows(rows, records[strings.TrimSpace(join.Alias)], join)
		}
	}
	filteredRows := rows[:0]
	for _, row := range rows {
		if reportDatasetRowMatchesFilters(row, report.Dataset.Filters) {
			filteredRows = append(filteredRows, row)
		}
	}
	rows = filteredRows
	rows = reportApplyRuntimeQuery(rows, report.Dataset.RuntimeQuery)
	rows = reportApplyRuntimeTags(rows, report.Dataset.RuntimeTags)
	analysisResults, err := reportExecuteDatasetAnalyses(rows, report.Dataset)
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.analysis_invalid", err)
	}

	engineObjects, err := reportEngineObjects(objects)
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.execution_invalid", err)
	}
	aggregated, err := reportengine.Aggregate(reportEngineRows(rows), report.Dataset, engineObjects)
	if err != nil {
		var calculation *reportengine.CalculationError
		if errors.As(err, &calculation) && calculation.Stage == "comparison" {
			return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.comparison_invalid", err)
		}
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.execution_invalid", err)
	}
	return reportmodel.ReportSummary{Key: report.Key, Name: report.Name, Rows: aggregated.Rows, RowCount: len(aggregated.Rows), SourceRowCount: aggregated.VisibleSourceRows, Analyses: analysisResults}, nil
}

func reportDatasetPushdownAllowed(ctx context.Context, access reportcontract.ReportRecordAccess, principal principalmodel.Principal, objects map[string]definitionmodel.ObjectSchema) bool {
	authorizer, ok := access.(reportcontract.ReportDatasetPushdownAuthorizer)
	if !ok {
		return false
	}
	ordered := make([]definitionmodel.ObjectSchema, 0, len(objects))
	aliases := make([]string, 0, len(objects))
	for alias := range objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		ordered = append(ordered, objects[alias])
	}
	return authorizer.CanPushdownReportDataset(ctx, principal, ordered)
}

func (s *ReportDomainService) readReportAliasRecords(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, dataset reportmodel.ReportDatasetSchema, alias string) ([]recordmodel.Record, error) {
	items := []recordmodel.Record{}
	for pageNumber := 1; ; pageNumber++ {
		query := reportAliasReadQuery(dataset, alias)
		query.Page, query.PageSize = pageNumber, reportDatasetPageSize
		query = s.dependencies.Access.NormalizeReportListQuery(ctx, object, query, principal)
		query = reportIncludeAuthorizationProjection(query)
		page, err := s.dependencies.Records.ListReportRecords(ctx, principal.WorkspaceID, object, query)
		if err != nil {
			return nil, reportAppError(apperror.KindInternal, "backend.internal_error", err)
		}
		if query.ScopeExpression == nil {
			authorized := page.Items[:0]
			for _, item := range page.Items {
				if s.dependencies.Access.CanAccessReportRecord(ctx, principal, object, item) {
					authorized = append(authorized, item)
				}
			}
			page.Items = authorized
		}
		if projector, ok := s.dependencies.Access.(reportcontract.ReportRecordFieldProjector); ok {
			page.Items, err = projector.ProjectReportRecordFields(ctx, principal, object, page.Items)
			if err != nil {
				return nil, reportAppError(apperror.KindInternal, "backend.internal_error", err)
			}
		}
		items = append(items, page.Items...)
		if !page.HasNext {
			break
		}
	}
	return items, nil
}

// reportIncludeAuthorizationProjection keeps fields needed by direct RLS
// scopes available to the Record owner even when the business dataset does
// not expose those fields as dimensions or measures. CLS projection happens
// only after the record-level decision, so these internal fields never become
// report output merely because authorization needs them.
func reportIncludeAuthorizationProjection(query recordmodel.RecordListQuery) recordmodel.RecordListQuery {
	fields := make(map[string]bool, len(query.SelectFields)+6)
	for _, field := range query.SelectFields {
		if field = strings.TrimSpace(field); field != "" {
			fields[field] = true
		}
	}
	for _, field := range []string{query.OwnerField, query.DepartmentPathField, query.TeamField, query.StoreField, query.TerritoryField, query.WarehouseField} {
		if field = strings.TrimSpace(field); field != "" {
			fields[field] = true
		}
	}
	query.SelectFields = query.SelectFields[:0]
	for field := range fields {
		query.SelectFields = append(query.SelectFields, field)
	}
	sort.Strings(query.SelectFields)
	return query
}

func reportAliasReadQuery(dataset reportmodel.ReportDatasetSchema, alias string) recordmodel.RecordListQuery {
	alias = strings.TrimSpace(alias)
	fields := map[string]bool{}
	add := func(reference reportmodel.ReportDatasetField) {
		if strings.TrimSpace(reference.SourceAlias) == alias && strings.TrimSpace(reference.FieldKey) != "" {
			fields[strings.TrimSpace(reference.FieldKey)] = true
		}
	}
	filters := []recordmodel.RecordFilterExpression{}
	pushFilters := reportAliasFilterPushdownSafe(dataset, alias)
	for _, filter := range dataset.Filters {
		add(filter.Field)
		if pushFilters && strings.TrimSpace(filter.Field.SourceAlias) == alias {
			if expression, ok := reportFilterExpression(filter); ok {
				filters = append(filters, expression)
			}
		}
	}
	if runtimeQuery := dataset.RuntimeQuery; runtimeQuery != nil {
		for _, predicate := range runtimeQuery.Predicates {
			add(predicate.Field)
		}
	}
	if runtimeTags := dataset.RuntimeTags; runtimeTags != nil {
		add(runtimeTags.TargetField)
		add(runtimeTags.TagField)
	}
	for _, dimension := range dataset.Dimensions {
		add(dimension.Field)
	}
	for _, measure := range dataset.Measures {
		for _, reference := range []*reportmodel.ReportDatasetField{measure.Field, measure.StartField, measure.EndField} {
			if reference != nil {
				add(*reference)
			}
		}
	}
	if dataset.Privacy != nil {
		add(dataset.Privacy.EntityField)
	}
	for _, analysis := range dataset.Analyses {
		add(analysis.EntityField)
		add(analysis.TimeField)
		if analysis.EventField != nil {
			add(*analysis.EventField)
		}
	}
	for _, join := range dataset.Joins {
		for _, equality := range join.Equalities() {
			if strings.TrimSpace(join.LeftAlias) == alias {
				fields[strings.TrimSpace(equality.LeftField)] = true
			}
			if strings.TrimSpace(join.Alias) == alias {
				fields[strings.TrimSpace(equality.RightField)] = true
			}
		}
	}
	delete(fields, "")
	delete(fields, "id")
	selectFields := make([]string, 0, len(fields))
	for field := range fields {
		selectFields = append(selectFields, field)
	}
	sort.Strings(selectFields)
	query := recordmodel.RecordListQuery{SelectFields: selectFields}
	if len(filters) == 1 {
		query.FilterExpression = &filters[0]
	} else if len(filters) > 1 {
		query.FilterExpression = &recordmodel.RecordFilterExpression{Operator: "and", Children: filters}
	}
	return query
}

func reportAliasFilterPushdownSafe(dataset reportmodel.ReportDatasetSchema, alias string) bool {
	alias = strings.TrimSpace(alias)
	if alias == strings.TrimSpace(dataset.Source.Alias) {
		return true
	}
	leftJoined := map[string]bool{}
	for _, join := range dataset.Joins {
		parentUnsafe := leftJoined[strings.TrimSpace(join.LeftAlias)]
		unsafe := parentUnsafe || strings.TrimSpace(join.Type) == "left"
		leftJoined[strings.TrimSpace(join.Alias)] = unsafe
		if strings.TrimSpace(join.Alias) == alias {
			return !unsafe
		}
	}
	return false
}

func reportFilterExpression(filter reportmodel.ReportDatasetFilter) (recordmodel.RecordFilterExpression, bool) {
	operator := strings.TrimSpace(filter.Operator)
	expression := recordmodel.RecordFilterExpression{Operator: operator, Field: strings.TrimSpace(filter.Field.FieldKey), Value: filter.Value, Values: append([]any(nil), filter.Values...)}
	switch operator {
	case "eq", "ne", "gt", "gte", "lt", "lte", "in", "not_in", "is_null":
		return expression, true
	case "not_null":
		expression.Operator = "is_not_null"
		return expression, true
	case "between":
		if len(filter.Values) != 2 {
			return recordmodel.RecordFilterExpression{}, false
		}
		return recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
			{Operator: "gte", Field: expression.Field, Value: filter.Values[0]},
			{Operator: "lte", Field: expression.Field, Value: filter.Values[1]},
		}}, true
	default:
		return recordmodel.RecordFilterExpression{}, false
	}
}

func reportDatasetAliasOrder(dataset reportmodel.ReportDatasetSchema) []string {
	aliases := []string{strings.TrimSpace(dataset.Source.Alias)}
	for _, join := range dataset.Joins {
		aliases = append(aliases, strings.TrimSpace(join.Alias))
	}
	return aliases
}

func reportJoinDatasetRows(leftRows []reportDatasetRow, rightRecords []recordmodel.Record, join reportmodel.ReportDatasetJoin) []reportDatasetRow {
	return reportRuntimeRows(reportengine.JoinRows(reportEngineRows(leftRows), reportEngineRecords(rightRecords), join))
}

func reportDatasetRowMatchesFilters(row reportDatasetRow, filters []reportmodel.ReportDatasetFilter) bool {
	return reportengine.RowMatchesFilters(reportEngineRows([]reportDatasetRow{row})[0], filters)
}

func reportFilterMatches(actual any, exists bool, filter reportmodel.ReportDatasetFilter) bool {
	return reportengine.FilterMatches(actual, exists, filter)
}

func reportCompareValues(left, right any) int {
	return reportengine.CompareValues(left, right)
}

func reportStableValue(value any) string {
	return reportengine.StableValue(value)
}
