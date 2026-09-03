package record

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const recordExportBatchSize = 200
const recordExportMaxBytes = 32 << 20
const recordExportDirectMaxRows = 1_000

type RecordExportOptions struct {
	Fields         []string
	Reason         string
	MaskingPolicy  string
	FilterSummary  string
	Query          recordmodel.RecordListQuery
	AssuranceToken string `json:"-"`
}

type RecordExportDependencies struct {
	Repository           recordrepository.RecordRepository
	Objects              func() map[string]definitionmodel.ObjectSchema
	EnsureSnapshotAccess func(definitionmodel.ObjectSchema, string, principalmodel.Principal) error
	NormalizeQuery       func(definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery
	ListRecords          func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	ListDirectoryUsers   func(context.Context) ([]identitysdk.User, error)
	RecordDisplay        func(definitionmodel.ObjectSchema, recordmodel.Record) (string, string)
	ProjectRecords       func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, []recordmodel.Record, string) ([]recordmodel.Record, error)
	ValidateAssurance    func(context.Context, definitionmodel.ObjectSchema, principalmodel.Principal, map[string]any, string) (map[string]string, error)
	Audit                func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
}

// RecordExportApplicationService coordinates authorization, CSV generation, and audit for Record exports.
type RecordExportApplicationService struct {
	dependencies RecordExportDependencies
}

type recordExportPrepared struct {
	object    definitionmodel.ObjectSchema
	fields    []definitionmodel.FieldSchema
	evidence  map[string]string
	options   RecordExportOptions
	principal principalmodel.Principal
}

type recordExportEncodedPage struct {
	content []byte
	rows    int
	hasNext bool
	next    string
}

func NewRecordExportApplicationService(dependencies RecordExportDependencies) *RecordExportApplicationService {
	return &RecordExportApplicationService{dependencies: dependencies}
}

func (s *RecordExportApplicationService) prepareDispatch(ctx context.Context, objectKey string, principal principalmodel.Principal, options RecordExportOptions) (recordExportPrepared, int, error) {
	object, fields, evidence, err := s.prepareExport(ctx, objectKey, principal, options, nil, false)
	if err != nil {
		return recordExportPrepared{}, 0, err
	}
	prepared := recordExportPrepared{object: object, fields: fields, evidence: evidence, options: options, principal: principal}
	total, err := s.countPrepared(ctx, prepared)
	if err != nil {
		return recordExportPrepared{}, 0, err
	}
	return prepared, total, nil
}

func (s *RecordExportApplicationService) countPrepared(ctx context.Context, prepared recordExportPrepared) (int, error) {
	query := prepared.options.Query
	query.Page, query.PageSize = 1, 1
	query.SkipTotal, query.AfterID = false, ""
	if s.dependencies.NormalizeQuery != nil {
		query = s.dependencies.NormalizeQuery(prepared.object, query, prepared.principal)
	}
	page, err := s.dependencies.Repository.ListRecords(ctx, prepared.principal.WorkspaceID, prepared.object, query)
	if err != nil {
		return 0, recordExportInternalError("count export records", err)
	}
	return page.Total, nil
}

