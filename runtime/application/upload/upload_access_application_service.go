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

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

const fileScanReceiptVersion = "v1"

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
	_, _ = mac.Write([]byte(strings.Join([]string{fileScanReceiptVersion, e.WorkspaceID, e.FileID, strings.ToLower(e.SHA256), strconv.FormatInt(e.Size, 10), e.Status, e.Provider, e.EvidenceRef, e.ScannedAt.UTC().Format(time.RFC3339Nano)}, "\x00")))
	return fileScanReceiptVersion + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type UploadAccessApplicationService struct {
	catalog CatalogPort
	audit   AuditPort
	records RecordQueryPort
}

func NewUploadAccessApplicationService(catalog CatalogPort, audit AuditPort, records RecordQueryPort) *UploadAccessApplicationService {
	return &UploadAccessApplicationService{catalog: catalog, audit: audit, records: records}
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
	if !objectHasField(object, fieldKey) {
		return uploadAccessError(apperror.KindBadRequest, "backend.upload.field_not_defined")
	}
	if (recordpolicy.RecordAllowsObjectAction(principal, objectKey, "update") || recordpolicy.RecordAllowsObjectAction(principal, objectKey, "create")) && recordpolicy.RecordCanWriteFieldForPrincipal(principal, objectKey, fieldKey) {
		return nil
	}
	s.audit.AppendWithMetadata(ctx, "file_upload_denied", objectKey, "", principal, "File upload permission denied", nil, nil, map[string]any{"field_key": fieldKey, "reason": "permission"})
	return uploadAccessError(apperror.KindForbidden, "backend.upload.permission_denied")
}

func (s *UploadAccessApplicationService) RecordUploaded(ctx context.Context, objectKey, fieldKey, filename, contentType string, size int, principal principalmodel.Principal) {
	if uploadAuthorizePrincipal(principal) != nil {
		return
	}
	s.audit.AppendWithMetadata(ctx, "file_uploaded", objectKey, "", principal, "", nil, nil, map[string]any{"field_key": fieldKey, "filename": filename, "content_type": contentType, "size": size})
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
	readAllowed := objectHasField(object, fieldKey) && recordpolicy.RecordAllowsObjectAction(principal, objectKey, "read") && recordpolicy.RecordCanReadFieldForPrincipal(principal, objectKey, fieldKey)
	if !readAllowed {
		return s.denyDownload(ctx, objectKey, fieldKey, recordID, filename, principal, "File download permission denied", "permission", apperror.KindForbidden, "backend.upload.permission_denied")
	}
	return s.authorizeRecordDownload(ctx, objectKey, fieldKey, recordID, filename, principal)
}

func objectHasField(object definitionmodel.ObjectSchema, fieldKey string) bool {
	for _, field := range object.Fields {
		if field.Key == fieldKey {
			return true
		}
	}
	return false
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
