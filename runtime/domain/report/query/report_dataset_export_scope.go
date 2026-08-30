package query

import (
	"sort"
	"strings"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func reportApplyRuntimeQuery(rows []reportDatasetRow, query *reportmodel.ReportDatasetRuntimeQuery) []reportDatasetRow {
	if query == nil || strings.TrimSpace(query.Value) == "" {
		return rows
	}
	expected := strings.ToLower(strings.Join(strings.Fields(query.Value), " "))
	filtered := make([]reportDatasetRow, 0, len(rows))
	for _, row := range rows {
		matched := query.Mode == "all"
		for _, predicate := range query.Predicates {
			actual, exists := reportDatasetFieldValue(row, predicate.Field)
			candidate := strings.ToLower(strings.Join(strings.Fields(reportStableValue(actual)), " "))
			predicateMatched := false
			if exists && actual != nil {
				switch predicate.Operator {
				case "contains":
					predicateMatched = strings.Contains(candidate, expected)
				case "starts_with":
					predicateMatched = strings.HasPrefix(candidate, expected)
				case "eq":
					predicateMatched = candidate == expected
				}
			}
			if query.Mode == "all" {
				matched = matched && predicateMatched
			} else {
				matched = matched || predicateMatched
			}
		}
		if matched {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func reportApplyRuntimeTags(rows []reportDatasetRow, tags *reportmodel.ReportDatasetRuntimeTags) []reportDatasetRow {
	if tags == nil || len(tags.Values) == 0 {
		return rows
	}
	requested := map[string]bool{}
	for _, tag := range tags.Values {
		requested[tag] = true
	}
	targetTags := map[string]map[string]bool{}
	for _, row := range rows {
		target, targetOK := reportDatasetFieldValue(row, tags.TargetField)
		tag, tagOK := reportDatasetFieldValue(row, tags.TagField)
		if !targetOK || !tagOK || target == nil || tag == nil {
			continue
		}
		targetKey, tagKey := reportStableValue(target), reportStableValue(tag)
		if !requested[tagKey] {
			continue
		}
		if targetTags[targetKey] == nil {
			targetTags[targetKey] = map[string]bool{}
		}
		targetTags[targetKey][tagKey] = true
	}
	eligible := map[string]bool{}
	for target, matched := range targetTags {
		if tags.Match == "any" {
			eligible[target] = len(matched) > 0
			continue
		}
		eligible[target] = len(matched) == len(requested)
	}
	filtered := make([]reportDatasetRow, 0, len(rows))
	seen := map[string]bool{}
	scopeAliases := map[string]bool{}
	for _, alias := range tags.ScopeJoinAliases {
		scopeAliases[alias] = true
	}
	for _, row := range rows {
		target, ok := reportDatasetFieldValue(row, tags.TargetField)
		if !ok || !eligible[reportStableValue(target)] {
			continue
		}
		aliases := make([]string, 0, len(row))
		for alias := range row {
			if !scopeAliases[alias] {
				aliases = append(aliases, alias)
			}
		}
		sort.Strings(aliases)
		parts := make([]string, 0, len(aliases))
		for _, alias := range aliases {
			record := row[alias]
			id := ""
			if record != nil {
				id = record.ID
			}
			parts = append(parts, alias+"="+id)
		}
		key := strings.Join(parts, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		copyRow := reportCopyDatasetRow(row)
		for alias := range scopeAliases {
			delete(copyRow, alias)
		}
		filtered = append(filtered, copyRow)
	}
	return filtered
}
