package runtimeext

import (
	"context"
	"regexp"
	"strings"
	"time"
)

var crossWorkspaceAggregateOutputKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

const (
	CrossWorkspaceDimensionWorkspace                  = "$workspace"
	CrossWorkspaceAggregateMaximumWorkspaces          = 256
	CrossWorkspaceAggregateMaximumSourceRows          = 100000
	CrossWorkspaceAggregateMaximumResultRows          = 1000
	CrossWorkspaceAggregateMaximumTimeoutMilliseconds = 10000
)

type AggregateOperation string

const (
	AggregateCount AggregateOperation = "count"
	AggregateSum   AggregateOperation = "sum"
	AggregateMin   AggregateOperation = "min"
	AggregateMax   AggregateOperation = "max"
	AggregateAvg   AggregateOperation = "avg"
)

func (operation AggregateOperation) Valid() bool {
	switch operation {
	case AggregateCount, AggregateSum, AggregateMin, AggregateMax, AggregateAvg:
		return true
	default:
		return false
	}
}

// CrossWorkspaceAggregateDimension is a fixed, source-declared grouping. The
// special $workspace field projects the public canonical Workspace code; the
// physical workspace_id never crosses the Runtime boundary.
type CrossWorkspaceAggregateDimension struct {
	Key       string
	Field     string
	Transform *CrossWorkspaceAggregateDimensionTransform
}

// CrossWorkspaceAggregateDimensionTransform is deliberately closed: generated
// project code may select only the Runtime-owned date-bucket transform and can
// never provide SQL or a generic expression.
type CrossWorkspaceAggregateDimensionTransform struct {
	DateBucket *CrossWorkspaceAggregateDateBucketTransform
}

// CrossWorkspaceAggregateDateBucketTransform fixes both grain and IANA zone in
// the source-owned Handler descriptor. Neither value is invocation input.
type CrossWorkspaceAggregateDateBucketTransform struct {
	Grain    string
	TimeZone string
}

func (transform CrossWorkspaceAggregateDimensionTransform) Valid() bool {
	return transform.DateBucket != nil && transform.DateBucket.Valid()
}

func (transform CrossWorkspaceAggregateDateBucketTransform) Valid() bool {
	grain, zone := strings.TrimSpace(transform.Grain), strings.TrimSpace(transform.TimeZone)
	switch grain {
	case "hour", "day", "week", "month", "quarter", "year":
	default:
		return false
	}
	if zone == "" || zone == "Local" || zone != "UTC" && !strings.Contains(zone, "/") {
		return false
	}
	_, err := time.LoadLocation(zone)
	return err == nil
}

// CrossWorkspaceAggregateMeasure is a fixed, source-declared aggregate. Count
// has no field; every other operation names one authored business field.
type CrossWorkspaceAggregateMeasure struct {
	Key       string
	Operation AggregateOperation
	Field     string
}

// CrossWorkspaceAggregateFilterCapability fixes both a field and its allowed
// operators. A Handler invocation may supply values but cannot select fields.
type CrossWorkspaceAggregateFilterCapability struct {
	Field     string
	Operators []string
}

// CrossWorkspaceAggregateCapability is an Action-scoped, immutable aggregate
// grant. Limits are required and additionally capped by Runtime constants.
type CrossWorkspaceAggregateCapability struct {
	Key                 string
	ObjectKey           string
	Dimensions          []CrossWorkspaceAggregateDimension
	Measures            []CrossWorkspaceAggregateMeasure
	Filters             []CrossWorkspaceAggregateFilterCapability
	MaxWorkspaces       int
	MaxSourceRows       int
	MaxResultRows       int
	TimeoutMilliseconds int
}

