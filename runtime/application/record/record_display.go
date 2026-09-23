package record

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func recordLocalizationAuditMetadata(metadata map[string]any, values []recordmodel.RecordLocalizedValueMutation) map[string]any {
	if len(values) == 0 {
		return metadata
	}
	result := make(map[string]any, len(metadata)+3)
	for key, value := range metadata {
		result[key] = value
	}
	fields := map[string]bool{}
	locales := map[string]bool{}
	for _, value := range values {
		fields[value.FieldKey] = true
		locales[value.Locale] = true
	}
	fieldKeys := make([]string, 0, len(fields))
	for field := range fields {
		fieldKeys = append(fieldKeys, field)
	}
	localeKeys := make([]string, 0, len(locales))
	for locale := range locales {
		localeKeys = append(localeKeys, locale)
	}
	sort.Strings(fieldKeys)
	sort.Strings(localeKeys)
	result["localized_fields"] = fieldKeys
	result["localized_locales"] = localeKeys
	result["localized_value_count"] = len(values)
	return result
}

func (s *RecordApplicationService) GetRecordLocalized(ctx context.Context, objectKey, recordID, locale, fallbackLocale string, principal principalmodel.Principal) (recordmodel.Record, error) {
	record, err := s.GetRecord(ctx, objectKey, recordID, principal)
	if err != nil || strings.TrimSpace(locale) == "" {
		return record, err
	}
	object, err := s.queryPolicy.ObjectForAction(principal, objectKey, "read")
	if err != nil {
		return recordmodel.Record{}, err
	}
	localizedFields := recordmodel.RecordLocalizedFieldKeys(object)
	if len(localizedFields) == 0 {
		return record, nil
	}
	repository, ok := s.RecordDomainService.Repository().(recordrepository.RecordLocalizationRepository)
	if !ok {
		return recordmodel.Record{}, apperror.New(apperror.KindInternal, "backend.record.localization_store_unavailable", nil, nil)
	}
	fields := []string{}
	for _, field := range localizedFields {
		if _, readable := record.Data[field]; readable {
			fields = append(fields, field)
		}
	}
	values, err := repository.ListRecordLocalizedValues(
		ctx,
		principal.WorkspaceID,
		object,
		[]string{record.ID},
		fields,
		recordmodel.RecordLocalizationLocales(locale, fallbackLocale),
	)
	if err != nil {
		return recordmodel.Record{}, apperror.New(apperror.KindInternal, "backend.record.localization_lookup_failed", err, nil)
	}
	lookup := map[string]string{}
	for _, value := range values {
		lookup[value.FieldKey+"\x00"+value.Locale] = value.TextValue
	}
	resolution := &recordmodel.RecordLocalizationResolution{RequestedLocale: locale, FallbackLocale: fallbackLocale, FieldSources: map[string]string{}}
	for _, field := range fields {
		resolution.FieldSources[field] = "base"
		if value, exists := lookup[field+"\x00"+locale]; exists {
			record.Data[field] = value
			resolution.FieldSources[field] = locale
			continue
		}
		if fallbackLocale != "" && fallbackLocale != locale {
			if value, exists := lookup[field+"\x00"+fallbackLocale]; exists {
				record.Data[field] = value
				resolution.FieldSources[field] = fallbackLocale
			}
		}
	}
	record.Localization = resolution
	return record, nil
}

func (s *RecordApplicationService) NormalizeListQuery(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	return s.normalizeListQuery(object, query, principal)
}

func (s *RecordApplicationService) NormalizeListQueryForAction(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal, action string) recordmodel.RecordListQuery {
	return s.queryPolicy.NormalizeListQueryForAction(object, query, principal, action)
}

