package upload

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

// AgentTaskAttachmentFileRequest is the already-authorized record-file
// identity that Runtime may materialize for one Agent task. The caller first
// proves read access to the exact business record and field; this service then
// proves upload ownership, clean scan state, and byte identity.
type AgentTaskAttachmentFileRequest struct {
	WorkspaceID string
	OwnerUserID string
	ObjectKey   string
	RecordID    string
	FieldKey    string
	FileID      string
	Filename    string
	MaxBytes    int64
}

type AgentTaskAttachmentFile struct {
	Filename    string
	ContentType string
	Bytes       int64
	SHA256      string
	Data        []byte
}

type AgentTaskAttachmentFileService struct {
	subjects *UploadSubjectRegistry
	scans    *FileScanReceiptVerifier
	files    *FileCapabilityService
}

func NewAgentTaskAttachmentFileService(subjects *UploadSubjectRegistry, scans *FileScanReceiptVerifier, files *FileCapabilityService) *AgentTaskAttachmentFileService {
	return &AgentTaskAttachmentFileService{subjects: subjects, scans: scans, files: files}
}

func (s *AgentTaskAttachmentFileService) Open(ctx context.Context, request AgentTaskAttachmentFileRequest) (AgentTaskAttachmentFile, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.OwnerUserID = strings.TrimSpace(request.OwnerUserID)
	request.ObjectKey = strings.TrimSpace(request.ObjectKey)
	request.RecordID = strings.TrimSpace(request.RecordID)
	request.FieldKey = strings.TrimSpace(request.FieldKey)
	request.FileID = strings.TrimSpace(request.FileID)
	request.Filename = strings.TrimSpace(request.Filename)
	if s == nil || s.subjects == nil || s.scans == nil || s.files == nil {
		return AgentTaskAttachmentFile{}, apperror.New(apperror.KindUnavailable, "agent.task.attachment_source_unavailable", nil, nil)
	}
	requiredIdentity := []string{request.WorkspaceID, request.OwnerUserID, request.ObjectKey, request.RecordID, request.FieldKey, request.FileID, request.Filename}
	for _, value := range requiredIdentity {
		if value == "" {
			return AgentTaskAttachmentFile{}, apperror.New(apperror.KindBadRequest, "agent.task.attachment_source_invalid", nil, nil)
		}
	}
	if filepath.Base(request.Filename) != request.Filename || request.MaxBytes < 1 {
		return AgentTaskAttachmentFile{}, apperror.New(apperror.KindBadRequest, "agent.task.attachment_source_invalid", nil, nil)
	}
	if err := s.subjects.Authorize(ctx, request.WorkspaceID, request.OwnerUserID, request.FileID); err != nil {
		return AgentTaskAttachmentFile{}, err
	}
	evidence, err := s.scans.Status(ctx, request.WorkspaceID, request.FileID)
	if err != nil {
		return AgentTaskAttachmentFile{}, err
	}
	if evidence.Status != lifecyclecontract.FileScanClean {
		return AgentTaskAttachmentFile{}, apperror.New(apperror.KindForbidden, "backend.upload.scan_not_clean", nil, nil)
	}
	if strings.TrimSpace(evidence.ObjectKey) != request.ObjectKey || strings.TrimSpace(evidence.FieldKey) != request.FieldKey || evidence.Size < 1 || evidence.Size > request.MaxBytes {
		return AgentTaskAttachmentFile{}, apperror.New(apperror.KindForbidden, "agent.task.attachment_source_mismatch", nil, nil)
	}
	verified, err := s.files.OpenVerified(ctx, request.WorkspaceID, runtimeext.VerifiedFileRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{
			FileID: request.FileID, ContentSHA256: evidence.SHA256, ScanReceipt: evidence.Receipt,
		},
		Binding: runtimeext.FileRecordBinding{ObjectKey: request.ObjectKey, RecordID: request.RecordID, FileIDField: request.FieldKey},
	})
	if err != nil {
		return AgentTaskAttachmentFile{}, err
	}
	defer verified.Content.Close()
	raw, err := io.ReadAll(io.LimitReader(verified.Content, request.MaxBytes+1))
	if err != nil {
		return AgentTaskAttachmentFile{}, err
	}
	if int64(len(raw)) != evidence.Size || int64(len(raw)) > request.MaxBytes || !strings.EqualFold(verified.ContentType, evidence.ContentType) || !strings.EqualFold(verified.ContentSHA256, evidence.SHA256) {
		return AgentTaskAttachmentFile{}, apperror.New(apperror.KindUnavailable, "agent.task.attachment_source_mismatch", errors.New("verified file identity changed while reading"), nil)
	}
	return AgentTaskAttachmentFile{
		Filename: request.Filename, ContentType: strings.ToLower(strings.TrimSpace(evidence.ContentType)),
		Bytes: evidence.Size, SHA256: strings.ToLower(strings.TrimSpace(evidence.SHA256)), Data: raw,
	}, nil
}
