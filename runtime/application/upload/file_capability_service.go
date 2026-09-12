package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

const maxDerivedFileBytes = 128 << 20

var derivedContentTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$`)

// FileCapabilityService is the Runtime-owned byte boundary used by generated
// project Actions. It never accepts a workspace path or a host filesystem path.
type FileCapabilityService struct {
	store      lifecyclecontract.UploadFileArtifactStore
	verifier   *FileScanReceiptVerifier
	uploadRoot string
	clock      func() time.Time
	tickets    *FileDownloadTicketService
}

func NewFileCapabilityService(store lifecyclecontract.UploadFileArtifactStore, verifier *FileScanReceiptVerifier, uploadRoot string, clock func() time.Time, tickets ...*FileDownloadTicketService) (*FileCapabilityService, error) {
	root, err := filepath.Abs(strings.TrimSpace(uploadRoot))
	if err != nil || strings.TrimSpace(uploadRoot) == "" {
		return nil, fmt.Errorf("file capability upload root is required")
	}
	if store == nil || verifier == nil {
		return nil, fmt.Errorf("file capability artifact store and verifier are required")
	}
	if clock == nil {
		clock = time.Now
	}
	var downloadTickets *FileDownloadTicketService
	if len(tickets) > 0 {
		downloadTickets = tickets[0]
	}
	return &FileCapabilityService{store: store, verifier: verifier, uploadRoot: root, clock: clock, tickets: downloadTickets}, nil
}

func (s *FileCapabilityService) VerifyClean(ctx context.Context, workspaceID string, request runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error) {
	evidence, err := s.verifier.VerifyClean(ctx, workspaceID, request.FileID, request.ContentSHA256, request.ScanReceipt)
	if err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	return runtimeext.FileVerificationEvidence{
		FileID: evidence.FileID, ContentSHA256: evidence.SHA256, Filename: evidence.Filename, ContentType: evidence.ContentType,
		Size: evidence.Size, Status: evidence.Status, Provider: evidence.Provider, EvidenceRef: evidence.EvidenceRef, ScanReceipt: evidence.Receipt,
	}, nil
}

func (s *FileCapabilityService) OpenVerified(ctx context.Context, workspaceID string, request runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error) {
	evidence, err := s.VerifyClean(ctx, workspaceID, request.FileVerificationRequest)
	if err != nil {
		return runtimeext.VerifiedFile{}, err
	}
	registered, err := s.store.FindFileScan(ctx, workspaceID, evidence.FileID)
	if err != nil {
		return runtimeext.VerifiedFile{}, err
	}
	if registered.ObjectKey != strings.TrimSpace(request.Binding.ObjectKey) || registered.FieldKey != strings.TrimSpace(request.Binding.FileIDField) {
		return runtimeext.VerifiedFile{}, errors.New("backend.upload.file_artifact_binding_mismatch")
	}
	path, err := s.artifactPath(workspaceID, evidence.Filename)
	if err != nil {
		return runtimeext.VerifiedFile{}, err
	}
	content, err := os.ReadFile(path)
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
	evidence, err := s.VerifyClean(ctx, workspaceID, request.FileVerificationRequest)
	if err != nil {
		return runtimeext.FileDownloadTicket{}, err
	}
	registered, err := s.store.FindFileScan(ctx, workspaceID, evidence.FileID)
	if err != nil {
		return runtimeext.FileDownloadTicket{}, err
	}
	if registered.ObjectKey != strings.TrimSpace(request.Binding.ObjectKey) || registered.FieldKey != strings.TrimSpace(request.Binding.FileIDField) {
		return runtimeext.FileDownloadTicket{}, errors.New("backend.upload.file_artifact_binding_mismatch")
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
	workspaceDir, err := s.workspaceDirectory(workspaceID)
	if err != nil {
		return runtimeext.DerivedFileEvidence{}, err
	}
	if err := os.MkdirAll(workspaceDir, 0o750); err != nil {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_storage_unavailable: %w", err)
	}
	temporary, err := os.CreateTemp(workspaceDir, ".derived-*")
	if err != nil {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_storage_unavailable: %w", err)
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(request.Content, maxDerivedFileBytes+1))
	if err != nil {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_read_failed: %w", err)
	}
	if size > maxDerivedFileBytes {
		return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_too_large")
	}
	if err := temporary.Sync(); err != nil {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_save_failed: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_save_failed: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	identity := sha256.Sum256([]byte(strings.Join([]string{workspaceID, idempotencyKey, strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.FieldKey)}, "\x00")))
	fileID := "derived_" + hex.EncodeToString(identity[:16])
	extension := filepath.Ext(filename)
	filenameDigest := sha256.Sum256([]byte(filename))
	storageName := digest[:24] + "-" + hex.EncodeToString(identity[16:24]) + "-" + hex.EncodeToString(filenameDigest[:4]) + extension
	existing, lookupErr := s.store.FindFileScan(ctx, workspaceID, fileID)
	if lookupErr == nil {
		if existing.Filename != storageName || existing.ContentType != contentType || existing.ObjectKey != strings.TrimSpace(request.ObjectKey) || existing.FieldKey != strings.TrimSpace(request.FieldKey) || existing.Size != size || !strings.EqualFold(existing.SHA256, digest) {
			return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_idempotency_conflict")
		}
		path, pathErr := s.artifactPath(workspaceID, existing.Filename)
		if pathErr != nil {
			return runtimeext.DerivedFileEvidence{}, pathErr
		}
		current, readErr := os.ReadFile(path)
		if readErr != nil {
			return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_unavailable: %w", readErr)
		}
		currentDigest := sha256.Sum256(current)
		if int64(len(current)) != size || hex.EncodeToString(currentDigest[:]) != digest {
			return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_identity_mismatch")
		}
		if existing.Status == lifecyclecontract.FileScanClean {
			return s.derivedFileResult(ctx, workspaceID, filename, contentType, existing)
		}
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return runtimeext.DerivedFileEvidence{}, lookupErr
	}
	path := filepath.Join(workspaceDir, storageName)
	if current, readErr := os.ReadFile(path); readErr == nil {
		currentDigest := sha256.Sum256(current)
		if int64(len(current)) != size || hex.EncodeToString(currentDigest[:]) != digest {
			return runtimeext.DerivedFileEvidence{}, errors.New("backend.upload.derived_file_idempotency_conflict")
		}
		_ = os.Remove(temporaryPath)
	} else if !os.IsNotExist(readErr) {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_save_failed: %w", readErr)
	} else if err := os.Rename(temporaryPath, path); err != nil {
		return runtimeext.DerivedFileEvidence{}, fmt.Errorf("backend.upload.derived_file_save_failed: %w", err)
	}
	keep = true
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

func (s *FileCapabilityService) artifactPath(workspaceID, filename string) (string, error) {
	if filename = strings.TrimSpace(filename); filename == "" || filepath.Base(filename) != filename {
		return "", errors.New("backend.upload.file_identity_invalid")
	}
	directory, err := s.workspaceDirectory(workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, filename), nil
}

func (s *FileCapabilityService) workspaceDirectory(workspaceID string) (string, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return "", errors.New("backend.workspace_scope_required")
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID)))
	return filepath.Join(s.uploadRoot, "workspace-"+hex.EncodeToString(digest[:16])), nil
}
