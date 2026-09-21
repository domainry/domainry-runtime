package upload

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const maxDerivedFileBytes = 128 << 20

var derivedContentTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$`)

// FileCapabilityService is the Runtime-owned byte boundary used by generated
// project Actions. It never accepts a workspace path or a host filesystem path.
type FileCapabilityService struct {
	store    lifecyclecontract.UploadFileArtifactStore
	verifier *FileScanReceiptVerifier
	blobs    runtimefile.BlobStore
	clock    func() time.Time
	tickets  *FileDownloadTicketService
	subjects *UploadSubjectRegistry
}

func NewFileCapabilityService(store lifecyclecontract.UploadFileArtifactStore, verifier *FileScanReceiptVerifier, blobs runtimefile.BlobStore, clock func() time.Time, tickets ...*FileDownloadTicketService) (*FileCapabilityService, error) {
	if store == nil || verifier == nil || blobs == nil {
		return nil, fmt.Errorf("file capability artifact store, verifier and blob store are required")
	}
	if !validFileAdapterDescriptor(blobs.Descriptor()) {
		return nil, fmt.Errorf("file capability blob store descriptor is required")
	}
	if clock == nil {
		clock = time.Now
	}
	var downloadTickets *FileDownloadTicketService
	if len(tickets) > 0 {
		downloadTickets = tickets[0]
	}
	return &FileCapabilityService{store: store, verifier: verifier, blobs: blobs, clock: clock, tickets: downloadTickets}, nil
}

func (s *FileCapabilityService) VerifyClean(ctx context.Context, workspaceID string, request runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error) {
	if principal, claiming := ctx.Value(uploadClaimPrincipalKey{}).(principalmodel.Principal); claiming && !principal.SystemScope.Valid() {
		if s == nil || s.subjects == nil {
			return runtimeext.FileVerificationEvidence{}, uploadAccessError(apperror.KindForbidden, "backend.upload.subject_binding_denied")
		}
		if err := s.subjects.Authorize(ctx, workspaceID, principal.UserID, request.FileID); err != nil {
			return runtimeext.FileVerificationEvidence{}, err
		}
	}
	evidence, err := s.verifier.VerifyClean(ctx, workspaceID, request.FileID, request.ContentSHA256, request.ScanReceipt)
	if err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	return runtimeext.FileVerificationEvidence{
		FileID: evidence.FileID, ObjectKey: evidence.ObjectKey, FieldKey: evidence.FieldKey, ContentSHA256: evidence.SHA256, Filename: evidence.Filename, ContentType: evidence.ContentType,
		Size: evidence.Size, Status: evidence.Status, Provider: evidence.Provider, EvidenceRef: evidence.EvidenceRef, ScanReceipt: evidence.Receipt,
	}, nil
}

// VerifyRecordReference validates a structured Record file reference against
// Runtime-owned upload, subject and scanner evidence. Direct Record writes may
// only claim the exact Object/Field that authorized the upload.
func (s *FileCapabilityService) VerifyRecordReference(ctx context.Context, workspaceID string, principal principalmodel.Principal, objectKey, fieldKey string, reference recordmodel.RecordFileReference, scanRequired bool) error {
	if s == nil || s.verifier == nil || s.subjects == nil {
		return errors.New("backend.upload.file_reference_verifier_unavailable")
	}
	if err := s.subjects.Authorize(ctx, workspaceID, principal.UserID, reference.FileID); err != nil {
		return err
	}
	evidence, err := s.verifier.Status(ctx, workspaceID, reference.FileID)
	if err != nil {
		return err
	}
	if evidence.ObjectKey != strings.TrimSpace(objectKey) || evidence.FieldKey != strings.TrimSpace(fieldKey) || evidence.FileID != reference.FileID || evidence.Filename != reference.Filename || !strings.EqualFold(evidence.ContentType, reference.ContentType) || evidence.Size != reference.Size || !strings.EqualFold(evidence.SHA256, reference.ContentSHA256) {
		return errors.New("backend.upload.file_identity_mismatch")
	}
	if scanRequired {
		if _, err := s.verifier.VerifyClean(ctx, workspaceID, reference.FileID, reference.ContentSHA256, reference.ScanReceipt); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileCapabilityService) ValidateRecordReferences(ctx context.Context, object definitionmodel.ObjectSchema, values map[string]any, principal principalmodel.Principal) error {
	for _, field := range object.Fields {
		if _, changed := values[field.Key]; !changed || field.Type != recordmodel.RecordFileFieldType && field.Type != recordmodel.RecordFileListFieldType {
			continue
		}
		policy, err := recordmodel.RecordFileFieldPolicyFor(field)
		if err != nil {
			return apperror.New(apperror.KindBadRequest, "backend.upload.field_policy_invalid", err, map[string]string{"field": field.Key})
		}
		references, err := recordmodel.RecordFileReferences(field, values[field.Key])
		if err != nil {
			code := "backend.validation.file_reference"
			if contract, ok := err.(*recordmodel.RecordFileContractError); ok {
				code = contract.Code
			}
			return apperror.New(apperror.KindBadRequest, code, err, map[string]string{"field": field.Key})
		}
		for _, reference := range references {
			if err := s.VerifyRecordReference(ctx, principal.WorkspaceID, principal, object.Key, field.Key, reference, policy.ScanRequired); err != nil {
				kind := apperror.KindBadRequest
				code := "backend.upload.file_identity_mismatch"
				if apperror.CodeOf(err) == "backend.upload.subject_binding_denied" {
					kind, code = apperror.KindForbidden, "backend.upload.subject_binding_denied"
				} else if errors.Is(err, ErrFileNotClean) || errors.Is(err, ErrFileScanReceiptInvalid) {
					code = "backend.upload.scan_not_clean"
				}
				return apperror.New(kind, code, err, map[string]string{"field": field.Key, "file_id": reference.FileID})
			}
		}
	}
	return nil
}

func (s *FileCapabilityService) BindUploadSubjects(subjects *UploadSubjectRegistry) {
	s.subjects = subjects
}

func (s *FileCapabilityService) OpenVerified(ctx context.Context, workspaceID string, request runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error) {
	evidence, err := s.VerifyClean(ctx, workspaceID, request.FileVerificationRequest)
	if err != nil {
		return runtimeext.VerifiedFile{}, err
	}
	// The scan evidence keeps the field that authorized the original upload.
	// The Action executor separately authorizes Binding against the exact
	// caller-readable record that currently references this immutable file ID.
	// Those bindings legitimately differ after a trusted Action promotes an
	// upload into a source, version, or other business record.
	reader, err := s.blobs.Open(ctx, workspaceID, evidence.Filename)
	if err != nil {
		return runtimeext.VerifiedFile{}, fmt.Errorf("backend.upload.file_unavailable: %w", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, evidence.Size+1))
	if err != nil {
		return runtimeext.VerifiedFile{}, fmt.Errorf("backend.upload.file_unavailable: %w", err)
	}
	digest := sha256.Sum256(content)
	if int64(len(content)) != evidence.Size || !strings.EqualFold(hex.EncodeToString(digest[:]), evidence.ContentSHA256) {
		return runtimeext.VerifiedFile{}, errors.New("backend.upload.file_identity_mismatch")
	}
	return runtimeext.VerifiedFile{FileVerificationEvidence: evidence, Filename: evidence.Filename, ContentType: evidence.ContentType, Content: io.NopCloser(bytes.NewReader(content))}, nil
}

func (s *FileCapabilityService) IssueDownload(ctx context.Context, workspaceID string, principal runtimeext.Principal, request runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error) {
	if s.tickets == nil {
		return runtimeext.FileDownloadTicket{}, errors.New("backend.upload.download_ticket_unavailable")
	}
	if _, err := s.VerifyClean(ctx, workspaceID, request.FileVerificationRequest); err != nil {
		return runtimeext.FileDownloadTicket{}, err
	}
	return s.tickets.Issue(ctx, workspaceID, principal.UserID, principal.AuthorizationRevision, request)
}

func (s *FileCapabilityService) CreateDerived(ctx context.Context, workspaceID string, request runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error) {
	if err := ctx.Err(); err != nil {
		return runtimeext.DerivedFileEvidence{}, err
	}
	workspaceID, idempotencyKey := strings.TrimSpace(workspaceID), strings.TrimSpace(request.IdempotencyKey)
	filename, contentType := strings.TrimSpace(request.Filename), strings.ToLower(strings.TrimSpace(request.ContentType))
	if workspaceID == "" || idempotencyKey == "" || strings.TrimSpace(request.ObjectKey) == "" || strings.TrimSpace(request.FieldKey) == "" || filename == "" || filepath.Base(filename) != filename || !derivedContentTypePattern.MatchString(contentType) || request.Content == nil {
		return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_request_invalid")
	}
	identity := sha256.Sum256([]byte(strings.Join([]string{workspaceID, idempotencyKey, strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.FieldKey)}, "\x00")))
	fileID := "derived_" + hex.EncodeToString(identity[:16])
	stageNonce := [16]byte{}
	if _, err := rand.Read(stageNonce[:]); err != nil {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_storage_unavailable: %w", err)
	}
	staged, err := s.blobs.Stage(ctx, runtimefile.BlobStageRequest{WorkspaceID: workspaceID, StageID: fileID + ":" + hex.EncodeToString(stageNonce[:]), Content: request.Content, MaxBytes: maxDerivedFileBytes})
	if err != nil {
		if errors.Is(err, runtimefile.ErrBlobTooLarge) {
			return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_too_large")
		}
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_read_failed: %w", err)
	}
	defer func() { _ = s.blobs.Delete(context.WithoutCancel(ctx), workspaceID, staged.BlobKey) }()
	digest, size := staged.ContentSHA256, staged.Size
	extension := filepath.Ext(filename)
	filenameDigest := sha256.Sum256([]byte(filename))
	storageName := digest + "-" + hex.EncodeToString(identity[16:24]) + "-" + hex.EncodeToString(filenameDigest[:4]) + extension
	existing, lookupErr := s.store.FindFileScan(ctx, workspaceID, fileID)
	if lookupErr == nil {
		if existing.Filename != storageName || existing.ContentType != contentType || existing.ObjectKey != strings.TrimSpace(request.ObjectKey) || existing.FieldKey != strings.TrimSpace(request.FieldKey) || existing.Size != size || !strings.EqualFold(existing.SHA256, digest) {
			return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_idempotency_conflict")
		}
		current, statErr := s.blobs.Stat(ctx, workspaceID, existing.Filename)
		if statErr != nil {
			return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_unavailable: %w", statErr)
		}
		if current.Size != size || !strings.EqualFold(current.ContentSHA256, digest) {
			return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_identity_mismatch")
		}
		if existing.Status == lifecyclecontract.FileScanClean {
			return s.derivedFileResult(ctx, workspaceID, filename, contentType, existing)
		}
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return runtimeext.DerivedFileEvidence{}, lookupErr
	}
	if _, err := s.blobs.Commit(ctx, runtimefile.BlobCommitRequest{
		WorkspaceID: workspaceID, StageKey: staged.BlobKey, BlobKey: storageName, ContentSHA256: digest, Size: size,
	}); err != nil {
		if errors.Is(err, runtimefile.ErrBlobIdentityConflict) {
			return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_idempotency_conflict")
		}
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_save_failed: %w", err)
	}
	now := s.clock().UTC()
	artifact := lifecyclecontract.UploadArtifact{ID: fileID, WorkspaceID: workspaceID, ObjectKey: strings.TrimSpace(request.ObjectKey), FieldKey: strings.TrimSpace(request.FieldKey), Filename: storageName, ContentType: contentType, SHA256: digest, Size: size, CreatedAt: now}
	if errors.Is(lookupErr, sql.ErrNoRows) {
		if err := s.store.RegisterUpload(ctx, artifact); err != nil {
			return runtimeext.DerivedFileEvidence{}, err
		}
	}
	scan := lifecyclecontract.FileScanEvidence{FileID: fileID, WorkspaceID: workspaceID, Filename: storageName, ContentType: contentType, ObjectKey: artifact.ObjectKey, FieldKey: artifact.FieldKey, SHA256: digest, Size: size, Status: lifecyclecontract.FileScanClean, Provider: "domainry-derived-file-v1", EvidenceRef: "derived:sha256:" + digest, ScannedAt: now}
	if err := s.store.RecordFileScan(ctx, scan); err != nil {
		return runtimeext.DerivedFileEvidence{}, err
	}
	return s.derivedFileResult(ctx, workspaceID, filename, contentType, scan)
}

func (s *FileCapabilityService) derivedFileResult(ctx context.Context, workspaceID, filename, contentType string, scan lifecyclecontract.FileScanEvidence) (runtimeext.DerivedFileEvidence, error) {
	verified, err := s.verifier.Status(ctx, workspaceID, scan.FileID)
	if err != nil {
		return runtimeext.DerivedFileEvidence{}, err
	}
	return runtimeext.DerivedFileEvidence{
		FileVerificationEvidence: runtimeext.FileVerificationEvidence{FileID: verified.FileID, ContentSHA256: verified.SHA256, Filename: verified.Filename, ContentType: verified.ContentType, Size: verified.Size, Status: verified.Status, Provider: verified.Provider, EvidenceRef: verified.EvidenceRef, ScanReceipt: verified.Receipt},
		Filename:                 filename, ContentType: contentType, ProtectedDownload: "/uploads/" + verified.Filename,
	}, nil
}
