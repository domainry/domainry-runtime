package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
)

const builtinFileScannerProvider = "domainry-content-safety-v1"
const blobIdentityVerifierProvider = "domainry-blob-identity-v1"

type DurableFileScanStore interface {
	lifecyclecontract.FileScanStore
	lifecyclecontract.PendingFileScanStore
}

// FileScanProcessor consumes Lifecycle's durable pending-upload queue. It owns
// immutable-content verification and delegates content verdicts to the
// deployment scanner. Scanner failures remain pending for retry; missing or
// identity-mismatched blobs become terminal failed evidence.
type FileScanProcessor struct {
	store   DurableFileScanStore
	blobs   runtimefile.BlobStore
	scanner runtimefile.FileScanner
	clock   func() time.Time
}

func NewFileScanProcessor(store DurableFileScanStore, blobs runtimefile.BlobStore, scanner runtimefile.FileScanner, clock func() time.Time) (*FileScanProcessor, error) {
	if store == nil || blobs == nil || scanner == nil {
		return nil, fmt.Errorf("durable file scan store, blob store and scanner are required")
	}
	if !validFileAdapterDescriptor(blobs.Descriptor()) || !validFileAdapterDescriptor(scanner.Descriptor()) {
		return nil, fmt.Errorf("blob store and scanner descriptors are required")
	}
	if clock == nil {
		clock = time.Now
	}
	return &FileScanProcessor{store: store, blobs: blobs, scanner: scanner, clock: clock}, nil
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
		result, err := p.scan(ctx, candidate)
		if err != nil {
			return processed, err
		}
		if err := p.store.RecordFileScan(ctx, result); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (p *FileScanProcessor) scan(ctx context.Context, candidate lifecyclecontract.FileScanEvidence) (lifecyclecontract.FileScanEvidence, error) {
	reader, err := p.blobs.Open(ctx, candidate.WorkspaceID, candidate.Filename)
	if err != nil {
		candidate.Status = lifecyclecontract.FileScanFailed
		candidate.Provider = blobIdentityVerifierProvider
		reason := "file_unavailable"
		if !errors.Is(err, runtimefile.ErrBlobNotFound) {
			reason = "blob_open_failed"
		}
		candidate.EvidenceRef = fileScanEvidenceReference(candidate, reason, candidate.Provider)
		candidate.ScannedAt = p.clock().UTC()
		return candidate, nil
	}
	defer reader.Close()

	hash := sha256.New()
	counter := &byteCounter{}
	content := io.TeeReader(reader, io.MultiWriter(hash, counter))
	result, err := p.scanner.Scan(ctx, runtimefile.FileScanRequest{
		WorkspaceID: candidate.WorkspaceID, FileID: candidate.FileID, BlobKey: candidate.Filename, Filename: candidate.Filename,
		ContentType: candidate.ContentType, ContentSHA256: candidate.SHA256, Size: candidate.Size,
	}, content)
	if err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	if _, err := io.Copy(io.Discard, content); err != nil {
		return lifecyclecontract.FileScanEvidence{}, err
	}
	actualDigest := hex.EncodeToString(hash.Sum(nil))
	if counter.size != candidate.Size || !strings.EqualFold(actualDigest, strings.TrimSpace(candidate.SHA256)) {
		candidate.Status = lifecyclecontract.FileScanFailed
		candidate.Provider = blobIdentityVerifierProvider
		candidate.EvidenceRef = fileScanEvidenceReference(candidate, "content_identity_changed", candidate.Provider)
	} else {
		if !validTerminalFileScanResult(result) {
			return lifecyclecontract.FileScanEvidence{}, fmt.Errorf("file scanner returned invalid terminal evidence")
		}
		candidate.Status, candidate.Provider, candidate.EvidenceRef = result.Status, result.Provider, result.EvidenceRef
	}
	candidate.ScannedAt = p.clock().UTC()
	return candidate, nil
}

type byteCounter struct{ size int64 }

func (c *byteCounter) Write(content []byte) (int, error) {
	c.size += int64(len(content))
	return len(content), nil
}

func validFileAdapterDescriptor(descriptor runtimefile.AdapterDescriptor) bool {
	return strings.TrimSpace(descriptor.Provider) != "" && strings.TrimSpace(descriptor.Revision) != ""
}

func validTerminalFileScanResult(result runtimefile.FileScanResult) bool {
	terminal := result.Status == lifecyclecontract.FileScanClean || result.Status == lifecyclecontract.FileScanQuarantined || result.Status == lifecyclecontract.FileScanFailed
	return terminal && strings.TrimSpace(result.Provider) != "" && strings.TrimSpace(result.EvidenceRef) != ""
}

// BuiltinFileScanner is the safe local default. It intentionally owns no
// storage paths and can be replaced by a deployment adapter.
type BuiltinFileScanner struct{}

func NewBuiltinFileScanner() *BuiltinFileScanner { return &BuiltinFileScanner{} }

func (*BuiltinFileScanner) Descriptor() runtimefile.AdapterDescriptor {
	return runtimefile.AdapterDescriptor{Provider: builtinFileScannerProvider, Revision: "v1"}
}

func (*BuiltinFileScanner) Scan(_ context.Context, request runtimefile.FileScanRequest, reader io.Reader) (runtimefile.FileScanResult, error) {
	content, err := io.ReadAll(reader)
	if err != nil {
		return runtimefile.FileScanResult{}, err
	}
	status, reason := lifecyclecontract.FileScanClean, "validated"
	if unsafeUploadContent(content) {
		status, reason = lifecyclecontract.FileScanQuarantined, "unsafe_signature"
	} else if !uploadContentTypeMatches(request.ContentType, content) {
		status, reason = lifecyclecontract.FileScanQuarantined, "content_type_mismatch"
	}
	candidate := lifecyclecontract.FileScanEvidence{
		WorkspaceID: request.WorkspaceID, FileID: request.FileID, Filename: request.Filename, ContentType: request.ContentType,
		SHA256: request.ContentSHA256, Size: request.Size, Status: status,
	}
	return runtimefile.FileScanResult{Status: status, Provider: builtinFileScannerProvider, EvidenceRef: fileScanEvidenceReference(candidate, reason, builtinFileScannerProvider)}, nil
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

func fileScanEvidenceReference(candidate lifecyclecontract.FileScanEvidence, reason, provider string) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, strings.Join([]string{
		strings.TrimSpace(provider), candidate.WorkspaceID, candidate.FileID, strings.ToLower(candidate.SHA256),
		fmt.Sprint(candidate.Size), strings.ToLower(candidate.ContentType), candidate.Status, strings.TrimSpace(reason),
	}, "\x00"))
	return strings.TrimSpace(reason) + ":sha256:" + hex.EncodeToString(hash.Sum(nil))
}
