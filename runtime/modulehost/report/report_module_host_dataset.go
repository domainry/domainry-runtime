package reportmodulehost

import (
	"context"
	"sort"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

const reportDatasetPageSize = 500

type reportDatasetRow map[string]*recordmodel.Record

func reportDatasetPushdownAllowed(ctx context.Context, access reportcontract.ReportRecordAccess, principal principalmodel.Principal, objects map[string]definitionmodel.ObjectSchema) bool {
	authorizer, ok := access.(reportcontract.ReportDatasetPushdownAuthorizer)
	if !ok {
		return false
	}
	aliases := make([]string, 0, len(objects))
	for alias := range objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	ordered := make([]definitionmodel.ObjectSchema, 0, len(objects))
	for _, alias := range aliases {
		ordered = append(ordered, objects[alias])
	}
	return authorizer.CanPushdownReportDataset(ctx, principal, ordered)
}

// reportIncludeAuthorizationProjection keeps fields required by Runtime RLS
// available to the Record owner. Report receives only the post-authorization
// projection, so these fields never become output implicitly.
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
		unsafe := leftJoined[strings.TrimSpace(join.LeftAlias)] || strings.TrimSpace(join.Type) == "left"
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
