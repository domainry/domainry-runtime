package service

import (
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

// Projection after a query cannot conceal values used to choose, count or order
// its rows. External queries therefore use only fields readable in clear text
// without record-dependent policy evaluation. Action-owned reads retain their
// existing capability and record-scope authorization in ListRecordsForAction.
func recordAuthorizeReadQueryFields(principal principalmodel.Principal, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordListQuery, error) {
	queryable := func(key string) bool {
		key = strings.TrimSpace(key)
		switch key {
		case "id", "created_at", "updated_at":
			return true // These fields belong to every readable record envelope.
		}
		return recordpolicy.RecordCanReadObjectFieldKeyForPrincipal(principal, object, key) &&
			!recordpolicy.RecordFieldReadMaskedForPrincipal(principal, object.Key, key) &&
			!recordpolicy.RecordFieldRequiresPolicyEvaluation(principal, object.Key, key, "read")
	}
	deny := func(key string) error {
		return recordServiceError(apperror.KindForbidden, "backend.record.field_not_queryable", nil, "object_key", object.Key, "field", key)
	}
	keys := make([]string, 0, len(query.Filters))
	for key := range query.Filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// NormalizeListQuery has already validated these operator suffixes.
		field := key
		for _, suffix := range []string{"__in", "__gte", "__lte"} {
			if strings.HasSuffix(field, suffix) {
				field = strings.TrimSuffix(field, suffix)
				break
			}
		}
		if !queryable(field) {
			return query, deny(field)
		}
	}
	for _, rule := range query.Sort {
		if !queryable(rule.Field) {
			return query, deny(rule.Field)
		}
	}
	var checkExpression func(*recordmodel.RecordFilterExpression) error
	checkExpression = func(expression *recordmodel.RecordFilterExpression) error {
		if expression == nil {
			return nil
		}
		if expression.Field != "" && !queryable(expression.Field) {
			return deny(expression.Field)
		}
		for i := range expression.Children {
			if err := checkExpression(&expression.Children[i]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := checkExpression(query.FilterExpression); err != nil {
		return query, err
	}
	if strings.TrimSpace(query.Search) != "" {
		fields := make([]string, 0, len(query.SearchFields))
		for _, field := range query.SearchFields {
			if queryable(field) {
				fields = append(fields, field)
			}
		}
		if len(fields) == 0 {
			// An empty search projection means no predicate in the repository;
			// never turn a request that cannot be searched into a broad listing.
			return query, recordServiceError(apperror.KindForbidden, "backend.record.search_fields_unavailable", nil, "object_key", object.Key)
		}
		query.SearchFields = fields
	}
	return query, nil
}
