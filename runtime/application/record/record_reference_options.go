package record

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

const (
	recordReferenceDefaultLimit = 20
	recordReferenceMaximumLimit = 50
)

// RecordReferenceOption is the stable projection consumed by relation input
// components. Raw records and authorization metadata never cross this boundary.
type RecordReferenceOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type RecordReferenceOptionPage struct {
	Items   []RecordReferenceOption `json:"items"`
	HasMore bool                    `json:"has_more"`
}

type RecordReferenceOptionRequest struct {
	Query          string
	Page           int
	Limit          int
	Locale         string
	FallbackLocale string
}

// ReferenceOptions resolves a source relation field to its target and returns
// an authorization-filtered id/name projection for a reusable reference input.
func (s *RecordApplicationService) ReferenceOptions(ctx context.Context, sourceObjectKey, relationFieldKey string, request RecordReferenceOptionRequest, principal principalmodel.Principal) (RecordReferenceOptionPage, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return RecordReferenceOptionPage{}, err
	}
	source, relation, target, err := s.resolveReferenceOptionContract(sourceObjectKey, relationFieldKey)
	if err != nil {
		return RecordReferenceOptionPage{}, err
	}
	displayFields, err := recordReferenceDisplayFields(principal, source, relation, target)
	if err != nil {
		return RecordReferenceOptionPage{}, err
	}
	if definitioncontract.IsFoundationObjectKey(target.Key) {
		return s.foundationReferenceOptions(ctx, target.Key, request, displayFields)
	}
	pageNumber, limit := request.Page, request.Limit
	if pageNumber <= 0 {
		pageNumber = 1
	}
	if limit <= 0 {
		limit = recordReferenceDefaultLimit
	}
	if limit > recordReferenceMaximumLimit {
		limit = recordReferenceMaximumLimit
	}
	searchFields := recordReferenceSearchFields(target, displayFields)
	if strings.TrimSpace(request.Query) != "" && len(searchFields) == 0 {
		searchFields = []string{"id"}
	}
	query := recordmodel.RecordListQuery{
		Page: pageNumber, PageSize: limit, Search: strings.TrimSpace(request.Query), SearchFields: searchFields,
		SelectFields: recordReferenceProjectionFields(displayFields), Locale: request.Locale, FallbackLocale: request.FallbackLocale,
	}
	if len(searchFields) > 0 && searchFields[0] != "id" {
		query.Sort = []recordmodel.RecordSortRule{{Field: searchFields[0], Direction: "asc"}}
	}
	page, err := s.ListRecords(ctx, target.Key, query, principal)
	if err != nil {
		return RecordReferenceOptionPage{}, err
	}
	result := RecordReferenceOptionPage{Items: make([]RecordReferenceOption, 0, len(page.Items)), HasMore: page.HasNext}
	for _, item := range page.Items {
		result.Items = append(result.Items, RecordReferenceOption{ID: item.ID, Name: recordReferenceDisplayName(item, displayFields)})
	}
	return result, nil
}

func (s *RecordApplicationService) resolveReferenceOptionContract(sourceObjectKey, relationFieldKey string) (definitionmodel.ObjectSchema, definitionmodel.FieldSchema, definitionmodel.ObjectSchema, error) {
	if s.schemaMap == nil {
		return definitionmodel.ObjectSchema{}, definitionmodel.FieldSchema{}, definitionmodel.ObjectSchema{}, apperror.New(apperror.KindInternal, "backend.reference.schema_unavailable", nil, nil)
	}
	objects := s.schemaMap()
	sourceObjectKey, relationFieldKey = strings.TrimSpace(sourceObjectKey), strings.TrimSpace(relationFieldKey)
	source, exists := objects[sourceObjectKey]
	if !exists {
		return definitionmodel.ObjectSchema{}, definitionmodel.FieldSchema{}, definitionmodel.ObjectSchema{}, apperror.New(apperror.KindNotFound, "backend.object.not_found", nil, map[string]string{"object_key": sourceObjectKey})
	}
	var relation definitionmodel.FieldSchema
	for _, field := range source.Fields {
		if strings.TrimSpace(field.Key) == relationFieldKey && strings.TrimSpace(field.DisabledAt) == "" {
			relation = field
			break
		}
	}
	if relation.Key == "" || strings.TrimSpace(relation.Type) != "relation" {
		return definitionmodel.ObjectSchema{}, definitionmodel.FieldSchema{}, definitionmodel.ObjectSchema{}, apperror.New(apperror.KindBadRequest, "backend.reference.relation_field_invalid", nil, map[string]string{"object_key": sourceObjectKey, "field_key": relationFieldKey})
	}
	targetKey := recordvalidation.RecordRelationTarget(relation)
	if targetKey == "" {
		return definitionmodel.ObjectSchema{}, definitionmodel.FieldSchema{}, definitionmodel.ObjectSchema{}, apperror.New(apperror.KindBadRequest, "backend.reference.target_missing", nil, map[string]string{"object_key": sourceObjectKey, "field_key": relationFieldKey})
	}
	target, exists := objects[targetKey]
	if !exists {
		if !definitioncontract.IsFoundationObjectKey(targetKey) {
			return definitionmodel.ObjectSchema{}, definitionmodel.FieldSchema{}, definitionmodel.ObjectSchema{}, apperror.New(apperror.KindBadRequest, "backend.reference.target_not_found", nil, map[string]string{"target_object_key": targetKey})
		}
		target = definitionmodel.ObjectSchema{Key: targetKey, Name: targetKey}
	}
	return source, relation, target, nil
}

