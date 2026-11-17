package upload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/entityid"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// IngestUploaded stages external bytes using the Runtime field policy. The
// existing Lifecycle queue and FileScanProcessor alone produce a verdict.
// Unlike CreateDerived, this path never writes clean evidence itself.
func (s *FileCapabilityService) IngestUploaded(ctx context.Context, workspaceID string, principal principalmodel.Principal, request runtimeext.UploadedFileRequest, policy recordmodel.RecordFileFieldPolicy) (runtimeext.FileVerificationEvidence, error) {
	if err := ctx.Err(); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	if s == nil || s.store == nil || s.subjects == nil || s.verifier == nil || s.blobs == nil {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindUnavailable, "backend.upload.ingest_unavailable")
	}
	if err := uploadAuthorizePrincipal(principal); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	if strings.TrimSpace(principal.UserID) == "" || workspaceID == "" || principal.WorkspaceID != workspaceID {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindForbidden, "backend.upload.subject_binding_denied")
	}
	filename := strings.TrimSpace(request.Filename)
	contentType := strings.ToLower(strings.TrimSpace(request.ContentType))
	digest := strings.ToLower(strings.TrimSpace(request.ContentSHA256))
	decoded, decodeErr := hex.DecodeString(digest)
	if request.IdempotencyKey == "" || request.ObjectKey == "" || request.FieldKey == "" || filename == "" || filepath.Base(filename) != filename || strings.ContainsAny(filename, "\\\x00\r\n") || len(filename) > 255 || request.Content == nil || decodeErr != nil || len(decoded) != sha256.Size || !derivedContentTypePattern.MatchString(contentType) || !policy.ScanRequired {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindBadRequest, "backend.upload.ingest_request_invalid")
	}
	if request.Size < 1 || request.Size > policy.MaxSizeBytes || policy.MaxSizeBytes < 1 || policy.MaxSizeBytes > recordmodel.RecordFileMaximumSizeBytes {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindBadRequest, "backend.upload.file_too_large")
	}
	if !policy.AllowsContentType(contentType) {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindBadRequest, "backend.upload.mime_type_denied")
	}
	identity := sha256.Sum256([]byte(strings.Join([]string{workspaceID, principal.UserID, request.IdempotencyKey, request.ObjectKey, request.FieldKey}, "\x00")))
	fileID, err := entityid.Derive("art", "runtime.uploaded-file", workspaceID, principal.UserID, request.IdempotencyKey, request.ObjectKey, request.FieldKey)
	if err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	nonce := [16]byte{}
	if _, err := rand.Read(nonce[:]); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	staged, err := s.blobs.Stage(ctx, runtimefile.BlobStageRequest{WorkspaceID: workspaceID, StageID: fileID + ":" + hex.EncodeToString(nonce[:]), Content: request.Content, MaxBytes: policy.MaxSizeBytes})
	if err != nil {
		if errors.Is(err, runtimefile.ErrBlobTooLarge) {
			return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindBadRequest, "backend.upload.file_too_large")
		}
		return runtimeext.FileVerificationEvidence{}, err
	}
	defer func() { _ = s.blobs.Delete(context.WithoutCancel(ctx), workspaceID, staged.BlobKey) }()
	if staged.Size != request.Size || !strings.EqualFold(staged.ContentSHA256, digest) {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindBadRequest, "backend.upload.content_identity_mismatch")
	}
	nameDigest := sha256.Sum256([]byte(filename))
	storageName := digest + "-" + hex.EncodeToString(identity[16:24]) + "-" + hex.EncodeToString(nameDigest[:4]) + filepath.Ext(filename)
	existing, lookupErr := s.store.FindFileScan(ctx, workspaceID, fileID)
	if lookupErr == nil {
		if existing.Filename != storageName || existing.ObjectKey != request.ObjectKey || existing.FieldKey != request.FieldKey || existing.ContentType != contentType || existing.SHA256 != digest || existing.Size != request.Size {
			return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindConflict, "backend.upload.ingest_idempotency_conflict")
		}
		if err := s.subjects.Authorize(ctx, workspaceID, principal.UserID, fileID); err != nil {
			return runtimeext.FileVerificationEvidence{}, err
		}
		stored, err := s.blobs.Stat(ctx, workspaceID, storageName)
		if err != nil {
			return runtimeext.FileVerificationEvidence{}, err
		}
		if stored.Size != request.Size || !strings.EqualFold(stored.ContentSHA256, digest) {
			return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindConflict, "backend.upload.content_identity_mismatch")
		}
		return s.InspectUploaded(ctx, workspaceID, principal, runtimeext.UploadedFileInspection{FileID: fileID, ObjectKey: request.ObjectKey, FieldKey: request.FieldKey})
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return runtimeext.FileVerificationEvidence{}, lookupErr
	}
	if _, err := s.blobs.Commit(ctx, runtimefile.BlobCommitRequest{WorkspaceID: workspaceID, StageKey: staged.BlobKey, BlobKey: storageName, ContentSHA256: digest, Size: request.Size}); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	artifactRegistered := false
	defer func() {
		if !artifactRegistered {
			_ = s.blobs.Delete(context.WithoutCancel(ctx), workspaceID, storageName)
		}
	}()
	artifact := lifecyclecontract.UploadArtifact{ArtifactID: fileID, WorkspaceID: workspaceID, ObjectKey: request.ObjectKey, FieldKey: request.FieldKey, Filename: storageName, ContentType: contentType, SHA256: digest, Size: request.Size, CreatedAt: s.clock().UTC()}
	if err := s.store.RegisterUpload(ctx, artifact); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	artifactRegistered = true
	if err := s.subjects.Ensure(ctx, artifact, principal); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	return s.InspectUploaded(ctx, workspaceID, principal, runtimeext.UploadedFileInspection{FileID: fileID, ObjectKey: request.ObjectKey, FieldKey: request.FieldKey})
}

// InspectUploaded never opens bytes or gives record-read authority. The exact
// uploader and original file field are checked before exposing scan evidence.
func (s *FileCapabilityService) InspectUploaded(ctx context.Context, workspaceID string, principal principalmodel.Principal, request runtimeext.UploadedFileInspection) (runtimeext.FileVerificationEvidence, error) {
	if s == nil || s.subjects == nil || s.verifier == nil {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindUnavailable, "backend.upload.inspect_unavailable")
	}
	if err := uploadAuthorizePrincipal(principal); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	if principal.WorkspaceID != workspaceID || request.FileID == "" || request.ObjectKey == "" || request.FieldKey == "" {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindForbidden, "backend.upload.subject_binding_denied")
	}
	if err := s.subjects.Authorize(ctx, workspaceID, principal.UserID, request.FileID); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	evidence, err := s.verifier.Status(ctx, workspaceID, request.FileID)
	if err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	if evidence.ArtifactID != request.FileID || evidence.ObjectKey != request.ObjectKey || evidence.FieldKey != request.FieldKey {
		return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindForbidden, "backend.upload.file_identity_mismatch")
	}
	return runtimeext.FileVerificationEvidence{FileID: evidence.ArtifactID, ObjectKey: evidence.ObjectKey, FieldKey: evidence.FieldKey, Filename: evidence.Filename, ContentType: evidence.ContentType, ContentSHA256: evidence.SHA256, Size: evidence.Size, Status: evidence.Status, Provider: evidence.Provider, EvidenceRef: evidence.EvidenceRef, ScanReceipt: evidence.Receipt}, nil
}
