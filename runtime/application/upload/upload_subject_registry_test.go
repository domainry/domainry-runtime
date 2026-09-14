package upload

import (
	"bytes"
	"context"
	"database/sql"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"testing"
	"time"
)

type uploadSubjectMemory map[string]UploadSubjectBinding

func (s uploadSubjectMemory) InsertUploadSubject(_ context.Context, value UploadSubjectBinding) error {
	s[value.WorkspaceID+"\x00"+value.FileID] = value
	return nil
}
func (s uploadSubjectMemory) FindUploadSubject(_ context.Context, workspace, id string) (UploadSubjectBinding, error) {
	for _, value := range s {
		if value.WorkspaceID == workspace && (value.FileID == id || value.Filename == id) {
			return value, nil
		}
	}
	return UploadSubjectBinding{}, sql.ErrNoRows
}

func TestUploadSubjectBindingRejectsForeignClaimsAndForgedRecordReferences(t *testing.T) {
	owner := uploadAccessPrincipal("asset.read", "asset.create")
	registry := NewUploadSubjectRegistry(uploadSubjectMemory{})
	artifact := lifecyclecontract.UploadArtifact{ID: "file-1", WorkspaceID: owner.WorkspaceID, ObjectKey: "asset", FieldKey: "file_url", Filename: "avatar.png", SHA256: "hash"}
	if err := registry.Register(t.Context(), artifact, owner); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"file-1", "avatar.png"} {
		if err := registry.Authorize(t.Context(), owner.WorkspaceID, owner.UserID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Authorize(t.Context(), owner.WorkspaceID, "peer", "avatar.png"); err == nil {
		t.Fatal("foreign upload claim allowed")
	}
	if err := registry.Authorize(t.Context(), "other-workspace", owner.UserID, "file-1"); err == nil {
		t.Fatal("cross-workspace upload claim allowed")
	}
	records := &uploadAccessRecordStub{record: recordmodel.Record{OwnerUserID: owner.UserID, Data: map[string]any{"file_url": "/uploads/avatar.png"}}}
	service := NewUploadAccessApplicationService(uploadAccessCatalog(), &uploadAccessAuditStub{}, records, registry)
	if err := service.AuthorizeDownload(t.Context(), "asset", "file_url", "row-1", "avatar.png", owner); err != nil {
		t.Fatal(err)
	}
	if err := service.AuthorizeDownload(t.Context(), "asset", "file_url", "", "avatar.png", owner); err == nil {
		t.Fatal("empty record context allowed")
	}
	records.record.OwnerUserID = "peer"
	if err := service.AuthorizeDownload(t.Context(), "asset", "file_url", "forged-own-row", "avatar.png", owner); err == nil {
		t.Fatal("editable URL re-bound a foreign upload")
	}
}

func TestFileCleanClaimRequiresAuthenticatedUploaderDespiteValidScanReceipt(t *testing.T) {
	owner := uploadAccessPrincipal("asset.create")
	store := &fileCapabilityStoreStub{evidence: map[string]lifecyclecontract.FileScanEvidence{}}
	verifier := NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32))
	service, err := NewFileCapabilityService(store, verifier, t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewUploadSubjectRegistry(uploadSubjectMemory{})
	service.BindUploadSubjects(registry)
	artifact := lifecyclecontract.UploadArtifact{ID: "file-1", WorkspaceID: owner.WorkspaceID, ObjectKey: "asset", FieldKey: "file_url", Filename: "avatar.png", SHA256: "hash", Size: 1}
	if err = registry.Register(t.Context(), artifact, owner); err != nil {
		t.Fatal(err)
	}
	store.evidence[owner.WorkspaceID+"\x00file-1"] = lifecyclecontract.FileScanEvidence{FileID: artifact.ID, WorkspaceID: artifact.WorkspaceID, ObjectKey: artifact.ObjectKey, FieldKey: artifact.FieldKey, Filename: artifact.Filename, SHA256: artifact.SHA256, Size: 1, Status: lifecyclecontract.FileScanClean}
	evidence, err := verifier.Status(t.Context(), owner.WorkspaceID, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeext.FileVerificationRequest{FileID: artifact.ID, ContentSHA256: artifact.SHA256, ScanReceipt: evidence.Receipt}
	if _, err = service.VerifyClean(WithUploadClaimPrincipal(t.Context(), owner), owner.WorkspaceID, request); err != nil {
		t.Fatal(err)
	}
	peer := owner
	peer.UserID = "peer"
	if _, err = service.VerifyClean(WithUploadClaimPrincipal(t.Context(), peer), owner.WorkspaceID, request); err == nil {
		t.Fatal("clean receipt proved ownership for another user")
	}
}
