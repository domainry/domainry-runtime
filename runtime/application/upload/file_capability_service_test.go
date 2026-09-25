package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
)

type fileCapabilityStoreStub struct {
	mu            sync.Mutex
	evidence      map[string]lifecyclecontract.FileScanEvidence
	registerCalls int
}

func derivedFileSystemPrincipal() principalmodel.Principal {
	return principalmodel.NewSystemPrincipal("derived-file-test", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test derived files"))
}

func TestFileCapabilityValidatesRecordReferenceAgainstUploadSubjectAndCleanEvidence(t *testing.T) {
	owner := uploadAccessPrincipal("asset.create")
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	verifier := NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32))
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewFileCapabilityService(store, verifier, blobs, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewUploadSubjectRegistry(uploadSubjectMemory{})
	service.BindUploadSubjects(registry)
	artifact := lifecyclecontract.UploadArtifact{
		ID: "file-1", WorkspaceID: owner.WorkspaceID, ObjectKey: "asset", FieldKey: "attachment", Filename: "stored.pdf",
		ContentType: "application/pdf", SHA256: strings.Repeat("a", 64), Size: 7, CreatedAt: time.Now().UTC(),
	}
	if err := registry.Register(t.Context(), artifact, owner); err != nil {
		t.Fatal(err)
	}
	evidence := lifecyclecontract.FileScanEvidence{
		FileID: artifact.ID, WorkspaceID: artifact.WorkspaceID, ObjectKey: artifact.ObjectKey, FieldKey: artifact.FieldKey,
		Filename: artifact.Filename, ContentType: artifact.ContentType, SHA256: artifact.SHA256, Size: artifact.Size,
		Status: lifecyclecontract.FileScanClean, Provider: "test", EvidenceRef: "scan-1", ScannedAt: time.Now().UTC(),
	}
	store.evidence[owner.WorkspaceID+"\x00"+artifact.ID] = evidence
	clean, err := verifier.Status(t.Context(), owner.WorkspaceID, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "asset", Fields: []definitionmodel.FieldSchema{{
		Key: "attachment", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"allowed_mime_types": []any{"application/pdf"}},
	}}}
	values := map[string]any{"attachment": map[string]any{
		"file_id": artifact.ID, "filename": artifact.Filename, "content_type": artifact.ContentType, "size": artifact.Size,
		"content_sha256": artifact.SHA256, "scan_receipt": clean.Receipt,
	}}
	if err := service.ValidateRecordReferences(t.Context(), object, values, owner); err != nil {
		t.Fatal(err)
	}
	peer := owner
	peer.UserID = "peer"
	if err := service.ValidateRecordReferences(t.Context(), object, values, peer); apperror.CodeOf(err) != "backend.upload.subject_binding_denied" {
		t.Fatalf("foreign uploader error=%v", err)
	}
	forged := definitionmodel.ObjectSchema{Key: "other", Fields: object.Fields}
	if err := service.ValidateRecordReferences(t.Context(), forged, values, owner); apperror.CodeOf(err) != "backend.upload.file_identity_mismatch" {
		t.Fatalf("forged object binding error=%v", err)
	}
	store.evidence[owner.WorkspaceID+"\x00"+artifact.ID] = lifecyclecontract.FileScanEvidence{
		FileID: artifact.ID, WorkspaceID: artifact.WorkspaceID, ObjectKey: artifact.ObjectKey, FieldKey: artifact.FieldKey,
		Filename: artifact.Filename, ContentType: artifact.ContentType, SHA256: artifact.SHA256, Size: artifact.Size, Status: lifecyclecontract.FileScanPending,
	}
	if err := service.ValidateRecordReferences(t.Context(), object, values, owner); apperror.CodeOf(err) != "backend.upload.scan_not_clean" {
		t.Fatalf("pending scan error=%v", err)
	}
}

func TestFileCapabilityVerifyCleanFailsClosedWithoutSubjectRegistry(t *testing.T) {
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), blobs, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	principal := uploadAccessPrincipal("asset.create")
	_, err = service.VerifyClean(WithUploadClaimPrincipal(t.Context(), principal), principal.WorkspaceID, runtimeext.FileVerificationRequest{FileID: "file-1"})
	if apperror.CodeOf(err) != "backend.upload.subject_binding_denied" {
		t.Fatalf("missing subject registry error=%v", err)
	}
}

