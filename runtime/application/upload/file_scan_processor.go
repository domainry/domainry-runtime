package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

const builtinFileScannerProvider = "domainry-content-safety-v1"

type DurableFileScanStore interface {
	lifecyclecontract.FileScanStore
	lifecyclecontract.PendingFileScanStore
}

// FileScanProcessor consumes Lifecycle's durable pending-upload queue. The
// built-in scanner validates the exact stored bytes, rejects executable and
// EICAR content, and verifies the declared file format before publishing a
// terminal result. Deployments can replace this processor with a stronger
// scanner while keeping the same Lifecycle evidence contract.
type FileScanProcessor struct {
	store      DurableFileScanStore
	uploadRoot string
	clock      func() time.Time
}

func NewFileScanProcessor(store DurableFileScanStore, uploadRoot string, clock func() time.Time) (*FileScanProcessor, error) {
	root, err := filepath.Abs(strings.TrimSpace(uploadRoot))
	if err != nil || strings.TrimSpace(uploadRoot) == "" {
		return nil, fmt.Errorf("file scan upload root is required")
	}
	if store == nil {
		return nil, fmt.Errorf("durable file scan store is required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &FileScanProcessor{store: store, uploadRoot: root, clock: clock}, nil
}

func (p *FileScanProcessor) ProcessPending(ctx context.Context, limit int) (int, error) {
	if p == nil || p.store == nil {
		return 0, fmt.Errorf("file scan processor is unavailable")
	}
	scope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "scan pending Runtime uploads")
	pending, err := p.store.PendingFileScans(ctx, scope, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, candidate := range pending {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		result := p.scan(candidate)
		if err := p.store.RecordFileScan(ctx, result); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (p *FileScanProcessor) scan(candidate lifecyclecontract.FileScanEvidence) lifecyclecontract.FileScanEvidence {
	status, reason := lifecyclecontract.FileScanClean, "validated"
	path, err := p.path(candidate)
	var content []byte
	if err == nil {
		content, err = os.ReadFile(path)
	}
	if err != nil {
		status, reason = lifecyclecontract.FileScanFailed, "file_unavailable"
	} else if !fileContentIdentityMatches(candidate, content) {
		status, reason = lifecyclecontract.FileScanFailed, "content_identity_changed"
	} else if unsafeUploadContent(content) {
		status, reason = lifecyclecontract.FileScanQuarantined, "unsafe_signature"
	} else if !uploadContentTypeMatches(candidate.ContentType, content) {
		status, reason = lifecyclecontract.FileScanQuarantined, "content_type_mismatch"
	}
	candidate.Status = status
	candidate.Provider = builtinFileScannerProvider
	candidate.EvidenceRef = fileScanEvidenceReference(candidate, reason)
	candidate.ScannedAt = p.clock().UTC()
	return candidate
}

func (p *FileScanProcessor) path(candidate lifecyclecontract.FileScanEvidence) (string, error) {
	workspace := strings.TrimSpace(candidate.WorkspaceID)
	filename := strings.TrimSpace(candidate.Filename)
	if workspace == "" || filename == "" || filepath.Base(filename) != filename {
		return "", fmt.Errorf("file scan artifact identity is invalid")
	}
	digest := sha256.Sum256([]byte(workspace))
	return filepath.Join(p.uploadRoot, "workspace-"+hex.EncodeToString(digest[:16]), filename), nil
}

func fileContentIdentityMatches(candidate lifecyclecontract.FileScanEvidence, content []byte) bool {
	digest := sha256.Sum256(content)
	return int64(len(content)) == candidate.Size && strings.EqualFold(hex.EncodeToString(digest[:]), strings.TrimSpace(candidate.SHA256))
}

func unsafeUploadContent(content []byte) bool {
	upper := bytes.ToUpper(content)
	if bytes.Contains(upper, []byte("EICAR-STANDARD-ANTIVIRUS-TEST-FILE")) {
		return true
	}
	for _, prefix := range [][]byte{
		{0x7f, 'E', 'L', 'F'}, {'M', 'Z'}, {0xcf, 0xfa, 0xed, 0xfe}, {0xce, 0xfa, 0xed, 0xfe}, {0xfe, 0xed, 0xfa, 0xcf}, {0xfe, 0xed, 0xfa, 0xce},
	} {
		if bytes.HasPrefix(content, prefix) {
			return true
		}
	}
	return false
}

func uploadContentTypeMatches(contentType string, content []byte) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(content)))
	switch contentType {
	case "application/json":
		return json.Valid(content)
	case "text/csv", "text/plain":
		return strings.HasPrefix(detected, "text/plain")
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/zip":
		return detected == "application/zip"
	case "application/vnd.ms-excel":
		return bytes.HasPrefix(content, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
	case "application/octet-stream":
		return true
	default:
		return detected == contentType
	}
}

func fileScanEvidenceReference(candidate lifecyclecontract.FileScanEvidence, reason string) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, strings.Join([]string{
		builtinFileScannerProvider, candidate.WorkspaceID, candidate.FileID, strings.ToLower(candidate.SHA256),
		fmt.Sprint(candidate.Size), strings.ToLower(candidate.ContentType), candidate.Status, strings.TrimSpace(reason),
	}, "\x00"))
	return strings.TrimSpace(reason) + ":sha256:" + hex.EncodeToString(hash.Sum(nil))
}
