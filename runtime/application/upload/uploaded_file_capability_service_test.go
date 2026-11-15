package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
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

type uploadedFileStore struct{ *fileCapabilityStoreStub }

func (s uploadedFileStore) PendingFileScans(_ context.Context, _ lifecycleaccess.SystemScope, limit int) ([]lifecyclecontract.FileScanEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []lifecyclecontract.FileScanEvidence{}
	for _, evidence := range s.evidence {
		if evidence.Status == lifecyclecontract.FileScanPending {
			result = append(result, evidence)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func incomingFixture(t *testing.T) (*FileCapabilityService, uploadedFileStore, *FileScanProcessor, principalmodel.Principal, recordmodel.RecordFileFieldPolicy) {
	t.Helper()
	store := uploadedFileStore{&fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.UnixMilli(1794640000123) }
	service, err := NewFileCapabilityService(store, NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32)), blobs, clock)
	if err != nil {
		t.Fatal(err)
	}
	service.BindUploadSubjects(NewUploadSubjectRegistry(uploadSubjectMemory{}))
	processor, err := NewFileScanProcessor(store, blobs, NewBuiltinFileScanner(), clock)
	if err != nil {
		t.Fatal(err)
	}
	owner := uploadAccessPrincipal("asset.ingest") // no ordinary asset create/update grant
	policy, err := recordmodel.RecordFileFieldPolicyFor(definitionmodel.FieldSchema{Key: "attachment", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"allowed_mime_types": []any{"text/plain"}}})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, processor, owner, policy
}
func incomingRequest(content string) runtimeext.UploadedFileRequest {
	digest := sha256.Sum256([]byte(content))
	return runtimeext.UploadedFileRequest{IdempotencyKey: "execution:attachment-1", ObjectKey: "asset", FieldKey: "attachment", Filename: "customer-log.txt", ContentType: "text/plain", ContentSHA256: hex.EncodeToString(digest[:]), Size: int64(len(content)), Content: strings.NewReader(content)}
}

func TestIncomingUploadRequiresRealScanBeforeRecordBinding(t *testing.T) {
	service, store, processor, owner, policy := incomingFixture(t)
	upload, err := service.IngestUploaded(t.Context(), owner.WorkspaceID, owner, incomingRequest("customer log"), policy)
	if err != nil || upload.Status != lifecyclecontract.FileScanPending || upload.ScanReceipt != "" || store.registerCalls != 1 {
		t.Fatalf("upload fabricated a verdict: %+v %v", upload, err)
	}
	if _, err := service.VerifyClean(t.Context(), owner.WorkspaceID, runtimeext.FileVerificationRequest{FileID: upload.FileID, ContentSHA256: upload.ContentSHA256}); err == nil {
		t.Fatal("pending bytes were accepted as clean")
	}
	if count, err := processor.ProcessPending(t.Context(), 10); err != nil || count != 1 {
		t.Fatalf("scan count=%d err=%v", count, err)
	}
	inspection := runtimeext.UploadedFileInspection{FileID: upload.FileID, ObjectKey: "asset", FieldKey: "attachment"}
	clean, err := service.InspectUploaded(t.Context(), owner.WorkspaceID, owner, inspection)
	if err != nil || clean.Status != lifecyclecontract.FileScanClean || clean.ScanReceipt == "" || clean.Provider != builtinFileScannerProvider {
		t.Fatalf("scan evidence missing %+v %v", clean, err)
	}
	reference := recordmodel.RecordFileReference{FileID: clean.FileID, Filename: clean.Filename, ContentType: clean.ContentType, Size: clean.Size, ContentSHA256: clean.ContentSHA256, ScanReceipt: clean.ScanReceipt}
	if err := service.VerifyRecordReference(t.Context(), owner.WorkspaceID, owner, "asset", "attachment", reference, true); err != nil {
		t.Fatal(err)
	}
	replay, err := service.IngestUploaded(t.Context(), owner.WorkspaceID, owner, incomingRequest("customer log"), policy)
	if err != nil || replay.FileID != upload.FileID || replay.Status != lifecyclecontract.FileScanClean || store.registerCalls != 1 {
		t.Fatalf("retry reset scan verdict %+v %v", replay, err)
	}
	changed := incomingRequest("changed log")
	if _, err := service.IngestUploaded(t.Context(), owner.WorkspaceID, owner, changed, policy); apperror.CodeOf(err) != "backend.upload.ingest_idempotency_conflict" {
		t.Fatalf("changed upload replaced immutable content %v", err)
	}
	for _, change := range []string{"user", "workspace", "field"} {
		peer := owner
		request := inspection
		workspace := owner.WorkspaceID
		switch change {
		case "user":
			peer.UserID = "peer"
		case "workspace":
			workspace = "other"
			peer.WorkspaceID = workspace
		case "field":
			request.FieldKey = "other"
		}
		if result, err := service.InspectUploaded(t.Context(), workspace, peer, request); err == nil || result.FileID != "" {
			t.Fatalf("foreign scan evidence escaped (%s): %+v %v", change, result, err)
		}
	}
}

