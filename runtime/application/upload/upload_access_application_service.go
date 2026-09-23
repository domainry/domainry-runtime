package upload

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

const fileScanReceiptVersion = "v2"

var ErrFileNotClean = errors.New("backend.upload.scan_not_clean")
var ErrFileScanReceiptInvalid = errors.New("backend.upload.scan_receipt_invalid")

type FileScanReceiptVerifier struct {
	store lifecyclecontract.FileScanStore
	key   []byte
}

func NewFileScanReceiptVerifier(store lifecyclecontract.FileScanStore, key []byte) *FileScanReceiptVerifier {
	return &FileScanReceiptVerifier{store: store, key: append([]byte(nil), key...)}
}

func (s *FileScanReceiptVerifier) Status(ctx context.Context, workspaceID, fileID string) (lifecyclecontract.FileScanEvidence, error) {
	if s == nil || s.store == nil || len(s.key) < 32 {
		return lifecyclecontract.FileScanEvidence{}, errors.New("backend.upload.scan_service_unavailable")
	}
	evidence, err := s.store.FindFileScan(ctx, workspaceID, fileID)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	if evidence.Status == lifecyclecontract.FileScanClean {
		evidence.Receipt = s.sign(evidence)
	}
	return evidence, nil
}

func (s *FileScanReceiptVerifier) VerifyClean(ctx context.Context, workspaceID, fileID, contentSHA256, receipt string) (lifecyclecontract.FileScanEvidence, error) {
	evidence, err := s.Status(ctx, workspaceID, fileID)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	if evidence.Status != lifecyclecontract.FileScanClean {
		return evidence, fmt.Errorf("%w: %s", ErrFileNotClean, evidence.Status)
	}
	if !strings.EqualFold(strings.TrimSpace(contentSHA256), evidence.SHA256) || !hmac.Equal([]byte(strings.TrimSpace(receipt)), []byte(s.sign(evidence))) {
		return lifecyclecontract.FileScanEvidence{}, ErrFileScanReceiptInvalid
	}
	return evidence, nil
}

func (s *FileScanReceiptVerifier) sign(e lifecyclecontract.FileScanEvidence) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(strings.Join([]string{fileScanReceiptVersion, e.WorkspaceID, e.FileID, strings.ToLower(e.SHA256), strconv.FormatInt(e.Size, 10), strings.ToLower(strings.TrimSpace(e.ContentType)), e.Status, e.Provider, e.EvidenceRef, e.ScannedAt.UTC().Format(time.RFC3339Nano)}, "\x00")))
	return fileScanReceiptVersion + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type UploadAccessApplicationService struct {
	catalog  CatalogPort
	audit    AuditPort
	records  RecordQueryPort
	subjects *UploadSubjectRegistry
}

func NewUploadAccessApplicationService(catalog CatalogPort, audit AuditPort, records RecordQueryPort, subjects ...*UploadSubjectRegistry) *UploadAccessApplicationService {
	s := &UploadAccessApplicationService{catalog: catalog, audit: audit, records: records}
	if len(subjects) > 0 {
		s.subjects = subjects[0]
	}
	return s
}

func (s *UploadAccessApplicationService) AuthorizeUpload(ctx context.Context, objectKey, fieldKey string, principal principalmodel.Principal) error {
	if err := uploadAuthorizePrincipal(principal); err != nil {
		return err
	}
	objectKey, fieldKey = strings.TrimSpace(objectKey), strings.TrimSpace(fieldKey)
	object, ok := s.catalog.ObjectMap(ctx)[objectKey]
	if !ok {
		return uploadAccessError(apperror.KindNotFound, "backend.object.not_found")
	}
	field, found := uploadField(object, fieldKey)
	if !found {
		return uploadAccessError(apperror.KindBadRequest, "backend.upload.field_not_defined")
	}
	if field.Type != recordmodel.RecordFileFieldType && field.Type != recordmodel.RecordFileListFieldType {
		return uploadAccessError(apperror.KindBadRequest, "backend.upload.field_type_invalid")
	}
	if _, err := recordmodel.RecordFileFieldPolicyFor(field); err != nil {
		return uploadAccessError(apperror.KindBadRequest, "backend.upload.field_policy_invalid")
	}
	if (recordpolicy.RecordAllowsObjectAction(principal, objectKey, "update") || recordpolicy.RecordAllowsObjectAction(principal, objectKey, "create")) && recordpolicy.RecordCanWriteObjectFieldKeyForPrincipal(principal, object, fieldKey) {
		return nil
	}
	s.audit.AppendWithMetadata(ctx, auditmodel.EventFamilyRuntimeUpload, "file_upload_denied", objectKey, "", principal, "File upload permission denied", nil, nil, map[string]any{"field_key": fieldKey, "reason": "permission"})
	return uploadAccessError(apperror.KindForbidden, "backend.upload.permission_denied")
}

