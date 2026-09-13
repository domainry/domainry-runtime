package upload

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

type fileCapabilityStoreStub struct {
	evidence      map[string]lifecyclecontract.FileScanEvidence
	registerCalls int
}

func (s *fileCapabilityStoreStub) RegisterUpload(_ context.Context, artifact lifecyclecontract.UploadArtifact) error {
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
	evidence, ok := s.evidence[workspaceID+"\x00"+fileID]
	if !ok {
		return lifecyclecontract.FileScanEvidence{}, sql.ErrNoRows
	}
	return evidence, nil
}

func (s *fileCapabilityStoreStub) RecordFileScan(_ context.Context, evidence lifecyclecontract.FileScanEvidence) error {
	s.evidence[evidence.WorkspaceID+"\x00"+evidence.FileID] = evidence
	return nil
}

func TestFileCapabilityCreateDerivedIsImmutableAndIdempotent(t *testing.T) {
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), t.TempDir(), func() time.Time {
		return time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeext.DerivedFileRequest{
		IdempotencyKey: "import-1/page-1", ObjectKey: "document_version", FieldKey: "file_id",
		Filename: "請求書-1.pdf", ContentType: "application/pdf", Content: bytes.NewReader([]byte("derived-pdf")),
	}
	first, err := service.CreateDerived(t.Context(), "workspace-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if first.FileID == "" || first.ScanReceipt == "" || first.Status != lifecyclecontract.FileScanClean || first.ProtectedDownload == "" || store.registerCalls != 1 {
		t.Fatalf("first=%+v register calls=%d", first, store.registerCalls)
	}
	request.Content = bytes.NewReader([]byte("derived-pdf"))
	second, err := service.CreateDerived(t.Context(), "workspace-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if second.FileID != first.FileID || second.ContentSHA256 != first.ContentSHA256 || second.ProtectedDownload != first.ProtectedDownload || store.registerCalls != 1 {
		t.Fatalf("first=%+v second=%+v register calls=%d", first, second, store.registerCalls)
	}
	request.Content = bytes.NewReader([]byte("different"))
	if _, err := service.CreateDerived(t.Context(), "workspace-a", request); err == nil || err.Error() != "backend.upload.derived_file_idempotency_conflict" {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestFileCapabilityOpenVerifiedRehashesStoredBytes(t *testing.T) {
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateDerived(t.Context(), "workspace-a", runtimeext.DerivedFileRequest{
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
	path, err := service.artifactPath("workspace-a", created.FileVerificationEvidence.Filename)
	if err != nil {
		t.Fatal(err)
	}
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
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), t.TempDir(), func() time.Time { return now }, tickets)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateDerived(t.Context(), "workspace-a", runtimeext.DerivedFileRequest{
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