func (s *RecordExportApplicationService) exportPrepared(ctx context.Context, prepared recordExportPrepared, maxRows int) ([]byte, string, error) {
	objectKey := prepared.object.Key
	var buffer bytes.Buffer
	bounded := dataexchange.BoundedWriter{Writer: &buffer, Limit: recordExportMaxBytes}
	exportedRecords := 0
	cursor := ""
	for {
		page, err := s.encodeExportPage(ctx, prepared, cursor, exportedRecords == 0, recordExportMaxBytes)
		if err != nil {
			return nil, "", err
		}
		if exportedRecords+page.rows > maxRows {
			return nil, "", recordExportError(apperror.KindBadRequest, "backend.export.too_many_records", nil, "limit", fmt.Sprint(maxRows))
		}
		if _, err := bounded.Write(page.content); err != nil {
			return nil, "", recordExportError(apperror.KindBadRequest, "backend.export.output_too_large", err)
		}
		exportedRecords += page.rows
		if !page.hasNext {
			break
		}
		if strings.TrimSpace(page.next) == "" || page.next == cursor {
			return nil, "", recordExportInternalError("advance export cursor", fmt.Errorf("record export cursor did not advance"))
		}
		cursor = page.next
	}
	maskedFields := recordpolicy.RecordExportMaskedFieldKeysForPrincipal(prepared.principal, prepared.object)
	contentDigest := sha256.Sum256(buffer.Bytes())
	s.audit(ctx, "record_exported", objectKey, prepared.principal, fmt.Sprintf("Exported %s records", objectKey), nil, nil, map[string]any{
		"fields": len(prepared.fields), "exported_fields": recordpolicy.RecordExportFieldKeys(prepared.fields), "masked_fields": maskedFields, "masked_field_count": len(maskedFields),
		"record_count":  exportedRecords,
		"export_reason": strings.TrimSpace(prepared.options.Reason), "masking_policy": strings.TrimSpace(prepared.options.MaskingPolicy), "filter_summary": strings.TrimSpace(prepared.options.FilterSummary),
		"download_status": "ready", "download_sha256": hex.EncodeToString(contentDigest[:]), "download_bytes": int(bounded.Bytes),
		"assurance_grant_id": prepared.evidence["grant_id"], "assurance_methods": prepared.evidence["methods"], "assurance_payload_digest": prepared.evidence["payload_digest"],
	})
	return buffer.Bytes(), objectKey + ".csv", nil
}

// encodeExportPage is the direct-delivery encoder. Data Exchange consumes
// projectExportPage directly so Runtime never encodes CSV only to decode it
// back into the SDK page contract.
func (s *RecordExportApplicationService) encodeExportPage(ctx context.Context, prepared recordExportPrepared, cursor string, includeHeader bool, maxBytes int64) (recordExportEncodedPage, error) {
	page, err := s.projectExportPage(ctx, prepared, cursor)
	if err != nil {
		return recordExportEncodedPage{}, err
	}
	var output bytes.Buffer
	encoder := dataexchange.NewCSVEncoder(&output, maxBytes)
	if includeHeader {
		if err := encoder.Write(page.columns); err != nil {
			return recordExportEncodedPage{}, err
		}
	}
	for _, row := range page.rows {
		if err := encoder.Write(row); err != nil {
			return recordExportEncodedPage{}, err
		}
	}
	if err := encoder.Close(); err != nil {
		return recordExportEncodedPage{}, recordExportError(apperror.KindBadRequest, "backend.export.output_too_large", err)
	}
	return recordExportEncodedPage{content: output.Bytes(), rows: len(page.rows), hasNext: page.hasNext, next: page.next}, nil
}

func recordExportHeader(fields []definitionmodel.FieldSchema) []string {
	header := []string{"id", "created_at", "updated_at"}
	for _, field := range fields {
		header = append(header, field.Key)
		if field.Type == "relation" {
			header = append(header, field.Key+"__display")
		}
	}
	return header
}