func TestIncomingUploadQuarantineIsNotRewrittenByRetry(t *testing.T) {
	for _, content := range []string{"EICAR-STANDARD-ANTIVIRUS-TEST-FILE", "MZ executable"} {
		service, store, processor, owner, policy := incomingFixture(t)
		upload, err := service.IngestUploaded(t.Context(), owner.WorkspaceID, owner, incomingRequest(content), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := processor.ProcessPending(t.Context(), 10); err != nil {
			t.Fatal(err)
		}
		replay, err := service.IngestUploaded(t.Context(), owner.WorkspaceID, owner, incomingRequest(content), policy)
		if err != nil || replay.Status != lifecyclecontract.FileScanQuarantined || replay.ScanReceipt != "" || store.registerCalls != 1 {
			t.Fatalf("quarantine bypassed: %+v %v", replay, err)
		}
		if _, err := service.VerifyClean(t.Context(), owner.WorkspaceID, runtimeext.FileVerificationRequest{FileID: upload.FileID, ContentSHA256: upload.ContentSHA256}); err == nil {
			t.Fatal("unsafe content obtained clean evidence")
		}
	}
}

func TestIncomingUploadRejectsForgedIdentityAndOutOfPolicyBytes(t *testing.T) {
	for _, mutation := range []func(*runtimeext.UploadedFileRequest, *recordmodel.RecordFileFieldPolicy){
		func(r *runtimeext.UploadedFileRequest, _ *recordmodel.RecordFileFieldPolicy) { r.Size++ },
		func(r *runtimeext.UploadedFileRequest, _ *recordmodel.RecordFileFieldPolicy) {
			r.ContentSHA256 = strings.Repeat("a", 64)
		},
		func(r *runtimeext.UploadedFileRequest, _ *recordmodel.RecordFileFieldPolicy) {
			r.Filename = "../log.txt"
		},
		func(r *runtimeext.UploadedFileRequest, _ *recordmodel.RecordFileFieldPolicy) {
			r.Filename = "bad\\log.txt"
		},
		func(r *runtimeext.UploadedFileRequest, _ *recordmodel.RecordFileFieldPolicy) { r.Content = nil },
		func(_ *runtimeext.UploadedFileRequest, p *recordmodel.RecordFileFieldPolicy) { p.ScanRequired = false },
		func(_ *runtimeext.UploadedFileRequest, p *recordmodel.RecordFileFieldPolicy) { p.MaxSizeBytes = 1 },
		func(r *runtimeext.UploadedFileRequest, _ *recordmodel.RecordFileFieldPolicy) {
			r.ContentType = "application/x-executable"
		},
	} {
		service, store, _, owner, policy := incomingFixture(t)
		request := incomingRequest("content")
		mutation(&request, &policy)
		if _, err := service.IngestUploaded(t.Context(), owner.WorkspaceID, owner, request, policy); err == nil || store.registerCalls != 0 {
			t.Fatalf("invalid content entered the scan queue: %v", err)
		}
	}
}

func TestIncomingUploadKeepsPendingWhenScannerUnavailable(t *testing.T) {
	service, store, processor, owner, policy := incomingFixture(t)
	upload, err := service.IngestUploaded(t.Context(), owner.WorkspaceID, owner, incomingRequest("customer log"), policy)
	if err != nil {
		t.Fatal(err)
	}
	transient := errors.New("scanner unavailable")
	failing, err := NewFileScanProcessor(store, service.blobs, &fileScannerStub{err: transient}, service.clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.ProcessPending(t.Context(), 10); !errors.Is(err, transient) {
		t.Fatalf("scanner failure hidden: %v", err)
	}
	pending, err := service.InspectUploaded(t.Context(), owner.WorkspaceID, owner, runtimeext.UploadedFileInspection{FileID: upload.FileID, ObjectKey: "asset", FieldKey: "attachment"})
	if err != nil || pending.Status != lifecyclecontract.FileScanPending || pending.ScanReceipt != "" {
		t.Fatalf("scanner outage fabricated success: %+v %v", pending, err)
	}
	if _, err := processor.ProcessPending(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
}
