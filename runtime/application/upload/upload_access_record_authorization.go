package upload

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *UploadAccessApplicationService) authorizeRecordDownload(ctx context.Context, objectKey, fieldKey, recordID, filename string, principal principalmodel.Principal) error {
	if objectKey == "document" && recordID == "" {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "Document download record context missing", "record_context", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	if recordID == "" {
		return nil
	}
	if s.records == nil {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download record service unavailable", "record_service", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	record, err := s.records.GetRecord(ctx, objectKey, recordID, principal)
	if err != nil {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download record access denied", "record", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	if !uploadFileMatchesRecord(filename, record.Data[fieldKey]) {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download filename mismatch", "mismatch", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	if objectKey == "document" && uploadRecordBoolean(record.Data["sensitive"]) && !principal.HasExactPermission("document.sensitive.read") {
		s.audit.AppendWithMetadata(ctx, "sensitive_file_download_denied", objectKey, recordID, principal, "Sensitive document download denied", nil, nil, map[string]any{"filename": filename, "field_key": fieldKey, "reason": "sensitive"})
		return uploadAccessError(apperror.KindForbidden, "backend.upload.permission_denied")
	}
	s.audit.AppendWithMetadata(ctx, "file_downloaded", objectKey, recordID, principal, "", nil, nil, map[string]any{"filename": filename, "field_key": fieldKey})
	return nil
}

func (s *UploadAccessApplicationService) denyDownload(ctx context.Context, objectKey, fieldKey, recordID, filename string, principal principalmodel.Principal, summary, reason string, kind apperror.ErrorKind, code string) error {
	s.audit.AppendWithMetadata(ctx, "file_download_denied", objectKey, recordID, principal, summary, nil, nil, map[string]any{"filename": filename, "field_key": fieldKey, "reason": reason})
	return uploadAccessError(kind, code)
}
