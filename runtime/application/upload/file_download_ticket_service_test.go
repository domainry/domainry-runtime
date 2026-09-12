package upload

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func TestFileDownloadTicketBindsCallerFileRecordAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 13, 3, 4, 5, 0, time.UTC)
	service, err := NewFileDownloadTicketService(bytes.Repeat([]byte("d"), 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeext.FileDownloadRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{FileID: "opaque-file", ContentSHA256: strings.Repeat("a", 64), ScanReceipt: "receipt"},
		Binding:                 runtimeext.FileRecordBinding{ObjectKey: "document_file_version", RecordID: "version-1", FileIDField: "runtime_file_id"},
	}
	ticket, err := service.Issue(t.Context(), "workspace-a", "user-a", "auth-7", request)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.ExpiresAt != now.Add(fileDownloadTicketLifetime) || !strings.HasPrefix(ticket.ProtectedDownload, "/uploads/opaque-file?download_ticket=") {
		t.Fatalf("ticket=%+v", ticket)
	}
	token := strings.TrimPrefix(ticket.ProtectedDownload, "/uploads/opaque-file?download_ticket=")
	claims, err := service.Authorize(t.Context(), token, "workspace-a", "user-a", "auth-7", "opaque-file")
	if err != nil || claims.ObjectKey != request.Binding.ObjectKey || claims.RecordID != request.Binding.RecordID || claims.FieldKey != request.Binding.FileIDField {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	for _, test := range []struct{ workspace, user, revision, file string }{
		{"workspace-b", "user-a", "auth-7", "opaque-file"},
		{"workspace-a", "user-b", "auth-7", "opaque-file"},
		{"workspace-a", "user-a", "auth-8", "opaque-file"},
		{"workspace-a", "user-a", "auth-7", "other-file"},
	} {
		if _, err := service.Authorize(t.Context(), token, test.workspace, test.user, test.revision, test.file); apperror.CodeOf(err) != "backend.upload.download_ticket_invalid" {
			t.Fatalf("unexpected authorization result for %+v: %v", test, err)
		}
	}
	if _, err := service.Authorize(t.Context(), token+"x", "workspace-a", "user-a", "auth-7", "opaque-file"); apperror.CodeOf(err) != "backend.upload.download_ticket_invalid" {
		t.Fatalf("tampered ticket err=%v", err)
	}
	now = ticket.ExpiresAt
	if _, err := service.Authorize(t.Context(), token, "workspace-a", "user-a", "auth-7", "opaque-file"); apperror.CodeOf(err) != "backend.upload.download_ticket_invalid" {
		t.Fatalf("expired ticket err=%v", err)
	}
}
