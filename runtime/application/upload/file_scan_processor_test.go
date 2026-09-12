package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

type durableFileScanStoreStub struct {
	pending  []lifecyclecontract.FileScanEvidence
	recorded []lifecyclecontract.FileScanEvidence
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
	processor, err := NewFileScanProcessor(store, t.TempDir(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	writeScanCandidate(t, processor, store.pending[0], content)
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
	processor, err := NewFileScanProcessor(store, t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	writeScanCandidate(t, processor, store.pending[0], content)
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
	processor, err := NewFileScanProcessor(store, t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	writeScanCandidate(t, processor, store.pending[0], actual)
	if _, err := processor.ProcessPending(t.Context(), 25); err != nil {
		t.Fatal(err)
	}
	if len(store.recorded) != 1 || store.recorded[0].Status != lifecyclecontract.FileScanFailed || !strings.HasPrefix(store.recorded[0].EvidenceRef, "content_identity_changed:sha256:") {
		t.Fatalf("changed evidence=%#v", store.recorded)
	}
}

func fileScanCandidate(fileID, workspaceID, filename, contentType string, content []byte) lifecyclecontract.FileScanEvidence {
	digest := sha256.Sum256(content)
	return lifecyclecontract.FileScanEvidence{
		FileID: fileID, WorkspaceID: workspaceID, Filename: filename, ContentType: contentType,
		SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content)), Status: lifecyclecontract.FileScanPending,
	}
}

func writeScanCandidate(t *testing.T, processor *FileScanProcessor, candidate lifecyclecontract.FileScanEvidence, content []byte) {
	t.Helper()
	path, err := processor.path(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
