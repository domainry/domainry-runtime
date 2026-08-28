package upload

import (
	"context"
	"errors"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
)

type fileScanStoreStub struct {
	evidence lifecyclecontract.FileScanEvidence
}

func (s fileScanStoreStub) FindFileScan(context.Context, string, string) (lifecyclecontract.FileScanEvidence, error) {
	return s.evidence, nil
}
func (fileScanStoreStub) RecordFileScan(context.Context, lifecyclecontract.FileScanEvidence) error {
	return nil
}

func TestFileScanReceiptIsCleanOnlyAndBoundToExactIdentity(t *testing.T) {
	clean := lifecyclecontract.FileScanEvidence{FileID: "file-1", WorkspaceID: "workspace-a", SHA256: "abc", Size: 7, Status: lifecyclecontract.FileScanClean, Provider: "scanner", EvidenceRef: "scan-1", ScannedAt: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)}
	service := NewFileScanReceiptVerifier(fileScanStoreStub{evidence: clean}, []byte("01234567890123456789012345678901"))
	status, err := service.Status(t.Context(), "workspace-a", "file-1")
	if err != nil || status.Receipt == "" {
		t.Fatalf("clean status = %#v, %v", status, err)
	}
	if _, err := service.VerifyClean(t.Context(), "workspace-a", "file-1", "abc", status.Receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyClean(t.Context(), "workspace-a", "file-1", "changed", status.Receipt); !errors.Is(err, ErrFileScanReceiptInvalid) {
		t.Fatalf("changed hash error = %v", err)
	}
	tampered := service
	tampered.store = fileScanStoreStub{evidence: lifecyclecontract.FileScanEvidence{FileID: "file-2", WorkspaceID: "workspace-a", SHA256: "abc", Size: 7, Status: lifecyclecontract.FileScanClean, Provider: "scanner", EvidenceRef: "scan-1", ScannedAt: clean.ScannedAt}}
	if _, err := tampered.VerifyClean(t.Context(), "workspace-a", "file-2", "abc", status.Receipt); !errors.Is(err, ErrFileScanReceiptInvalid) {
		t.Fatalf("cross-file receipt error = %v", err)
	}
}

func TestFileScanReceiptFailsClosedForNonCleanStates(t *testing.T) {
	for _, state := range []string{lifecyclecontract.FileScanPending, lifecyclecontract.FileScanQuarantined, lifecyclecontract.FileScanFailed} {
		t.Run(state, func(t *testing.T) {
			service := NewFileScanReceiptVerifier(fileScanStoreStub{evidence: lifecyclecontract.FileScanEvidence{FileID: "file-1", WorkspaceID: "workspace-a", SHA256: "abc", Size: 7, Status: state}}, []byte("01234567890123456789012345678901"))
			status, err := service.Status(t.Context(), "workspace-a", "file-1")
			if err != nil || status.Receipt != "" {
				t.Fatalf("status = %#v, %v", status, err)
			}
			if _, err := service.VerifyClean(t.Context(), "workspace-a", "file-1", "abc", "forged"); !errors.Is(err, ErrFileNotClean) {
				t.Fatalf("verify error = %v", err)
			}
		})
	}
}
