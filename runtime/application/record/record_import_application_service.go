package record

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/logging"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const recordImportMaxBytes = 2 << 20
const recordImportMaxRows = 1000
const recordImportMaxColumns = 128
const recordImportBatchSize = 50

type RecordImportDependencies struct {
	Repository        recordrepository.RecordRepository
	ObjectForAction   func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	CanWrite          func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool
	ValidateRelations func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error
	CreateRecord      func(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error)
	CreateIdempotent  func(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, bool, error)
	Execution         *recordruntime.RecordMutationExecutionRuntime
	Audit             func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
}

// RecordImportApplicationService coordinates preview, creation, and audit for Record imports.
type RecordImportApplicationService struct {
	dependencies RecordImportDependencies
}

func NewRecordImportApplicationService(dependencies RecordImportDependencies) *RecordImportApplicationService {
	return &RecordImportApplicationService{dependencies: dependencies}
}

func (s *RecordImportApplicationService) Preview(ctx context.Context, objectKey string, rawCSV []byte, principal principalmodel.Principal) (recordmodel.RecordImportPreview, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordImportPreview{}, err
	}
	object, err := s.dependencies.ObjectForAction(principal, objectKey, "import")
	if err != nil {
		s.audit(ctx, "record_import_denied", objectKey, "", principal, "Import preview denied", nil, nil, nil)
		return recordmodel.RecordImportPreview{}, err
	}
	preview, err := s.buildPreview(ctx, object, rawCSV, principal)
	if err != nil {
		return recordmodel.RecordImportPreview{}, err
	}
	s.audit(ctx, "record_import_previewed", objectKey, "", principal, fmt.Sprintf("Previewed %d %s rows", len(preview.Rows), objectKey), nil, map[string]any{"rows": len(preview.Rows), "invalid_rows": preview.InvalidRows}, nil)
	return preview, nil
}

func (s *RecordImportApplicationService) Apply(ctx context.Context, objectKey string, rawCSV []byte, principal principalmodel.Principal) (recordmodel.RecordImportApplyResult, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.RecordImportApplyResult{}, err
	}
	object, err := s.dependencies.ObjectForAction(principal, objectKey, "import")
	if err != nil {
		s.audit(ctx, "record_import_denied", objectKey, "", principal, "Import apply denied", nil, nil, nil)
		return recordmodel.RecordImportApplyResult{}, err
	}
	preview, err := s.buildPreview(ctx, object, rawCSV, principal)
	if err != nil {
		return recordmodel.RecordImportApplyResult{}, err
	}
	if !preview.CanApply {
		s.audit(ctx, "record_import_rejected", objectKey, "", principal, "Import apply rejected by preview validation", nil, map[string]any{"invalid_rows": preview.InvalidRows, "duplicate_rows": preview.DuplicateRows}, nil)
		return recordmodel.RecordImportApplyResult{}, recordImportError(apperror.KindBadRequest, "backend.import.invalid_rows", nil)
	}
	created := 0
	for index, row := range preview.Rows {
		if err := ctx.Err(); err != nil {
			return recordmodel.RecordImportApplyResult{}, err
		}
		if _, err := s.dependencies.CreateRecord(ctx, object.Key, recordvalidation.RecordCloneData(row.Data), principal); err != nil {
			return recordmodel.RecordImportApplyResult{}, err
		}
		created++
		if err := recordImportBatchYield(ctx, index+1); err != nil {
			return recordmodel.RecordImportApplyResult{}, err
		}
	}
	result := recordmodel.RecordImportApplyResult{ObjectKey: object.Key, Created: created, Skipped: len(preview.Rows) - created, Preview: preview}
	s.audit(ctx, "record_import_applied", object.Key, "", principal, fmt.Sprintf("Imported %d %s records", created, object.Key), nil, map[string]any{"created": created}, nil)
	return result, nil
}

func (s *RecordImportApplicationService) ApplyIdempotent(ctx context.Context, objectKey string, rawCSV []byte, operationKey string, principal principalmodel.Principal) (recordmodel.RecordImportApplyResult, bool, error) {
	return s.applyIdempotent(ctx, objectKey, rawCSV, operationKey, principal, func(object definitionmodel.ObjectSchema) (recordmodel.RecordImportPreview, error) {
		return s.buildPreview(ctx, object, rawCSV, principal)
	})
}