func recordExportInternalError(operation string, err error) error {
	return recordExportError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func recordAppendUpdateDeniedAudit(ctx context.Context, audit func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any), objectKey, recordID string, principal principalmodel.Principal, err error, reason string, patch map[string]any) {
	if audit == nil {
		return
	}
	keys := make([]string, 0, len(patch))
	for key := range patch {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	audit(ctx, "record_update_denied", objectKey, recordID, principal, "Update denied for "+objectKey+" record", nil, nil, map[string]any{
		"decision": "denied", "operation": "update", "reason": strings.TrimSpace(reason),
		"error_code": apperror.CodeOf(err), "attempted_keys": keys,
	})
}

func recordExportError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}

func restrictExportFields(fields []definitionmodel.FieldSchema, requested []string) []definitionmodel.FieldSchema {
	requestedSet := map[string]bool{}
	for _, field := range requested {
		if field = strings.TrimSpace(field); field != "" {
			requestedSet[field] = true
		}
	}
	if len(requestedSet) == 0 {
		return fields
	}
	allowed := make([]definitionmodel.FieldSchema, 0, len(fields))
	for _, field := range fields {
		if requestedSet[field.Key] {
			allowed = append(allowed, field)
		}
	}
	return allowed
}

func recordMutationPlanApplicationError(err error) error {
	if err == nil {
		return nil
	}
	var planner *recordmutation.MutationPlannerError
	if errors.As(err, &planner) {
		switch planner.Code {
		case "backend.mutation.action_required":
			return apperror.New(apperror.KindForbidden, planner.Code, err, map[string]string{"object": planner.Field})
		case "backend.mutation.predicate_invalid":
			return apperror.New(apperror.KindBadRequest, planner.Code, err, map[string]string{"field": planner.Field})
		default:
			return apperror.New(apperror.KindInternal, planner.Code, err, nil)
		}
	}
	var plan *transactionmodel.MutationPlanError
	if errors.As(err, &plan) {
		kind := apperror.KindBadRequest
		if plan.Code == "backend.mutation.effect_authority_denied" {
			kind = apperror.KindForbidden
		}
		return apperror.New(kind, plan.Code, err, map[string]string{"field": plan.Field})
	}
	return err
}

func (s *RecordApplicationService) CanAccessRecord(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	if recordAuthorizeQuery(principal) != nil {
		return false
	}
	return s.canAccessRecord(principal, object, record)
}

// RecordScopeAllows resolves one record against the same RLS policy used by
// Action execution without requiring a broad object.read grant. Callers must
// still decide the dedicated Action and object data permissions separately.
func (s *RecordApplicationService) RecordScopeAllows(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (bool, error) {
	return s.RecordScopeAllowsAction(ctx, objectKey, recordID, "read", principal)
}

func (s *RecordApplicationService) RecordScopeAllowsAction(ctx context.Context, objectKey, recordID, action string, principal principalmodel.Principal) (bool, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return false, err
	}
	objectKey = strings.TrimSpace(objectKey)
	recordID = strings.TrimSpace(recordID)
	if objectKey == "" || recordID == "" {
		return false, apperror.New(apperror.KindBadRequest, "backend.permissions.record_context_required", nil, nil)
	}
	if s.schemaMap == nil {
		return false, apperror.New(apperror.KindInternal, "backend.permissions.schema_unavailable", nil, nil)
	}
	object, exists := s.schemaMap()[objectKey]
	if !exists {
		return false, apperror.New(apperror.KindNotFound, "backend.object.not_found", nil, map[string]string{"object_key": objectKey})
	}
	if s.RecordDomainService == nil || s.RecordDomainService.Repository() == nil {
		return false, apperror.New(apperror.KindInternal, "backend.permissions.record_store_unavailable", nil, nil)
	}
	if _, err := s.queryPolicy.ObjectForAction(principal, objectKey, action); err != nil {
		return false, nil
	}
	query := s.queryPolicy.NormalizeListQueryForAction(object, recordmodel.RecordListQuery{
		Page: 1, PageSize: 1, SkipTotal: true,
		Filters: map[string]any{"id__in": []any{recordID}},
	}, principal, action)
	page, err := s.RecordDomainService.Repository().ListRecords(ctx, principal.WorkspaceID, object, query)
	if err != nil {
		return false, apperror.New(apperror.KindInternal, "backend.permissions.record_lookup_failed", err, nil)
	}
	if len(page.Items) == 0 {
		return false, nil
	}
	return true, nil
}

