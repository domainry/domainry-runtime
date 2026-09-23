package upload

import (
	"context"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *UploadAccessApplicationService) authorizeRecordDownload(ctx context.Context, objectKey, fieldKey, recordID, filename string, principal principalmodel.Principal) error {
	if recordID == "" {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download record context missing", "record_context", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	if s.records == nil {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download record service unavailable", "record_service", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	record, err := s.records.GetRecord(ctx, objectKey, recordID, principal)
	if err != nil {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download record access denied", "record", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	object := s.catalog.ObjectMap(ctx)[objectKey]
	field, found := uploadField(object, fieldKey)
	reference, matches := uploadFileMatchesRecord(filename, field, record.Data[fieldKey])
	if !found || !matches {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download filename mismatch", "mismatch", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	if s.subjects != nil {
		// Editable file URLs cannot claim another subject's upload. Trusted
		// promotion/sharing uses Action tickets with exact record authorization.
		if err := s.subjects.Authorize(ctx, principal.WorkspaceID, record.OwnerUserID, filename); err != nil {
			return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File upload subject does not own the record", "upload_subject", apperror.KindForbidden, "backend.upload.permission_denied")
		}
	}
	if objectKey == "document" && uploadRecordBoolean(record.Data["sensitive"]) && !principal.HasExactPermission("document.sensitive.read") {
		s.audit.AppendWithMetadata(ctx, auditmodel.EventFamilyRuntimeUpload, "sensitive_file_download_denied", objectKey, recordID, principal, "Sensitive document download denied", nil, nil, map[string]any{"filename": filename, "field_key": fieldKey, "reason": "sensitive"})
		return uploadAccessError(apperror.KindForbidden, "backend.upload.permission_denied")
	}
	s.audit.AppendWithMetadata(ctx, auditmodel.EventFamilyRuntimeUpload, "file_downloaded", objectKey, recordID, principal, "", nil, nil, map[string]any{
		"file_id": reference.FileID, "filename": reference.Filename, "content_type": reference.ContentType, "size": reference.Size,
		"content_sha256": reference.ContentSHA256, "field_key": fieldKey,
	})
	return nil
}

func (s *UploadAccessApplicationService) denyDownload(ctx context.Context, objectKey, fieldKey, recordID, filename string, principal principalmodel.Principal, summary, reason string, kind apperror.ErrorKind, code string) error {
	s.audit.AppendWithMetadata(ctx, auditmodel.EventFamilyRuntimeUpload, "file_download_denied", objectKey, recordID, principal, summary, nil, nil, map[string]any{"filename": filename, "field_key": fieldKey, "reason": reason})
	return uploadAccessError(kind, code)
}