func (s *UploadAccessApplicationService) UploadPolicy(ctx context.Context, objectKey, fieldKey string, principal principalmodel.Principal) (recordmodel.RecordFileFieldPolicy, error) {
	if err := s.AuthorizeUpload(ctx, objectKey, fieldKey, principal); err != nil {
		return recordmodel.RecordFileFieldPolicy{}, err
	}
	object := s.catalog.ObjectMap(ctx)[strings.TrimSpace(objectKey)]
	field, _ := uploadField(object, strings.TrimSpace(fieldKey))
	policy, err := recordmodel.RecordFileFieldPolicyFor(field)
	if err != nil {
		return recordmodel.RecordFileFieldPolicy{}, uploadAccessError(apperror.KindBadRequest, "backend.upload.field_policy_invalid")
	}
	return policy, nil
}

func (s *UploadAccessApplicationService) ValidateUploadContent(ctx context.Context, objectKey, fieldKey, contentType string, size int64, principal principalmodel.Principal) error {
	policy, err := s.UploadPolicy(ctx, objectKey, fieldKey, principal)
	if err != nil {
		return err
	}
	if size < 1 || size > policy.MaxSizeBytes {
		return uploadAccessError(apperror.KindBadRequest, "backend.upload.file_too_large")
	}
	if !policy.AllowsContentType(contentType) {
		return uploadAccessError(apperror.KindBadRequest, "backend.upload.mime_type_denied")
	}
	return nil
}

func (s *UploadAccessApplicationService) RecordUploaded(ctx context.Context, objectKey, fieldKey string, reference recordmodel.RecordFileReference, principal principalmodel.Principal) {
	if uploadAuthorizePrincipal(principal) != nil {
		return
	}
	s.audit.AppendWithMetadata(ctx, auditmodel.EventFamilyRuntimeUpload, "file_uploaded", objectKey, "", principal, "", nil, nil, map[string]any{
		"field_key": fieldKey, "file_id": reference.FileID, "filename": reference.Filename, "content_type": reference.ContentType,
		"size": reference.Size, "content_sha256": reference.ContentSHA256,
	})
}

func (s *UploadAccessApplicationService) AuthorizeDownload(ctx context.Context, objectKey, fieldKey, recordID, filename string, principal principalmodel.Principal) error {
	if err := uploadAuthorizePrincipal(principal); err != nil {
		return err
	}
	objectKey, fieldKey, recordID = strings.TrimSpace(objectKey), strings.TrimSpace(fieldKey), strings.TrimSpace(recordID)
	if objectKey == "" || fieldKey == "" {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download context missing", "missing_context", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	object, ok := s.catalog.ObjectMap(ctx)[objectKey]
	if !ok {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download object missing", "object", apperror.KindNotFound, "backend.object.not_found")
	}
	field, fileField := uploadField(object, fieldKey)
	fileField = fileField && (field.Type == recordmodel.RecordFileFieldType || field.Type == recordmodel.RecordFileListFieldType)
	readAllowed := fileField && recordpolicy.RecordAllowsObjectAction(principal, objectKey, "read") && recordpolicy.RecordCanReadObjectFieldKeyForPrincipal(principal, object, fieldKey)
	if !readAllowed {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download permission denied", "permission", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	return s.authorizeRecordDownload(ctx, objectKey, fieldKey, recordID, filename, principal)
}

func (s *UploadAccessApplicationService) RecordTicketDownload(ctx context.Context, objectKey, fieldKey, recordID, fileID string, principal principalmodel.Principal) {
	if uploadAuthorizePrincipal(principal) != nil {
		return
	}
	s.audit.AppendWithMetadata(ctx, auditmodel.EventFamilyRuntimeUpload, "file_downloaded", strings.TrimSpace(objectKey), strings.TrimSpace(recordID), principal, "", nil, nil, map[string]any{
		"file_id": strings.TrimSpace(fileID), "field_key": strings.TrimSpace(fieldKey), "authorization": "action_download_ticket",
	})
}

func uploadField(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == fieldKey {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func objectHasField(object definitionmodel.ObjectSchema, fieldKey string) bool {
	_, found := uploadField(object, fieldKey)
	return found
}

func uploadAccessError(kind apperror.ErrorKind, code string) error {
	return &apperror.AppError{Kind: kind, Code: code}
}

func uploadAuthorizePrincipal(principal principalmodel.Principal) error {
	if !principal.Known {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.role.unknown"}
	}
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}