func (s *RecordExportApplicationService) prepareExport(ctx context.Context, objectKey string, principal principalmodel.Principal, options RecordExportOptions, evidence map[string]string, preverified bool) (definitionmodel.ObjectSchema, []definitionmodel.FieldSchema, map[string]string, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return definitionmodel.ObjectSchema{}, nil, nil, err
	}
	objectKey = strings.TrimSpace(objectKey)
	object, found := s.objects()[objectKey]
	if !found {
		return definitionmodel.ObjectSchema{}, nil, nil, recordExportError(apperror.KindNotFound, "backend.object.not_found", nil)
	}
	// recordAuthorizeQuery already rejects principals without a resolved workspace identity.
	if !recordpolicy.RecordAllowsObjectAction(principal, objectKey, "export") {
		s.audit(ctx, "record_export_denied", objectKey, principal, "Export permission denied", nil, nil, nil)
		return definitionmodel.ObjectSchema{}, nil, nil, recordExportError(apperror.KindForbidden, "backend.permission.denied", nil)
	}
	if s.dependencies.EnsureSnapshotAccess != nil {
		if err := s.dependencies.EnsureSnapshotAccess(object, "export", principal); err != nil {
			s.audit(ctx, "record_export_denied", objectKey, principal, "Report export permission denied", nil, map[string]any{"reason": err.Error()}, nil)
			return definitionmodel.ObjectSchema{}, nil, nil, err
		}
	}
	fields := recordpolicy.RecordExportableFieldsForPrincipal(principal, object)
	if len(fields) == 0 {
		s.audit(ctx, "record_export_denied", objectKey, principal, "No exportable fields", nil, nil, nil)
		return definitionmodel.ObjectSchema{}, nil, nil, recordExportError(apperror.KindForbidden, "backend.export.no_fields", nil)
	}
	fields = restrictExportFields(fields, options.Fields)
	if len(fields) == 0 {
		s.audit(ctx, "record_export_denied", objectKey, principal, "No requested export fields are allowed", nil, nil, map[string]any{"requested_fields": options.Fields})
		return definitionmodel.ObjectSchema{}, nil, nil, recordExportError(apperror.KindForbidden, "backend.export.no_fields", nil)
	}
	intent := RecordExportAssuranceIntent(options)
	if object.ExportAssurancePolicy != nil {
		if preverified {
			if len(evidence) == 0 {
				return definitionmodel.ObjectSchema{}, nil, nil, recordExportError(apperror.KindForbidden, "backend.export.assurance_required", nil)
			}
		} else {
			if s.dependencies.ValidateAssurance == nil {
				err := recordExportError(apperror.KindForbidden, "backend.export.assurance_unavailable", nil)
				s.audit(ctx, "record_export_assurance_denied", objectKey, principal, "Export assurance unavailable", nil, nil, map[string]any{"error_code": apperror.CodeOf(err), "intent": intent})
				return definitionmodel.ObjectSchema{}, nil, nil, err
			}
			var err error
			evidence, err = s.dependencies.ValidateAssurance(ctx, object, principal, intent, strings.TrimSpace(options.AssuranceToken))
			if err != nil {
				s.audit(ctx, "record_export_assurance_denied", objectKey, principal, "Export assurance denied", nil, nil, map[string]any{"error_code": apperror.CodeOf(err), "intent": intent})
				return definitionmodel.ObjectSchema{}, nil, nil, err
			}
		}
		if !preverified {
			s.audit(ctx, "record_export_assurance_succeeded", objectKey, principal, "Export assurance verified", nil, nil, map[string]any{"grant_id": evidence["grant_id"], "methods": evidence["methods"], "payload_digest": evidence["payload_digest"], "intent": intent})
		}
	}
	return object, fields, evidence, nil
}

// RecordExportAssuranceIntent binds user input into a grant; Runtime RLS/CLS is re-evaluated at execution.
func RecordExportAssuranceIntent(options RecordExportOptions) map[string]any {
	fields := make([]string, 0, len(options.Fields))
	for _, field := range options.Fields {
		fields = append(fields, strings.TrimSpace(field))
	}
	return map[string]any{
		"fields": fields, "reason": strings.TrimSpace(options.Reason), "masking_policy": strings.TrimSpace(options.MaskingPolicy),
		"filter_summary": strings.TrimSpace(options.FilterSummary), "query": options.Query,
	}
}

func recordExportFieldValue(principal principalmodel.Principal, objectKey string, field definitionmodel.FieldSchema, value any) (string, error) {
	if recordpolicy.RecordFieldExportMaskedForPrincipal(principal, objectKey, field.Key) {
		return recordpolicy.RecordMaskFieldValue(field, value), nil
	}
	if strings.TrimSpace(field.Type) != "currency" {
		return fmt.Sprint(value), nil
	}
	config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
	if err != nil {
		return "", recordExportError(apperror.KindInternal, "backend.export.decimal_config_invalid", err, "field", field.Key)
	}
	normalized, err := recordmodel.RecordNormalizeDecimal(value, config)
	if err != nil {
		return "", recordExportError(apperror.KindInternal, "backend.export.decimal_value_invalid", err, "field", field.Key)
	}
	return normalized, nil
}