func (s *fileCapabilityStoreStub) RegisterUpload(_ context.Context, artifact lifecyclecontract.UploadArtifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerCalls++
	s.evidence[artifact.WorkspaceID+"\x00"+artifact.ID] = lifecyclecontract.FileScanEvidence{
		FileID: artifact.ID, WorkspaceID: artifact.WorkspaceID, Filename: artifact.Filename, ContentType: artifact.ContentType,
		ObjectKey: artifact.ObjectKey, FieldKey: artifact.FieldKey, SHA256: artifact.SHA256, Size: artifact.Size, Status: lifecyclecontract.FileScanPending,
	}
	return nil
}

func (*fileCapabilityStoreStub) ReconcileUploadArtifacts(context.Context, lifecycleaccess.SystemScope, time.Time, int) (lifecyclecontract.UploadCleanupResult, error) {
	return lifecyclecontract.UploadCleanupResult{}, nil
}

func (s *fileCapabilityStoreStub) FindFileScan(_ context.Context, workspaceID, fileID string) (lifecyclecontract.FileScanEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	evidence, ok := s.evidence[workspaceID+"\x00"+fileID]
	if !ok {
		return lifecyclecontract.FileScanEvidence{}, sql.ErrNoRows
	}
	return evidence, nil
}

func (s *fileCapabilityStoreStub) RecordFileScan(_ context.Context, evidence lifecyclecontract.FileScanEvidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidence[evidence.WorkspaceID+"\x00"+evidence.FileID] = evidence
	return nil
}