// PreviewRows validates an already decoded Data Exchange batch without
// encoding it back to CSV. The file engine remains the only CSV decoder.
func (s *RecordImportApplicationService) PreviewRows(ctx context.Context, objectKey string, headers []string, rows []dataexchange.ImportRow, principal principalmodel.Principal) (recordmodel.RecordImportPreview, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordImportPreview{}, err
	}
	object, err := s.dependencies.ObjectForAction(principal, objectKey, "import")
	if err != nil {
		s.audit(ctx, "record_import_denied", objectKey, "", principal, "Import preview denied", nil, nil, nil)
		return recordmodel.RecordImportPreview{}, err
	}
	preview, err := s.buildPreviewRows(ctx, object, headers, rows, principal)
	if err != nil {
		return recordmodel.RecordImportPreview{}, err
	}
	s.audit(ctx, "record_import_previewed", objectKey, "", principal, fmt.Sprintf("Previewed %d %s rows", len(preview.Rows), objectKey), nil, map[string]any{"rows": len(preview.Rows), "invalid_rows": preview.InvalidRows}, nil)
	return preview, nil
}

// ApplyRowsIdempotent applies an already decoded Data Exchange batch. A
// canonical JSON fingerprint preserves the existing idempotency receipt
// semantics without reintroducing a second CSV codec path.
func (s *RecordImportApplicationService) ApplyRowsIdempotent(ctx context.Context, objectKey string, headers []string, rows []dataexchange.ImportRow, operationKey string, principal principalmodel.Principal) (recordmodel.RecordImportApplyResult, bool, error) {
	fingerprint, err := json.Marshal(struct {
		Headers []string                 `json:"headers"`
		Rows    []dataexchange.ImportRow `json:"rows"`
	}{Headers: headers, Rows: rows})
	if err != nil {
		return recordmodel.RecordImportApplyResult{}, false, recordImportInternalError("encode structured import fingerprint", err)
	}
	return s.applyIdempotent(ctx, objectKey, fingerprint, operationKey, principal, func(object definitionmodel.ObjectSchema) (recordmodel.RecordImportPreview, error) {
		return s.buildPreviewRows(ctx, object, headers, rows, principal)
	})
}

