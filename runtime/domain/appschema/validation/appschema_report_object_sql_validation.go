package validation

import (
	"strconv"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportobjectsql "github.com/domainry/domainry-report-sdk/query/objectsql"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

func (v *reportDefinitionValidator) validateExecutionDefinition() {
	if v.report.ObjectSQLV1 == nil {
		return
	}
	plan, err := reportcontract.CompileReportObjectSQL(*v.report.ObjectSQLV1, v.objects)
	if err != nil {
		if planErr, ok := err.(*reportmodel.ReportObjectSQLPlanError); ok {
			v.issue(planErr.Code, planErr.Path, planErr.Params)
		} else {
			v.issue("backend.report.object_sql_invalid", "object_sql_v1.sql", nil)
		}
		return
	}
	v.validateObjectSQLExplicitBounds(plan)
	if reportmodel.ReportCrossWorkspaceAggregate(v.report) {
		v.validateCrossWorkspaceAggregatePlan(plan)
	}
	for _, source := range plan.Sources {
		for _, fieldKey := range source.Fields {
			v.validateAudienceFieldPermission("object_sql_v1.sql", source.ObjectKey, fieldKey, "read")
		}
	}
}

func (v *reportDefinitionValidator) validateCrossWorkspaceAggregatePlan(plan reportmodel.ReportObjectSQLPlan) {
	for _, projection := range plan.Projections {
		if reportObjectSQLContainsAggregate(projection.Expression) {
			continue
		}
		if (projection.Expression.Kind == "field" && projection.Expression.FieldKey == "workspace_id") || (projection.Expression.Kind == "function" && projection.Expression.Name == "date_bucket") {
			continue
		}
		v.issue("backend.report.cross_workspace_raw_projection_forbidden", "object_sql_v1.sql", map[string]string{"projection": projection.Alias})
	}
	for _, group := range plan.GroupBy {
		if (group.Kind == "field" && group.FieldKey == "workspace_id") || (group.Kind == "function" && group.Name == "date_bucket") {
			continue
		}
		v.issue("backend.report.cross_workspace_grouping_forbidden", "object_sql_v1.sql", nil)
	}
	if len(plan.GroupBy) == 0 {
		hasAggregate := false
		for _, projection := range plan.Projections {
			hasAggregate = hasAggregate || reportObjectSQLContainsAggregate(projection.Expression)
		}
		if !hasAggregate {
			v.issue("backend.report.cross_workspace_aggregate_required", "object_sql_v1.sql", nil)
		}
	}
}

func reportObjectSQLContainsAggregate(expression reportmodel.ReportObjectSQLExpression) bool {
	if expression.Kind == "aggregate" {
		return true
	}
	for _, argument := range expression.Arguments {
		if reportObjectSQLContainsAggregate(argument) {
			return true
		}
	}
	for _, when := range expression.Whens {
		if reportObjectSQLContainsAggregate(when.Condition) || reportObjectSQLContainsAggregate(when.Value) {
			return true
		}
	}
	return expression.Else != nil && reportObjectSQLContainsAggregate(*expression.Else)
}

// validateObjectSQLExplicitBounds shifts the empirical IF-3 pit into the
// authoring contract: object_sql SQL without an authored ORDER BY and literal
// LIMIT compiles, but the Runtime then applies the implicit default limit
// (1000 rows) - exactly the sync/async export threshold - so a growing report
// silently truncates and pagination order is an unauthored implementation
// detail. The requirement is skipped for single-row aggregate plans and only
// enforced on the definition-contract path (model validate / Blueprint
// authoring); already-published definitions keep executing unchanged.
func (v *reportDefinitionValidator) validateObjectSQLExplicitBounds(plan reportmodel.ReportObjectSQLPlan) {
	if !v.requireObjectSQLExplicitBounds || reportobjectsql.ReportObjectSQLPlanSingleRow(plan) {
		return
	}
	// GROUP BY plans inherit a deterministic order from their grouping terms;
	// every other multi-row plan must author its own ORDER BY.
	if !plan.ExplicitOrderBy && len(plan.GroupBy) == 0 {
		v.issue("backend.report.object_sql_order_by_required", "object_sql_v1.sql.order_by", map[string]string{
			"missing_clause": "ORDER BY",
			"reason":         "declare a deterministic ORDER BY in the object_sql statement; multi-row reports must not rely on implicit ordering",
		})
	}
	if !plan.ExplicitLimit {
		v.issue("backend.report.object_sql_limit_required", "object_sql_v1.sql.limit", map[string]string{
			"missing_clause": "LIMIT",
			"default_limit":  strconv.Itoa(reportobjectsql.ReportObjectSQLDefaultLimitRows),
			"maximum":        strconv.Itoa(reportobjectsql.ReportObjectSQLMaximumLimitRows),
			"reason":         "declare a literal LIMIT sized to the business bound; the implicit default of 1000 rows equals the sync/async export threshold and silently truncates larger reports",
		})
	}
}