func (s *RecordApplicationService) ListRecords(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	page, err := s.RecordDomainService.ListRecords(ctx, objectKey, query, principal)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	s.resolveRecordSystemDisplayNames(ctx, page.Items)
	return page, nil
}

func (s *RecordApplicationService) GetRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.Record{}, err
	}
	return s.RecordDomainService.GetRecord(ctx, objectKey, recordID, principal)
}

func (s *RecordApplicationService) GetRecordForUpdate(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.Record{}, err
	}
	return s.RecordDomainService.GetRecordForUpdate(ctx, objectKey, recordID, principal)
}

func (s *RecordApplicationService) ListRecordsForAction(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	return s.RecordDomainService.ListRecordsForAction(ctx, objectKey, query, principal)
}

func (s *RecordApplicationService) GetRecordForAction(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.Record{}, err
	}
	return s.RecordDomainService.GetRecordForAction(ctx, objectKey, recordID, principal)
}

func (s *RecordApplicationService) GetRecordForUpdateForAction(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.Record{}, err
	}
	return s.RecordDomainService.GetRecordForUpdateForAction(ctx, objectKey, recordID, principal)
}

func (s *RecordApplicationService) ProjectRecordFields(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, records []recordmodel.Record, action string) ([]recordmodel.Record, error) {
	if s.contextualFieldPolicy == nil {
		return append([]recordmodel.Record(nil), records...), nil
	}
	projected, denials, err := s.contextualFieldPolicy.ApplyReadPage(ctx, principal, object, records, action)
	if err != nil {
		return nil, err
	}
	if s.audit != nil {
		for _, record := range records {
			recordDenials := denials[record.ID]
			if len(recordDenials) == 0 {
				continue
			}
			fields := make([]string, 0, len(recordDenials))
			rules := make([]string, 0, len(recordDenials))
			for _, denial := range recordDenials {
				fields = append(fields, denial.FieldKey)
				rules = append(rules, denial.RuleKey)
			}
			s.audit(ctx, "field_access_denied", object.Key, record.ID, principal, "Contextual field access denied", nil, nil, map[string]any{"action": action, "fields": fields, "policy_rules": rules, "result": "denied", "reason": "field_policy_denied"})
		}
	}
	return projected, nil
}

func (s *RecordApplicationService) RecordReferences(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordservice.RecordReferenceSummary, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordservice.RecordReferenceSummary{}, err
	}
	return s.RecordDomainService.RecordReferences(ctx, objectKey, recordID, principal)
}

func (s *RecordApplicationService) RelatedRecords(ctx context.Context, objectKey, recordID, relatedObjectKey string, request recordservice.RecordRelatedRecordsRequest, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	page, err := s.RecordDomainService.RelatedRecords(ctx, objectKey, recordID, relatedObjectKey, request, principal)
	if err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	s.resolveRecordSystemDisplayNames(ctx, page.Items)
	return page, nil
}

func (s *RecordApplicationService) IdentityProfileReferences(ctx context.Context, userID string, principal principalmodel.Principal) ([]recordservice.RecordIdentityProfileReference, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	return s.RecordDomainService.IdentityProfileReferences(ctx, userID, principal)
}

func recordApplicationDisplay(object definitionmodel.ObjectSchema, record recordmodel.Record) (string, string) {
	for _, key := range []string{"name", "title", "subject", "number", "code", "display_name", "short_name"} {
		if value := strings.TrimSpace(fmt.Sprint(record.Data[key])); value != "" && value != "<nil>" {
			return key, value
		}
	}
	return "id", record.ID
}