func TestFileCapabilityCreateDerivedIsImmutableAndIdempotent(t *testing.T) {
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	blobRoot := t.TempDir()
	blobs, err := blobstore.NewLocalStore(blobRoot)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), blobs, func() time.Time {
		return time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeext.DerivedFileRequest{
		IdempotencyKey: "import-1/page-1", ObjectKey: "document_version", FieldKey: "file_id",
		Filename: "請求書-1.pdf", ContentType: "application/pdf", Content: bytes.NewReader([]byte("derived-pdf")),
	}
	first, err := service.CreateDerived(t.Context(), "workspace-a", derivedFileSystemPrincipal(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.FileID == "" || first.ScanReceipt == "" || first.Status != lifecyclecontract.FileScanClean || first.ProtectedDownload == "" || store.registerCalls != 1 {
		t.Fatalf("first=%+v register calls=%d", first, store.registerCalls)
	}
	request.Content = bytes.NewReader([]byte("derived-pdf"))
	second, err := service.CreateDerived(t.Context(), "workspace-a", derivedFileSystemPrincipal(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.FileID != first.FileID || second.ContentSHA256 != first.ContentSHA256 || second.ProtectedDownload != first.ProtectedDownload || store.registerCalls != 1 {
		t.Fatalf("first=%+v second=%+v register calls=%d", first, second, store.registerCalls)
	}
	request.Content = bytes.NewReader([]byte("different"))
	if _, err := service.CreateDerived(t.Context(), "workspace-a", derivedFileSystemPrincipal(), request); err == nil || err.Error() != "backend.upload.derived_file_idempotency_conflict" {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestFileCapabilityCreateDerivedRegistersTheActionPrincipal(t *testing.T) {
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), blobs, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	subjects := uploadSubjectMemory{}
	service.BindUploadSubjects(NewUploadSubjectRegistry(subjects))
	owner := uploadAccessPrincipal("asset.create")
	request := runtimeext.DerivedFileRequest{
		IdempotencyKey: "attachment-1", ObjectKey: "asset", FieldKey: "file_url",
		Filename: "source.txt", ContentType: "text/plain", Content: strings.NewReader("source"),
	}
	created, err := service.CreateDerived(t.Context(), owner.WorkspaceID, owner, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Content = strings.NewReader("source")
	if _, err := service.CreateDerived(t.Context(), owner.WorkspaceID, owner, request); err != nil {
		t.Fatalf("idempotent replay lost the subject binding: %v", err)
	}
	object := definitionmodel.ObjectSchema{Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"scan_required": true}}}}
	values := map[string]any{"file_url": map[string]any{
		"file_id": created.FileID, "filename": created.FileVerificationEvidence.Filename, "content_type": created.FileVerificationEvidence.ContentType,
		"size": created.Size, "content_sha256": created.ContentSHA256, "scan_receipt": created.ScanReceipt,
	}}
	if err := service.ValidateRecordReferences(t.Context(), object, values, owner); err != nil {
		t.Fatal(err)
	}
	peer := owner
	peer.UserID = "peer"
	if err := service.ValidateRecordReferences(t.Context(), object, values, peer); apperror.CodeOf(err) != "backend.upload.subject_binding_denied" {
		t.Fatalf("foreign principal validation error=%v", err)
	}
}

func TestFileCapabilityCreateDerivedSupportsConcurrentIdempotentReplay(t *testing.T) {
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), blobs, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan runtimeext.DerivedFileEvidence, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, createErr := service.CreateDerived(t.Context(), "workspace-a", derivedFileSystemPrincipal(), runtimeext.DerivedFileRequest{
				IdempotencyKey: "import-1/page-1", ObjectKey: "document_version", FieldKey: "file_id",
				Filename: "page.pdf", ContentType: "application/pdf", Content: bytes.NewReader([]byte("same-content")),
			})
			results <- result
			errors <- createErr
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for createErr := range errors {
		if createErr != nil {
			t.Fatalf("concurrent replay failed: %v", createErr)
		}
	}
	var first runtimeext.DerivedFileEvidence
	for result := range results {
		if first.FileID == "" {
			first = result
			continue
		}
		if result.FileID != first.FileID || result.ContentSHA256 != first.ContentSHA256 || result.ScanReceipt != first.ScanReceipt {
			t.Fatalf("concurrent results differ: first=%+v result=%+v", first, result)
		}
	}
}

func TestFileCapabilityOpenVerifiedRehashesStoredBytes(t *testing.T) {
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	blobRoot := t.TempDir()
	blobs, err := blobstore.NewLocalStore(blobRoot)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), blobs, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateDerived(t.Context(), "workspace-a", derivedFileSystemPrincipal(), runtimeext.DerivedFileRequest{
		IdempotencyKey: "preview-1", ObjectKey: "document_upload", FieldKey: "file_url", Filename: "preview.png", ContentType: "image/png", Content: bytes.NewReader([]byte("png-content")),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeext.VerifiedFileRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{FileID: created.FileID, ContentSHA256: created.ContentSHA256, ScanReceipt: created.ScanReceipt},
		Binding:                 runtimeext.FileRecordBinding{ObjectKey: "document_source_file", RecordID: "source-1", FileIDField: "runtime_file_id"},
	}
	opened, err := service.OpenVerified(t.Context(), "workspace-a", request)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(opened.Content)
	_ = opened.Content.Close()
	if err != nil || string(content) != "png-content" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	workspaceDigest := sha256.Sum256([]byte("workspace-a"))
	path := filepath.Join(blobRoot, "workspace-"+hex.EncodeToString(workspaceDigest[:16]), created.FileVerificationEvidence.Filename)
	if err := os.WriteFile(path, []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenVerified(t.Context(), "workspace-a", request); err == nil || err.Error() != "backend.upload.file_identity_mismatch" {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
}

func TestFileCapabilityIssuesTicketForActionAuthorizedRecordBinding(t *testing.T) {
	now := time.Date(2026, 9, 13, 4, 5, 6, 0, time.UTC)
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	tickets, err := NewFileDownloadTicketService(bytes.Repeat([]byte("t"), 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), blobs, func() time.Time { return now }, tickets)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateDerived(t.Context(), "workspace-a", derivedFileSystemPrincipal(), runtimeext.DerivedFileRequest{
		IdempotencyKey: "file-1", ObjectKey: "document_upload", FieldKey: "file_url", Filename: "file.pdf", ContentType: "application/pdf", Content: bytes.NewReader([]byte("pdf")),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeext.FileDownloadRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{FileID: created.FileID, ContentSHA256: created.ContentSHA256, ScanReceipt: created.ScanReceipt},
		Binding:                 runtimeext.FileRecordBinding{ObjectKey: "document_file_version", RecordID: "version-1", FileIDField: "runtime_file_id"},
	}
	ticket, err := service.IssueDownload(t.Context(), "workspace-a", runtimeext.Principal{UserID: "user-a", AuthorizationRevision: "auth-1", Known: true}, request)
	if err != nil || ticket.ProtectedDownload == "" || ticket.ExpiresAt != now.Add(fileDownloadTicketLifetime) {
		t.Fatalf("ticket=%+v err=%v", ticket, err)
	}
	request.ScanReceipt = "forged"
	if _, err := service.IssueDownload(t.Context(), "workspace-a", runtimeext.Principal{UserID: "user-a", Known: true}, request); !errors.Is(err, ErrFileScanReceiptInvalid) {
		t.Fatalf("scan receipt mismatch err=%v", err)
	}
}