func (s *RecordImportApplicationService) applyIdempotent(ctx context.Context, objectKey string, input []byte, operationKey string, principal principalmodel.Principal, buildPreview func(definitionmodel.ObjectSchema) (recordmodel.RecordImportPreview, error)) (recordmodel.RecordImportApplyResult, bool, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.RecordImportApplyResult{}, false, err
	}
	object, err := s.dependencies.ObjectForAction(principal, objectKey, "import")
	if err != nil {
		s.audit(ctx, "record_import_denied", objectKey, "", principal, "Import apply denied", nil, nil, nil)
		return recordmodel.RecordImportApplyResult{}, false, err
	}
	if s.dependencies.Execution != nil && s.dependencies.CreateIdempotent != nil {
		if cached, replay, err := s.dependencies.Execution.ReplayImport(ctx, objectKey, operationKey, input, principal); err != nil {
			return recordmodel.RecordImportApplyResult{}, false, err
		} else if replay {
			s.audit(ctx, "record_import_idempotent_replayed", object.Key, "", principal, "Replayed idempotent record import", nil, nil, nil)
			return cached, true, nil
		}
	}
	preview, err := buildPreview(object)
	if err != nil {
		return recordmodel.RecordImportApplyResult{}, false, err
	}
	if !preview.CanApply {
		s.audit(ctx, "record_import_rejected", objectKey, "", principal, "Import apply rejected by preview validation", nil, map[string]any{"invalid_rows": preview.InvalidRows, "duplicate_rows": preview.DuplicateRows}, nil)
		return recordmodel.RecordImportApplyResult{}, false, recordImportError(apperror.KindBadRequest, "backend.import.invalid_rows", nil)
	}
	if s.dependencies.Execution == nil || s.dependencies.CreateIdempotent == nil {
		return recordmodel.RecordImportApplyResult{}, false, recordImportError(apperror.KindInternal, "backend.idempotency.receipt_unavailable", nil)
	}
	cached, claim, replay, err := s.dependencies.Execution.BeginImport(ctx, objectKey, operationKey, input, principal)
	if err != nil {
		if strings.TrimSpace(claim.Execution.ID) != "" {
			logging.LogIdempotency(ctx, claimAuditFacts(claim, "record.import", string(claim.Decision)), principal.RequestID)
			s.audit(ctx, "record_import_idempotency_"+string(claim.Decision), object.Key, "", principal, "Observed idempotent record import decision", nil, nil, idempotency.AuditMetadata(claimAuditFacts(claim, "record.import", string(claim.Decision))))
		}
		return recordmodel.RecordImportApplyResult{}, false, err
	}
	if replay {
		logging.LogIdempotency(ctx, claimAuditFacts(claim, "record.import", "replayed"), principal.RequestID)
		s.audit(ctx, "record_import_idempotent_replayed", object.Key, "", principal, "Replayed idempotent record import", nil, nil, idempotency.AuditMetadata(claimAuditFacts(claim, "record.import", "replayed")))
		return cached, true, nil
	}
	for index, row := range preview.Rows {
		if err := ctx.Err(); err != nil {
			return recordmodel.RecordImportApplyResult{}, false, err
		}
		rowKey := strings.TrimSpace(operationKey) + ":row:" + strconv.Itoa(row.Row)
		if _, _, err := s.dependencies.CreateIdempotent(ctx, object.Key, recordvalidation.RecordCloneData(row.Data), rowKey, principal); err != nil {
			return recordmodel.RecordImportApplyResult{}, false, err
		}
		if err := recordImportBatchYield(ctx, index+1); err != nil {
			return recordmodel.RecordImportApplyResult{}, false, err
		}
	}
	result := recordmodel.RecordImportApplyResult{ObjectKey: object.Key, Created: len(preview.Rows), Preview: preview}
	if err := s.dependencies.Execution.CompleteOperation(ctx, claim, result); err != nil {
		return recordmodel.RecordImportApplyResult{}, false, err
	}
	logging.LogIdempotency(ctx, claimAuditFacts(claim, "record.import", "succeeded"), principal.RequestID)
	s.audit(ctx, "record_import_applied", object.Key, "", principal, fmt.Sprintf("Imported %d %s records", result.Created, object.Key), nil, map[string]any{"created": result.Created}, idempotency.AuditMetadata(claimAuditFacts(claim, "record.import", "succeeded")))
	return result, false, nil
}

func (s *RecordImportApplicationService) buildPreview(ctx context.Context, object definitionmodel.ObjectSchema, rawCSV []byte, principal principalmodel.Principal) (recordmodel.RecordImportPreview, error) {
	fieldByHeader := recordvalidation.RecordImportFieldHeaderAliases(object)
	seenImportKeys := map[string]int{}
	preview := recordmodel.RecordImportPreview{ObjectKey: object.Key}
	_, decodeErr := dataexchange.DecodeCSV(ctx, bytes.NewReader(rawCSV), dataexchange.CSVDecodeLimits{
		MaxBytes: recordImportMaxBytes, MaxRows: recordImportMaxRows, MaxColumns: recordImportMaxColumns,
	}, func(headers []string, sourceRow dataexchange.CSVRecord) error {
		return s.appendPreviewRow(ctx, object, fieldByHeader, headers, sourceRow, principal, seenImportKeys, &preview)
	})
	if decodeErr != nil {
		return recordmodel.RecordImportPreview{}, recordImportDecodeError(decodeErr)
	}
	preview.CanApply = len(preview.Rows) > 0 && preview.InvalidRows == 0 && preview.DuplicateRows == 0
	return preview, nil
}