func recordReferenceDisplayFields(principal principalmodel.Principal, source definitionmodel.ObjectSchema, relation definitionmodel.FieldSchema, target definitionmodel.ObjectSchema) ([]string, error) {
	if principal.AccessBundle == nil {
		if !principal.SystemScope.Valid() {
			return nil, apperror.New(apperror.KindForbidden, "backend.reference.permission_denied", nil, nil)
		}
		return recordReferenceDefaultDisplayFields(target), nil
	}
	for _, policy := range identityevaluator.AllowedReferences(*principal.AccessBundle, identitysdk.ResourceType(source.Key)) {
		if strings.TrimSpace(policy.Reference) != strings.TrimSpace(relation.Key) || strings.TrimSpace(string(policy.TargetResource)) != strings.TrimSpace(target.Key) {
			continue
		}
		fields := recordReferenceExistingDisplayFields(target, policy.DisplayFields)
		if len(fields) == 0 {
			fields = recordReferenceDefaultDisplayFields(target)
		}
		return fields, nil
	}
	return nil, apperror.New(apperror.KindForbidden, "backend.reference.permission_denied", nil, map[string]string{"object_key": source.Key, "field_key": relation.Key})
}

func recordReferenceDefaultDisplayFields(object definitionmodel.ObjectSchema) []string {
	if definitioncontract.IsFoundationObjectKey(object.Key) {
		return []string{"name"}
	}
	configured := ""
	if display, ok := object.UX["display"].(map[string]any); ok {
		configured = strings.TrimSpace(fmt.Sprint(display["title_field"]))
	}
	if configured == "" || configured == "<nil>" {
		configured = strings.TrimSpace(fmt.Sprint(object.Config["title_field"]))
	}
	ordered := append([]string{configured}, "name", "title", "subject", "number", "code", "display_name", "short_name")
	if fields := recordReferenceExistingDisplayFields(object, ordered); len(fields) > 0 {
		return fields[:1]
	}
	return []string{"id"}
}

func recordReferenceExistingDisplayFields(object definitionmodel.ObjectSchema, requested []string) []string {
	if definitioncontract.IsFoundationObjectKey(object.Key) {
		result := []string{}
		for _, field := range requested {
			field = strings.TrimSpace(field)
			if field == "id" || field == "name" {
				result = appendUniqueReferenceField(result, field)
			}
		}
		return result
	}
	fields := map[string]bool{"id": true}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.DisabledAt) == "" {
			fields[field.Key] = true
		}
	}
	result := []string{}
	for _, field := range requested {
		field = strings.TrimSpace(field)
		if fields[field] {
			result = appendUniqueReferenceField(result, field)
		}
	}
	return result
}

func appendUniqueReferenceField(fields []string, field string) []string {
	for _, existing := range fields {
		if existing == field {
			return fields
		}
	}
	return append(fields, field)
}

func recordReferenceSearchFields(object definitionmodel.ObjectSchema, displayFields []string) []string {
	searchable := map[string]bool{}
	for _, field := range object.Fields {
		switch strings.TrimSpace(field.Type) {
		case "text", "long_text", "email", "phone", "url", "select", "user":
			searchable[field.Key] = true
		}
	}
	result := []string{}
	for _, field := range displayFields {
		if searchable[field] {
			result = appendUniqueReferenceField(result, field)
		}
	}
	return result
}

func recordReferenceProjectionFields(displayFields []string) []string {
	result := []string{}
	for _, field := range displayFields {
		if field != "id" {
			result = appendUniqueReferenceField(result, field)
		}
	}
	return result
}

func recordReferenceDisplayName(record recordmodel.Record, displayFields []string) string {
	for _, field := range displayFields {
		if field == "id" {
			return record.ID
		}
		if value := strings.TrimSpace(fmt.Sprint(record.Data[field])); value != "" && value != "<nil>" {
			return value
		}
	}
	return record.ID
}

func (s *RecordApplicationService) foundationReferenceOptions(ctx context.Context, targetObjectKey string, request RecordReferenceOptionRequest, displayFields []string) (RecordReferenceOptionPage, error) {
	if targetObjectKey != "identity_user" || s.IdentityProjection() == nil {
		return RecordReferenceOptionPage{}, apperror.New(apperror.KindUnavailable, "backend.reference.provider_unavailable", nil, map[string]string{"target_object_key": targetObjectKey})
	}
	users, err := s.IdentityProjection().ListUsers(ctx, identitysdk.ProjectionQuery{})
	if err != nil {
		return RecordReferenceOptionPage{}, apperror.New(apperror.KindInternal, "backend.reference.lookup_failed", err, map[string]string{"target_object_key": targetObjectKey})
	}
	needle := strings.ToLower(strings.TrimSpace(request.Query))
	limit := request.Limit
	if limit <= 0 {
		limit = recordReferenceDefaultLimit
	}
	if limit > recordReferenceMaximumLimit {
		limit = recordReferenceMaximumLimit
	}
	page := request.Page
	if page <= 0 {
		page = 1
	}
	matched := make([]RecordReferenceOption, 0, len(users))
	for _, user := range users {
		name := strings.TrimSpace(user.Name)
		if name == "" {
			name = user.ID
		}
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		matched = append(matched, RecordReferenceOption{ID: user.ID, Name: name})
	}
	_ = displayFields
	start := (page - 1) * limit
	if start >= len(matched) {
		return RecordReferenceOptionPage{Items: []RecordReferenceOption{}}, nil
	}
	end := start + limit
	if end > len(matched) {
		end = len(matched)
	}
	return RecordReferenceOptionPage{Items: matched[start:end], HasMore: end < len(matched)}, nil
}