func (s *RecordExportApplicationService) relationLabels(ctx context.Context, source definitionmodel.ObjectSchema, fields []definitionmodel.FieldSchema, records []recordmodel.Record, principal principalmodel.Principal) map[string]map[string]string {
	labels := map[string]map[string]string{}
	requests := map[string]map[string]bool{}
	fieldTargets := map[string]string{}
	for _, field := range fields {
		if field.Type != "relation" || recordpolicy.RecordFieldExportMaskedForPrincipal(principal, source.Key, field.Key) {
			continue
		}
		target := recordvalidation.RecordRelationTarget(field)
		if target == "" {
			continue
		}
		fieldTargets[field.Key] = target
		labels[field.Key] = map[string]string{}
		if requests[target] == nil {
			requests[target] = map[string]bool{}
		}
		for _, record := range records {
			id := strings.TrimSpace(fmt.Sprint(record.Data[field.Key]))
			if id != "" && id != "<nil>" {
				requests[target][id] = true
			}
		}
	}
	targetLabels := map[string]map[string]string{}
	for target, ids := range requests {
		targetLabels[target] = map[string]string{}
		if target == "identity_user" {
			s.identityLabels(ctx, ids, principal, targetLabels[target])
			continue
		}
		if s.dependencies.ListRecords == nil {
			continue
		}
		idValues := make([]string, 0, len(ids))
		for id := range ids {
			idValues = append(idValues, id)
		}
		sort.Strings(idValues)
		pageSize := len(idValues)
		if pageSize > 200 {
			pageSize = 200
		}
		page, err := s.dependencies.ListRecords(ctx, target, recordmodel.RecordListQuery{Page: 1, PageSize: pageSize, Filters: map[string]any{"id__in": idValues}}, principal)
		if err != nil {
			continue
		}
		targetObject := s.objects()[target]
		for _, record := range page.Items {
			label := record.ID
			if s.dependencies.RecordDisplay != nil {
				_, label = s.dependencies.RecordDisplay(targetObject, record)
			}
			targetLabels[target][record.ID] = label
		}
	}
	for fieldKey, target := range fieldTargets {
		for id, label := range targetLabels[target] {
			labels[fieldKey][id] = label
		}
	}
	return labels
}

// identityUsersListAction is owned by identity:builtin. Runtime only uses the
// exact cross-owner grant to decide whether an export may resolve other users'
// directory labels.
const identityUsersListAction = "identity.users.list"

func (s *RecordExportApplicationService) identityLabels(ctx context.Context, ids map[string]bool, principal principalmodel.Principal, labels map[string]string) {
	canRead := principal.HasExactPermission(identityUsersListAction)
	if s.dependencies.ListDirectoryUsers == nil || (!canRead && !ids[principal.UserID]) {
		return
	}
	users, err := s.dependencies.ListDirectoryUsers(ctx)
	if err != nil {
		return
	}
	for _, user := range users {
		if ids[user.ID] && (canRead || user.ID == principal.UserID) {
			label := strings.TrimSpace(user.Name)
			if label == "" {
				label = user.ID
			}
			labels[user.ID] = label
		}
	}
}

func (s *RecordExportApplicationService) objects() map[string]definitionmodel.ObjectSchema {
	if s.dependencies.Objects != nil {
		return s.dependencies.Objects()
	}
	return nil
}

func (s *RecordExportApplicationService) audit(ctx context.Context, event, objectKey string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) {
	if s.dependencies.Audit != nil {
		s.dependencies.Audit(ctx, event, objectKey, "", principal, summary, before, after, metadata)
	}
}