func (s *RecordImportApplicationService) buildPreviewRows(ctx context.Context, object definitionmodel.ObjectSchema, headers []string, rows []dataexchange.ImportRow, principal principalmodel.Principal) (recordmodel.RecordImportPreview, error) {
	if len(headers) == 0 {
		return recordmodel.RecordImportPreview{}, recordImportDecodeError(dataexchange.ErrHeaderRequired)
	}
	if len(headers) > recordImportMaxColumns {
		return recordmodel.RecordImportPreview{}, recordImportDecodeError(dataexchange.ErrTooManyColumns)
	}
	if len(rows) > recordImportMaxRows {
		return recordmodel.RecordImportPreview{}, recordImportDecodeError(dataexchange.ErrTooManyRows)
	}
	fieldByHeader := recordvalidation.RecordImportFieldHeaderAliases(object)
	seenImportKeys := map[string]int{}
	preview := recordmodel.RecordImportPreview{ObjectKey: object.Key}
	for _, sourceRow := range rows {
		if err := ctx.Err(); err != nil {
			return recordmodel.RecordImportPreview{}, err
		}
		if len(sourceRow.Values) != len(headers) {
			return recordmodel.RecordImportPreview{}, recordImportDecodeError(dataexchange.ErrInvalidCSV)
		}
		if err := s.appendPreviewRow(ctx, object, fieldByHeader, headers, dataexchange.CSVRecord{Number: sourceRow.Number, Values: sourceRow.Values}, principal, seenImportKeys, &preview); err != nil {
			return recordmodel.RecordImportPreview{}, err
		}
	}
	preview.CanApply = len(preview.Rows) > 0 && preview.InvalidRows == 0 && preview.DuplicateRows == 0
	return preview, nil
}

func (s *RecordImportApplicationService) appendPreviewRow(ctx context.Context, object definitionmodel.ObjectSchema, fieldByHeader map[string]definitionmodel.FieldSchema, headers []string, sourceRow dataexchange.CSVRecord, principal principalmodel.Principal, seenImportKeys map[string]int, preview *recordmodel.RecordImportPreview) error {
	rawRow := sourceRow.Values
	row := recordmodel.RecordImportPreviewRow{Row: sourceRow.Number, Data: map[string]any{}, RawValues: map[string]string{}, Valid: true}
	for columnIndex, rawHeader := range headers {
		header := strings.TrimSpace(rawHeader)
		if header == "" || strings.HasSuffix(header, "__display") {
			continue
		}
		fieldKey := ""
		if field, ok := fieldByHeader[recordvalidation.RecordNormalizeImportHeaderAlias(header)]; ok {
			fieldKey = strings.TrimSpace(field.Key)
		}
		if fieldKey == "" {
			fieldKey = header
		}
		row.RawValues[fieldKey] = strings.TrimSpace(rawRow[columnIndex])
		field, ok := fieldByHeader[fieldKey]
		if !ok {
			row.Issues = append(row.Issues, importRowIssue(fieldKey, "error", "backend.import.unknown_field"))
			continue
		}
		if !recordpolicy.RecordCanWriteFieldForPrincipal(principal, object.Key, fieldKey) {
			row.Issues = append(row.Issues, importRowIssue(fieldKey, "error", "backend.import.field_not_writable"))
			continue
		}
		value := strings.TrimSpace(rawRow[columnIndex])
		if value == "" {
			continue
		}
		coerced, issue := recordvalidation.RecordCoerceImportValue(object.Key, field, value)
		if issue != "" {
			row.Issues = append(row.Issues, importRowIssue(fieldKey, "error", issue, "value", value, "field", field.Key))
			continue
		}
		row.Data[fieldKey] = coerced
	}
	normalized, err := recordvalidation.RecordNormalizeData(object, row.Data, false)
	if err != nil {
		row.Issues = append(row.Issues, importRowIssueFromError("", err))
	} else {
		row.Data = normalized
	}
	if err := recordvalidation.RecordValidateData(object, row.Data, false); err != nil {
		row.Issues = append(row.Issues, importRowIssueFromError("", err))
	}
	if err := recordpolicy.RecordValidateWritableFields(principal, object, row.Data); err != nil {
		row.Issues = append(row.Issues, importRowIssueFromError("", err))
	}
	authorizationRecord := recordmodel.Record{Data: row.Data}
	recordpolicy.RecordApplyOwnerDefault(&authorizationRecord, principal)
	if s.dependencies.CanWrite != nil && !s.dependencies.CanWrite(principal, object, recordpolicy.RecordDataWithOwnerFacts(authorizationRecord)) {
		row.Issues = append(row.Issues, importRowIssue("", "error", "backend.record.outside_scope"))
	}
	if s.dependencies.ValidateRelations != nil {
		if err := s.dependencies.ValidateRelations(ctx, object, row.Data, principal); err != nil {
			row.Issues = append(row.Issues, importRowIssueFromError("", err))
		}
	}
	for _, field := range recordvalidation.RecordDuplicateIdentityFields(object) {
		value := row.Data[field.Key]
		if recordvalidation.RecordIsEmptyValue(value) {
			continue
		}
		identityKey := field.Key + "=" + strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		if firstRow, ok := seenImportKeys[identityKey]; ok {
			row.Duplicate = true
			row.Issues = append(row.Issues, importRowIssue(field.Key, "error", "backend.import.duplicate_in_file", "row", strconv.Itoa(firstRow)))
		} else {
			seenImportKeys[identityKey] = row.Row
		}
		exists, err := s.dependencies.Repository.UniqueExists(ctx, principal.WorkspaceID, object.Key, field.Key, "", value)
		if err != nil {
			return recordImportInternalError("check duplicate field", err)
		}
		if exists {
			row.Duplicate = true
			row.Issues = append(row.Issues, importRowIssue(field.Key, "error", "backend.import.duplicate_existing"))
		}
	}
	if len(row.Issues) > 0 {
		row.Valid = false
	}
	if !row.Valid {
		row.ErrorSummary = recordvalidation.RecordImportErrorSummary(row)
		preview.ErrorRows = append(preview.ErrorRows, row)
	}
	if row.Duplicate {
		preview.DuplicateRows++
	}
	if row.Valid {
		preview.ValidRows++
	} else {
		preview.InvalidRows++
	}
	preview.Rows = append(preview.Rows, row)
	return nil
}

