package service

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordReferenceGroup struct {
	ObjectKey  string `json:"object_key"`
	ObjectName string `json:"object_name,omitempty"`
	FieldKey   string `json:"field_key"`
	FieldName  string `json:"field_name,omitempty"`
	Count      int    `json:"count"`
}

type RecordReferenceSummary struct {
	ObjectKey string                 `json:"object_key"`
	RecordID  string                 `json:"record_id"`
	Total     int                    `json:"total"`
	Groups    []RecordReferenceGroup `json:"groups"`
}

type RecordRelatedRecordsRequest struct {
	Page           int
	PageSize       int
	FieldKey       string
	Locale         string
	FallbackLocale string
}

type RecordReferenceDependencies struct {
	Repository      recordrepository.RecordRepository
	Objects         func() map[string]definitionmodel.ObjectSchema
	ObjectForAction func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	CanAccess       func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	ListRecords     func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
}

// RecordReferenceDomainService resolves record references.
type RecordReferenceDomainService struct {
	dependencies RecordReferenceDependencies
}

func NewRecordReferenceDomainService(dependencies RecordReferenceDependencies) *RecordReferenceDomainService {
	return &RecordReferenceDomainService{dependencies: dependencies}
}

// References returns reverse-relation aggregates without exposing the
// referenced records themselves.
func (s *RecordReferenceDomainService) References(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (RecordReferenceSummary, error) {
	object, err := s.objectForAction(principal, objectKey, "read")
	if err != nil {
		return RecordReferenceSummary{}, err
	}
	recordID = strings.TrimSpace(recordID)
	record, found, err := s.accessibleRecord(ctx, object, recordID, principal)
	if err != nil {
		return RecordReferenceSummary{}, recordInternalError("get record", err)
	}
	if !found {
		return RecordReferenceSummary{}, recordStateMachineError(apperror.KindNotFound, "backend.record.not_found")
	}
	_ = record

	summary := RecordReferenceSummary{ObjectKey: object.Key, RecordID: recordID, Groups: []RecordReferenceGroup{}}
	schema := s.objects()
	sourceKeys := make([]string, 0, len(schema))
	for key := range schema {
		sourceKeys = append(sourceKeys, key)
	}
	sort.Strings(sourceKeys)
	for _, sourceKey := range sourceKeys {
		source := schema[sourceKey]
		for _, field := range source.Fields {
			if field.Type != "relation" || recordvalidation.RecordRelationTarget(field) != object.Key {
				continue
			}
			count, err := s.count(ctx, principal, source, field.Key, recordID)
			if err != nil {
				return RecordReferenceSummary{}, err
			}
			if count == 0 {
				continue
			}
			summary.Groups = append(summary.Groups, RecordReferenceGroup{
				ObjectKey: source.Key, ObjectName: source.Name,
				FieldKey: field.Key, FieldName: field.Name, Count: count,
			})
			summary.Total += count
		}
	}
	return summary, nil
}

func (s *RecordReferenceDomainService) Related(ctx context.Context, objectKey, recordID, relatedObjectKey string, request RecordRelatedRecordsRequest, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	object, err := s.objectForAction(principal, objectKey, "read")
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	recordID = strings.TrimSpace(recordID)
	record, found, err := s.accessibleRecord(ctx, object, recordID, principal)
	if err != nil {
		return recordmodel.RecordPageResult{}, recordInternalError("get related parent record", err)
	}
	if !found {
		return recordmodel.RecordPageResult{}, recordStateMachineError(apperror.KindNotFound, "backend.record.not_found")
	}
	_ = record
	related, err := s.objectForAction(principal, relatedObjectKey, "read")
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	fieldKey, err := RecordRelatedRelationField(object.Key, related, request.FieldKey)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	// accessibleRecord above already requires the same list dependency.
	return s.dependencies.ListRecords(ctx, related.Key, recordmodel.RecordListQuery{
		Page: request.Page, PageSize: request.PageSize,
		Filters: map[string]any{fieldKey: recordID},
		Locale:  request.Locale, FallbackLocale: request.FallbackLocale,
	}, principal)
}

func (s *RecordReferenceDomainService) count(ctx context.Context, principal principalmodel.Principal, source definitionmodel.ObjectSchema, fieldKey, recordID string) (int, error) {
	if s.dependencies.ListRecords == nil {
		return 0, recordInternalError("count relation references", fmt.Errorf("record list dependency is unavailable"))
	}
	count := 0
	for page := 1; ; page++ {
		result, err := s.dependencies.ListRecords(ctx, source.Key, recordmodel.RecordListQuery{Page: page, PageSize: 200, Filters: map[string]any{fieldKey: recordID}}, principal)
		if err != nil {
			return 0, recordInternalError("count relation references", err)
		}
		count += len(result.Items)
		if !result.HasNext {
			return count, nil
		}
	}
}

func (s *RecordReferenceDomainService) accessibleRecord(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, principal principalmodel.Principal) (recordmodel.Record, bool, error) {
	if s.dependencies.ListRecords == nil {
		return recordmodel.Record{}, false, recordInternalError("resolve accessible record", fmt.Errorf("record list dependency is unavailable"))
	}
	page, err := s.dependencies.ListRecords(ctx, object.Key, recordmodel.RecordListQuery{Page: 1, PageSize: 1, Filters: map[string]any{"id__in": []any{recordID}}}, principal)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	if len(page.Items) == 0 {
		return recordmodel.Record{}, false, nil
	}
	return page.Items[0], true, nil
}

func (s *RecordReferenceDomainService) objectForAction(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	if s.dependencies.ObjectForAction == nil {
		return definitionmodel.ObjectSchema{}, recordInternalError("resolve record object", fmt.Errorf("record object policy dependency is unavailable"))
	}
	return s.dependencies.ObjectForAction(principal, objectKey, action)
}

func (s *RecordReferenceDomainService) objects() map[string]definitionmodel.ObjectSchema {
	if s.dependencies.Objects == nil {
		return nil
	}
	return s.dependencies.Objects()
}

func RecordRelatedRelationField(parentObjectKey string, related definitionmodel.ObjectSchema, requestedField string) (string, error) {
	requestedField = strings.TrimSpace(requestedField)
	candidates := []string{}
	for _, field := range related.Fields {
		if field.Type != "relation" {
			continue
		}
		if requestedField != "" && field.Key != requestedField {
			continue
		}
		if relationTarget(field) == parentObjectKey {
			candidates = append(candidates, field.Key)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if requestedField != "" {
		return "", recordBadRequest("backend.related.field_not_found", "field", requestedField, "object", related.Key)
	}
	if len(candidates) == 0 {
		return "", recordBadRequest("backend.related.relation_not_found", "object", related.Key, "target", parentObjectKey)
	}
	return "", recordBadRequest("backend.related.field_required", "object", related.Key, "target", parentObjectKey)
}

func relationTarget(field definitionmodel.FieldSchema) string {
	if value, ok := field.Config["object_key"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, ok := field.Config["target"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(field.Validation.Target)
}

func recordBadRequest(code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: values}
}
