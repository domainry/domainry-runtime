package records

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
)

func parseListQuery(r *http.Request) recordmodel.RecordListQuery {
	values := r.URL.Query()
	query := recordmodel.RecordListQuery{
		Page:           intQuery(values.Get("page")),
		PageSize:       intQuery(values.Get("page_size")),
		AfterID:        strings.TrimSpace(values.Get("after_id")),
		Search:         strings.TrimSpace(values.Get("search")),
		Filters:        map[string]any{},
		Locale:         recordRequestLocale(r),
		FallbackLocale: recordFallbackLocale(r),
	}
	if filters := strings.TrimSpace(values.Get("filters")); filters != "" {
		_ = json.Unmarshal([]byte(filters), &query.Filters)
	}
	if sortValue := strings.TrimSpace(values.Get("sort")); sortValue != "" {
		query.Sort = parseSortQuery(sortValue)
	}
	return query
}

func recordFallbackLocale(r *http.Request) string {
	if locale := supportedRecordLocale(r.URL.Query().Get("fallback_locale")); locale != "" {
		return locale
	}
	return localization.DefaultLocale
}

func recordRequestLocale(r *http.Request) string {
	for _, raw := range []string{r.URL.Query().Get("locale"), r.Header.Get("X-Locale"), firstRecordAcceptLanguage(r.Header.Get("Accept-Language"))} {
		if locale := supportedRecordLocale(raw); locale != "" {
			return locale
		}
	}
	return localization.DefaultLocale
}

func firstRecordAcceptLanguage(value string) string {
	first := strings.TrimSpace(strings.Split(value, ",")[0])
	return strings.TrimSpace(strings.Split(first, ";")[0])
}

func supportedRecordLocale(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	value = localization.NormalizeLocale(value)
	for _, supported := range localization.SupportedLocales() {
		if value == supported {
			return value
		}
	}
	return localization.DefaultLocale
}

func intQuery(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func parseSortQuery(value string) []recordmodel.RecordSortRule {
	rules := []recordmodel.RecordSortRule{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		field := item
		direction := "asc"
		if strings.Contains(item, ":") {
			parts := strings.SplitN(item, ":", 2)
			field = strings.TrimSpace(parts[0])
			direction = strings.TrimSpace(parts[1])
		} else {
			parts := strings.Fields(item)
			field = strings.TrimSpace(strings.TrimPrefix(parts[0], "-"))
			if strings.HasPrefix(parts[0], "-") || (len(parts) > 1 && strings.EqualFold(parts[1], "desc")) {
				direction = "desc"
			}
		}
		rules = append(rules, recordmodel.RecordSortRule{Field: field, Direction: direction})
	}
	return rules
}

func splitQueryCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