func recordImportDecodeError(err error) error {
	switch {
	case errors.Is(err, dataexchange.ErrPayloadTooLarge):
		return recordImportError(apperror.KindBadRequest, "backend.import.payload_too_large", err)
	case errors.Is(err, dataexchange.ErrTooManyRows):
		return recordImportError(apperror.KindBadRequest, "backend.import.too_many_rows", err)
	case errors.Is(err, dataexchange.ErrTooManyColumns):
		return recordImportError(apperror.KindBadRequest, "backend.import.too_many_columns", err)
	case errors.Is(err, dataexchange.ErrHeaderRequired):
		return recordImportError(apperror.KindBadRequest, "backend.import.header_required", err)
	case errors.Is(err, dataexchange.ErrInvalidCSV):
		return recordImportError(apperror.KindBadRequest, "backend.import.invalid_csv", err)
	default:
		return err
	}
}

func recordImportBatchYield(ctx context.Context, processed int) error {
	if processed%recordImportBatchSize != 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return nil
	}
}

func (s *RecordImportApplicationService) audit(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) {
	if s.dependencies.Audit != nil {
		s.dependencies.Audit(ctx, event, objectKey, recordID, principal, summary, before, after, metadata)
	}
}

func recordImportInternalError(operation string, err error) error {
	return recordImportError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func recordImportError(kind apperror.ErrorKind, code string, err error, params ...string) error {
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

func importRowIssue(field, severity, code string, params ...string) recordmodel.RecordImportRowIssue {
	code = recordvalidation.RecordPolicyMessageCode(code, "backend.bad_request")
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return recordmodel.RecordImportRowIssue{Field: field, Severity: severity, Message: code, Code: code, Params: values}
}

func importRowIssueFromError(field string, err error) recordmodel.RecordImportRowIssue {
	if err == nil {
		return importRowIssue(field, "error", "backend.bad_request")
	}
	var coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	if errors.As(err, &coded) {
		code := recordvalidation.RecordPolicyMessageCode(coded.ErrorCode(), "backend.bad_request")
		return recordmodel.RecordImportRowIssue{Field: field, Severity: "error", Message: code, Code: code, Params: coded.ErrorParams()}
	}
	return importRowIssue(field, "error", "backend.bad_request")
}
