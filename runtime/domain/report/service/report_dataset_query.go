package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
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

	groups, err := reportAggregateDatasetRows(rows, report.Dataset, objects)
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.execution_invalid", err)
	}
	resultRows := reportFinalizeGroups(groups, report.Dataset)
	if err := reportApplyDatasetComparisons(resultRows, report.Dataset); err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.comparison_invalid", err)
	}
	reportSortResultRows(resultRows, report.Dataset.Sort)
	if report.Dataset.Limit > 0 && len(resultRows) > report.Dataset.Limit {
		resultRows = resultRows[:report.Dataset.Limit]
	}
	visibleSourceRows := len(rows)
	if report.Dataset.Privacy != nil {
		visibleSourceRows = 0
		for _, group := range groups {
			if len(group.privacyEntities) >= report.Dataset.Privacy.MinimumGroupSize {
				visibleSourceRows += group.sourceRows
			}
		}
	}
	return reportmodel.ReportSummary{Key: report.Key, Name: report.Name, Rows: resultRows, RowCount: len(resultRows), SourceRowCount: visibleSourceRows, Analyses: analysisResults}, nil
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
	index := map[string][]*recordmodel.Record{}
	for recordIndex := range rightRecords {
		record := &rightRecords[recordIndex]
		key, ok := reportJoinRecordKey(record, join.Equalities(), false)
		if ok {
			index[key] = append(index[key], record)
		}
	}
	result := []reportDatasetRow{}
	for _, row := range leftRows {
		left := row[strings.TrimSpace(join.LeftAlias)]
		leftKey, ok := reportJoinRecordKey(left, join.Equalities(), true)
		matches := index[leftKey]
		if !ok || len(matches) == 0 {
			if strings.TrimSpace(join.Type) == "left" {
				copyRow := reportCopyDatasetRow(row)
				copyRow[strings.TrimSpace(join.Alias)] = nil
				result = append(result, copyRow)
			}
			continue
		}
		for _, match := range matches {
			copyRow := reportCopyDatasetRow(row)
			copyRow[strings.TrimSpace(join.Alias)] = match
			result = append(result, copyRow)
		}
	}
	return result
}

func reportCopyDatasetRow(row reportDatasetRow) reportDatasetRow {
	copyRow := make(reportDatasetRow, len(row)+1)
	for alias, record := range row {
		copyRow[alias] = record
	}
	return copyRow
}

func reportDatasetRowMatchesFilters(row reportDatasetRow, filters []reportmodel.ReportDatasetFilter) bool {
	for _, filter := range filters {
		actual, exists := reportDatasetFieldValue(row, filter.Field)
		if !reportFilterMatches(actual, exists, filter) {
			return false
		}
	}
	return true
}

func reportFilterMatches(actual any, exists bool, filter reportmodel.ReportDatasetFilter) bool {
	operator := strings.TrimSpace(filter.Operator)
	if operator == "is_null" {
		return !exists || actual == nil
	}
	if operator == "not_null" {
		return exists && actual != nil
	}
	if !exists || actual == nil {
		return false
	}
	compare := func(expected any) int { return reportCompareValues(actual, expected) }
	switch operator {
	case "eq":
		return compare(filter.Value) == 0
	case "ne":
		return compare(filter.Value) != 0
	case "gt":
		return compare(filter.Value) > 0
	case "gte":
		return compare(filter.Value) >= 0
	case "lt":
		return compare(filter.Value) < 0
	case "lte":
		return compare(filter.Value) <= 0
	case "in", "not_in":
		matched := false
		for _, expected := range filter.Values {
			matched = matched || compare(expected) == 0
		}
		return matched == (operator == "in")
	case "between":
		return len(filter.Values) == 2 && compare(filter.Values[0]) >= 0 && compare(filter.Values[1]) <= 0
	case "contains":
		return strings.Contains(reportStableValue(actual), reportStableValue(filter.Value))
	case "starts_with":
		return strings.HasPrefix(reportStableValue(actual), reportStableValue(filter.Value))
	case "ends_with":
		return strings.HasSuffix(reportStableValue(actual), reportStableValue(filter.Value))
	default:
		return false
	}
}

func reportCompareValues(left, right any) int {
	leftText, rightText := reportStableValue(left), reportStableValue(right)
	leftTime, leftTimeErr := reportParseTime(leftText)
	rightTime, rightTimeErr := reportParseTime(rightText)
	if leftTimeErr == nil && rightTimeErr == nil {
		if leftTime.Before(rightTime) {
			return -1
		}
		if leftTime.After(rightTime) {
			return 1
		}
		return 0
	}
	leftNumber, leftErr := decimal.NewFromString(leftText)
	rightNumber, rightErr := decimal.NewFromString(rightText)
	if leftErr == nil && rightErr == nil {
		return leftNumber.Cmp(rightNumber)
	}
	return strings.Compare(leftText, rightText)
}

func reportStableValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
