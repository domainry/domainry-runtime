package contract

import (
	"fmt"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

const ReportRequiredIndexMissingCode = "backend.report.required_index_missing"

// ReportDatasetIndexDiagnostic is the canonical publication diagnostic for a
// dataset access path that would otherwise require an unbounded scan. Fields
// contains the recommended tenant-leading index in physical order.
type ReportDatasetIndexDiagnostic struct {
	Code      string
	Path      string
	ObjectKey string
	FieldKey  string
	Usage     string
	Fields    []string
}

// ReportDatasetIndexDiagnostics derives index requirements exclusively from
// generic dataset metadata. Both single-resource authoring and whole-manifest
// publication use this function so a report cannot pass one gate and fail the
// other with a different performance policy.
func ReportDatasetIndexDiagnostics(report reportmodel.ReportSchema, objects map[string]definitionmodel.ObjectSchema) []ReportDatasetIndexDiagnostic {
	dataset := report.Dataset
	aliases := reportmodel.ReportDatasetAliasObjects(dataset)
	diagnostics := []ReportDatasetIndexDiagnostic{}
	seen := map[string]bool{}
	require := func(path, alias, fieldKey, usage string) {
		objectKey := strings.TrimSpace(aliases[strings.TrimSpace(alias)])
		fieldKey = strings.TrimSpace(fieldKey)
		if objectKey == "" || fieldKey == "" || fieldKey == "id" || fieldKey == "created_at" || fieldKey == "updated_at" {
			return
		}
		object, exists := objects[objectKey]
		if !exists {
			return
		}
		field, exists := reportDatasetObjectField(object, fieldKey)
		if !exists || reportDatasetFieldIndexed(field) {
			return
		}
		key := objectKey + "\x00" + fieldKey + "\x00" + usage
		if seen[key] {
			return
		}
		seen[key] = true
		diagnostics = append(diagnostics, ReportDatasetIndexDiagnostic{
			Code: ReportRequiredIndexMissingCode, Path: path, ObjectKey: objectKey,
			FieldKey: fieldKey, Usage: usage, Fields: []string{"workspace_id", fieldKey},
		})
	}
	for index, join := range dataset.Joins {
		path := fmt.Sprintf("dataset.joins[%d]", index)
		for equalityIndex, equality := range join.Equalities() {
			equalityPath := fmt.Sprintf("%s.field_equalities[%d]", path, equalityIndex)
			if len(join.FieldEqualities) == 0 {
				equalityPath = path
			}
			require(equalityPath+".left_field", join.LeftAlias, equality.LeftField, "join")
			require(equalityPath+".right_field", join.Alias, equality.RightField, "join")
		}
	}
	for index, filter := range dataset.Filters {
		require(fmt.Sprintf("dataset.filters[%d].field", index), filter.Field.SourceAlias, filter.Field.FieldKey, "filter")
	}
	for _, group := range []struct {
		name       string
		predicates []reportmodel.ReportDatasetPredicate
	}{{"query_predicates", dataset.QueryPredicates}, {"tag_predicates", dataset.TagPredicates}} {
		for index, predicate := range group.predicates {
			for filterIndex, filter := range predicate.Filters {
				require(fmt.Sprintf("dataset.%s[%d].filters[%d].field", group.name, index, filterIndex), filter.Field.SourceAlias, filter.Field.FieldKey, "filter")
			}
		}
	}
	for index, dimension := range dataset.Dimensions {
		grain := strings.TrimSpace(dimension.TimeGrain)
		if grain == "" {
			grain = strings.TrimSpace(dataset.DefaultTimeGrain)
		}
		if grain != "" {
			require(fmt.Sprintf("dataset.dimensions[%d].field", index), dimension.Field.SourceAlias, dimension.Field.FieldKey, "time_bucket")
		}
	}
	for index, analysis := range dataset.Analyses {
		path := fmt.Sprintf("dataset.analyses[%d]", index)
		require(path+".entity_field", analysis.EntityField.SourceAlias, analysis.EntityField.FieldKey, "analysis_entity")
		require(path+".time_field", analysis.TimeField.SourceAlias, analysis.TimeField.FieldKey, "analysis_time")
		if analysis.EventField != nil {
			require(path+".event_field", analysis.EventField.SourceAlias, analysis.EventField.FieldKey, "analysis_event")
		}
	}
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left := diagnostics[i].Path + "\x00" + diagnostics[i].ObjectKey + "\x00" + diagnostics[i].FieldKey
		right := diagnostics[j].Path + "\x00" + diagnostics[j].ObjectKey + "\x00" + diagnostics[j].FieldKey
		return left < right
	})
	return diagnostics
}

func reportDatasetObjectField(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func reportDatasetFieldIndexed(field definitionmodel.FieldSchema) bool {
	if field.Unique {
		return true
	}
	raw, exists := field.Config["indexed"]
	if !exists {
		return strings.TrimSpace(field.Type) == "relation"
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		value = strings.TrimSpace(value)
		return strings.EqualFold(value, "true") || value == "1"
	default:
		return false
	}
}