func (capability CrossWorkspaceAggregateCapability) Valid() bool {
	if strings.TrimSpace(capability.Key) == "" || strings.TrimSpace(capability.ObjectKey) == "" || len(capability.Dimensions) > 16 || len(capability.Measures) == 0 || len(capability.Measures) > 32 || len(capability.Filters) > 32 ||
		capability.MaxWorkspaces <= 0 || capability.MaxWorkspaces > CrossWorkspaceAggregateMaximumWorkspaces ||
		capability.MaxSourceRows <= 0 || capability.MaxSourceRows > CrossWorkspaceAggregateMaximumSourceRows ||
		capability.MaxResultRows <= 0 || capability.MaxResultRows > CrossWorkspaceAggregateMaximumResultRows ||
		capability.TimeoutMilliseconds <= 0 || capability.TimeoutMilliseconds > CrossWorkspaceAggregateMaximumTimeoutMilliseconds {
		return false
	}
	aliases, fields := map[string]bool{}, map[string]bool{}
	for _, dimension := range capability.Dimensions {
		key, field := strings.TrimSpace(dimension.Key), strings.TrimSpace(dimension.Field)
		if !validAggregateOutputKey(key) || field == "" || aliases[key] || fields[field] || (field != CrossWorkspaceDimensionWorkspace && !validAggregateBusinessField(field)) ||
			(dimension.Transform != nil && (field == CrossWorkspaceDimensionWorkspace || !dimension.Transform.Valid())) {
			return false
		}
		aliases[key], fields[field] = true, true
	}
	for _, measure := range capability.Measures {
		key, field := strings.TrimSpace(measure.Key), strings.TrimSpace(measure.Field)
		if !validAggregateOutputKey(key) || aliases[key] || !measure.Operation.Valid() || (measure.Operation == AggregateCount) != (field == "") || (field != "" && !validAggregateBusinessField(field)) {
			return false
		}
		aliases[key] = true
	}
	filterFields := map[string]bool{}
	for _, filter := range capability.Filters {
		field := strings.TrimSpace(filter.Field)
		if !validAggregateBusinessField(field) || filterFields[field] || len(filter.Operators) == 0 {
			return false
		}
		filterFields[field] = true
		operators := map[string]bool{}
		for _, raw := range filter.Operators {
			operator := strings.TrimSpace(raw)
			if !validCrossWorkspaceFilterOperator(operator) || operators[operator] {
				return false
			}
			operators[operator] = true
		}
	}
	return true
}

func validAggregateOutputKey(key string) bool {
	key = strings.TrimSpace(key)
	return crossWorkspaceAggregateOutputKeyPattern.MatchString(key) && validAggregateBusinessField(key)
}

func validAggregateBusinessField(field string) bool {
	field = strings.TrimSpace(field)
	if field == "" || strings.HasPrefix(field, "_") {
		return false
	}
	switch field {
	case "workspace_id", "tenant_id", "owner_org_id":
		return false
	default:
		return true
	}
}

func validCrossWorkspaceFilterOperator(operator string) bool {
	switch strings.TrimSpace(operator) {
	case "eq", "ne", "gt", "gte", "lt", "lte", "in", "not_in", "is_null", "is_not_null":
		return true
	default:
		return false
	}
}

type CrossWorkspaceAggregateFilter struct {
	Field    string
	Operator string
	Value    any
	Values   []any
}

type CrossWorkspaceAggregateRequest struct {
	CapabilityKey string
	Filters       []CrossWorkspaceAggregateFilter
	Limit         int
}

type CrossWorkspaceAggregateRow struct {
	Values map[string]string
}

type CrossWorkspaceAggregateResult struct {
	Rows           []CrossWorkspaceAggregateRow
	WorkspaceCount int
	SourceRowCount int64
}

// CrossWorkspaceAggregateExecution is optional so older test doubles and
// generated bindings remain source compatible. Generated capabilities call
// ExecuteCrossWorkspaceAggregate instead of asserting this interface.
type CrossWorkspaceAggregateExecution interface {
	CrossWorkspaceAggregate(context.Context, CrossWorkspaceAggregateRequest) (CrossWorkspaceAggregateResult, error)
}

func ExecuteCrossWorkspaceAggregate(ctx context.Context, execution ActionExecution, request CrossWorkspaceAggregateRequest) (CrossWorkspaceAggregateResult, error) {
	aggregator, ok := execution.(CrossWorkspaceAggregateExecution)
	if !ok {
		return CrossWorkspaceAggregateResult{}, &BusinessError{Code: "backend.action.cross_workspace_aggregate_unavailable", Message: "Runtime cross-Workspace aggregate execution is unavailable"}
	}
	return aggregator.CrossWorkspaceAggregate(ctx, request)
}
