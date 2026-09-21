package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
)

type durableFileScanStoreStub struct {
	pending  []lifecyclecontract.FileScanEvidence
	recorded []lifecyclecontract.FileScanEvidence
}

type fileScannerStub struct {
	result runtimefile.FileScanResult
	err    error
	read   int
}

func (*fileScannerStub) Descriptor() runtimefile.AdapterDescriptor {
	return runtimefile.AdapterDescriptor{Provider: "test-scanner", Revision: "v1"}
}

func (s *fileScannerStub) Scan(_ context.Context, _ runtimefile.FileScanRequest, reader io.Reader) (runtimefile.FileScanResult, error) {
	buffer := make([]byte, s.read)
	if s.read > 0 {
		_, _ = io.ReadFull(reader, buffer)
	}
	return s.result, s.err
}

func (s *durableFileScanStoreStub) FindFileScan(context.Context, string, string) (lifecyclecontract.FileScanEvidence, error) {
	return lifecyclecontract.FileScanEvidence{}, nil
}

func (s *durableFileScanStoreStub) RecordFileScan(_ context.Context, evidence lifecyclecontract.FileScanEvidence) error {
	s.recorded = append(s.recorded, evidence)
	return nil
}

func (s *durableFileScanStoreStub) PendingFileScans(context.Context, lifecycleaccess.SystemScope, int) ([]lifecyclecontract.FileScanEvidence, error) {
	return append([]lifecyclecontract.FileScanEvidence(nil), s.pending...), nil
}

func TestFileScanProcessorRecordsCleanEvidenceForExactPNG(t *testing.T) {
	content := []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00,
	}
	now := time.Date(2026, 9, 12, 9, 30, 0, 0, time.UTC)
	store := &durableFileScanStoreStub{pending: []lifecyclecontract.FileScanEvidence{
		fileScanCandidate("file-1", "workspace-a", "image.png", "image/png", content),
	}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.pending[0] = storeScanCandidate(t, blobs, store.pending[0], content)
	processor, err := NewFileScanProcessor(store, blobs, NewBuiltinFileScanner(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessPending(t.Context(), 25)
	if err != nil || processed != 1 || len(store.recorded) != 1 {
		t.Fatalf("processed=%d recorded=%#v err=%v", processed, store.recorded, err)
	}
	got := store.recorded[0]
	if got.Status != lifecyclecontract.FileScanClean || got.Provider != builtinFileScannerProvider || got.ScannedAt != now || !strings.HasPrefix(got.EvidenceRef, "validated:sha256:") {
		t.Fatalf("clean evidence=%#v", got)
	}
}

func TestFileScanProcessorQuarantinesUnsafeContent(t *testing.T) {
	content := []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")
	store := &durableFileScanStoreStub{pending: []lifecyclecontract.FileScanEvidence{
		fileScanCandidate("file-unsafe", "workspace-a", "payload.txt", "text/plain", content),
	}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.pending[0] = storeScanCandidate(t, blobs, store.pending[0], content)
	processor, err := NewFileScanProcessor(store, blobs, NewBuiltinFileScanner(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessPending(t.Context(), 25); err != nil {
		t.Fatal(err)
	}
	if len(store.recorded) != 1 || store.recorded[0].Status != lifecyclecontract.FileScanQuarantined || !strings.HasPrefix(store.recorded[0].EvidenceRef, "unsafe_signature:sha256:") {
		t.Fatalf("unsafe evidence=%#v", store.recorded)
	}
}

func TestFileScanProcessorFailsWhenStoredContentIdentityChanged(t *testing.T) {
	expected := []byte("expected")
	actual := []byte("changed")
	store := &durableFileScanStoreStub{pending: []lifecyclecontract.FileScanEvidence{
		fileScanCandidate("file-changed", "workspace-a", "data.json", "application/json", expected),
	}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.pending[0] = storeScanCandidate(t, blobs, store.pending[0], actual)
	processor, err := NewFileScanProcessor(store, blobs, NewBuiltinFileScanner(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessPending(t.Context(), 25); err != nil {
		t.Fatal(err)
	}
	if len(store.recorded) != 1 || store.recorded[0].Status != lifecyclecontract.FileScanFailed || !strings.HasPrefix(store.recorded[0].EvidenceRef, "content_identity_changed:sha256:") {
		t.Fatalf("changed evidence=%#v", store.recorded)
	}
}

func TestFileScanProcessorKeepsPendingOnTransientScannerFailure(t *testing.T) {
	content := []byte("content")
	store := &durableFileScanStoreStub{pending: []lifecyclecontract.FileScanEvidence{fileScanCandidate("file-1", "workspace-a", "ignored", "text/plain", content)}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.pending[0] = storeScanCandidate(t, blobs, store.pending[0], content)
	transient := errors.New("scanner unavailable")
	processor, err := NewFileScanProcessor(store, blobs, &fileScannerStub{err: transient}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := processor.ProcessPending(t.Context(), 25); processed != 0 || !errors.Is(err, transient) || len(store.recorded) != 0 {
		t.Fatalf("processed=%d recorded=%v err=%v", processed, store.recorded, err)
	}
}

func TestFileScanProcessorDrainsScannerStreamBeforeIdentityVerification(t *testing.T) {
	content := []byte("complete immutable content")
	store := &durableFileScanStoreStub{pending: []lifecyclecontract.FileScanEvidence{fileScanCandidate("file-1", "workspace-a", "ignored", "text/plain", content)}}
	blobs, err := blobstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.pending[0] = storeScanCandidate(t, blobs, store.pending[0], content)
	scanner := &fileScannerStub{read: 1, result: runtimefile.FileScanResult{Status: lifecyclecontract.FileScanClean, Provider: "external", EvidenceRef: "scan:external:1"}}
	processor, err := NewFileScanProcessor(store, blobs, scanner, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := processor.ProcessPending(t.Context(), 25); err != nil || processed != 1 {
		t.Fatalf("processed=%d err=%v", processed, err)
	}
	if len(store.recorded) != 1 || store.recorded[0].Provider != "external" || store.recorded[0].Status != lifecyclecontract.FileScanClean {
		t.Fatalf("recorded=%+v", store.recorded)
	}
}

func fileScanCandidate(fileID, workspaceID, filename, contentType string, content []byte) lifecyclecontract.FileScanEvidence {
	digest := sha256.Sum256(content)
	return lifecyclecontract.FileScanEvidence{
		FileID: fileID, WorkspaceID: workspaceID, Filename: filename, ContentType: contentType,
		SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content)), Status: lifecyclecontract.FileScanPending,
	}
}

func storeScanCandidate(t *testing.T, blobs runtimefile.BlobStore, candidate lifecyclecontract.FileScanEvidence, content []byte) lifecyclecontract.FileScanEvidence {
	t.Helper()
	staged, err := blobs.Stage(t.Context(), runtimefile.BlobStageRequest{WorkspaceID: candidate.WorkspaceID, StageID: candidate.FileID, Content: strings.NewReader(string(content)), MaxBytes: int64(len(content)) + 1})
	if err != nil {
		t.Fatal(err)
	}
	candidate.Filename = staged.ContentSHA256 + "-" + candidate.FileID + ".bin"
	if _, err := blobs.Commit(t.Context(), runtimefile.BlobCommitRequest{WorkspaceID: candidate.WorkspaceID, StageKey: staged.BlobKey, BlobKey: candidate.Filename, ContentSHA256: staged.ContentSHA256, Size: staged.Size}); err != nil {
		t.Fatal(err)
	}
	return candidate
}
