package reportmodulehost

import (
	"sort"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const reportQueryPageSize = 500

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
	query.SelectFields = query.SelectFields[:0]
	for field := range fields {
		query.SelectFields = append(query.SelectFields, field)
	}
	sort.Strings(query.SelectFields)
	return query
}
