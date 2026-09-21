package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const maxUploadBytes = 5 << 20
const maxUploadRequestBytes = maxUploadBytes + (1 << 20)

var errUploadTooLarge = errors.New("upload exceeds configured file limit")

var allowedUploadContentTypes = map[string]string{
	"application/json":         ".json",
	"application/pdf":          ".pdf",
	"application/zip":          ".zip",
	"image/gif":                ".gif",
	"image/jpeg":               ".jpg",
	"image/png":                ".png",
	"image/webp":               ".webp",
	"text/csv":                 ".csv",
	"text/plain":               ".txt",
	"application/octet-stream": ".bin",
	"application/vnd.ms-excel": ".xls",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       ".xlsx",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
}

type UploadsHandler struct {
	access            *uploadapplication.UploadAccessApplicationService
	blobs             runtimefile.BlobStore
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	readPrefix        func(io.Reader) ([]byte, error)
	artifacts         lifecyclecontract.UploadArtifactStore
	scans             *uploadapplication.FileScanReceiptVerifier
	tickets           *uploadapplication.FileDownloadTicketService
	subjects          *uploadapplication.UploadSubjectRegistry
}

type UploadsDependencies struct {
	Access            *uploadapplication.UploadAccessApplicationService
	Blobs             runtimefile.BlobStore
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Artifacts         lifecyclecontract.UploadArtifactStore
	Scans             *uploadapplication.FileScanReceiptVerifier
	Tickets           *uploadapplication.FileDownloadTicketService
	Subjects          *uploadapplication.UploadSubjectRegistry
}

func NewUploadsHandler(deps UploadsDependencies) *UploadsHandler {
	return &UploadsHandler{
		access: deps.Access, blobs: deps.Blobs, principal: deps.Principal, artifacts: deps.Artifacts, scans: deps.Scans, tickets: deps.Tickets, subjects: deps.Subjects,
		writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
		readPrefix: readUploadPrefix,
	}
}

func (h *UploadsHandler) UseAccess(access *uploadapplication.UploadAccessApplicationService) {
	if access != nil {
		h.access = access
	}
}

type uploadResponse struct {
	FileID      string `json:"file_id"`
	SHA256      string `json:"content_sha256"`
	ScanStatus  string `json:"scan_status"`
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	ObjectKey   string `json:"object_key,omitempty"`
	FieldKey    string `json:"field_key,omitempty"`
}

func (h *UploadsHandler) uploadFile(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !principal.Known {
		h.writeError(w, r, http.StatusForbidden, "backend.role.unknown")
		return
	}
	objectKey := strings.TrimSpace(r.URL.Query().Get("object_key"))
	fieldKey := strings.TrimSpace(r.URL.Query().Get("field_key"))
	if objectKey == "" || fieldKey == "" {
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.object_field_required")
		return
	}
	policy, err := h.access.UploadPolicy(r.Context(), objectKey, fieldKey, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	requestLimit := policy.MaxSizeBytes + (1 << 20)
	if requestLimit > maxUploadRequestBytes {
		requestLimit = maxUploadRequestBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, requestLimit)
	if err := r.ParseMultipartForm(requestLimit); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, r, http.StatusRequestEntityTooLarge, "backend.upload.file_too_large")
			return
		}
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.invalid_multipart")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.file_required")
		return
	}
	defer file.Close()
	prefix, err := h.readPrefix(file)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.read_failed")
		return
	}
	if len(prefix) == 0 {
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.empty_file")
		return
	}
	contentType := http.DetectContentType(prefix)
	extension, ok := allowedUploadContentTypes[contentType]
	if !ok {
		if parsedType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type")); err == nil {
			if detectedExtension, allowed := allowedUploadContentTypes[parsedType]; allowed {
				contentType = parsedType
				extension = detectedExtension
				ok = true
			}
		}
	}
	if !ok {
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.unsupported_file_type")
		return
	}
	if !policy.AllowsContentType(contentType) {
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.mime_type_denied")
		return
	}
	if _, err := principalmodel.NewWorkspaceID(principal.WorkspaceID); err != nil {
		h.writeError(w, r, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	if h.blobs == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "backend.upload.storage_unavailable")
		return
	}
	fileID := requestcontext.NewRequestID()
	staged, err := h.blobs.Stage(r.Context(), runtimefile.BlobStageRequest{
		WorkspaceID: principal.WorkspaceID, StageID: fileID, Content: io.MultiReader(bytes.NewReader(prefix), file), MaxBytes: policy.MaxSizeBytes,
	})
	if err != nil {
		if uploadStorageExhausted(err) {
			h.writeError(w, r, http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
			return
		}
		if errors.Is(err, runtimefile.ErrBlobTooLarge) {
			h.writeError(w, r, http.StatusRequestEntityTooLarge, "backend.upload.file_too_large")
			return
		}
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.read_failed")
		return
	}
	defer func() { _ = h.blobs.Delete(context.WithoutCancel(r.Context()), principal.WorkspaceID, staged.BlobKey) }()
	size, contentSHA256 := staged.Size, staged.ContentSHA256
	fileIdentityDigest := sha256.Sum256([]byte(fileID))
	filename := contentSHA256 + "-" + hex.EncodeToString(fileIdentityDigest[:8]) + extension
	if err := h.access.ValidateUploadContent(r.Context(), objectKey, fieldKey, contentType, size, principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if _, err := h.blobs.Commit(r.Context(), runtimefile.BlobCommitRequest{
		WorkspaceID: principal.WorkspaceID, StageKey: staged.BlobKey, BlobKey: filename, ContentSHA256: contentSHA256, Size: size,
	}); err != nil {
		if uploadStorageExhausted(err) {
			h.writeError(w, r, http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
			return
		}
		h.writeError(w, r, http.StatusInternalServerError, "backend.upload.save_failed")
		return
	}
	artifact := lifecyclecontract.UploadArtifact{ID: fileID, WorkspaceID: principal.WorkspaceID, ObjectKey: objectKey, FieldKey: fieldKey, Filename: filename, ContentType: contentType, SHA256: contentSHA256, Size: size, CreatedAt: time.Now().UTC()}
	if err := h.subjects.Register(r.Context(), artifact, principal); err != nil {
		_ = h.blobs.Delete(context.WithoutCancel(r.Context()), principal.WorkspaceID, filename)
		h.writeServiceError(w, r, err)
		return
	}
	if h.artifacts != nil {
		if err := h.artifacts.RegisterUpload(r.Context(), artifact); err != nil {
			_ = h.blobs.Delete(context.WithoutCancel(r.Context()), principal.WorkspaceID, filename)
			h.writeError(w, r, http.StatusInternalServerError, "backend.upload.register_failed")
			return
		}
	}
	url := "/uploads/" + fileID
	h.access.RecordUploaded(r.Context(), objectKey, fieldKey, recordmodel.RecordFileReference{
		FileID: fileID, Filename: filename, ContentType: contentType, Size: size, ContentSHA256: contentSHA256,
	}, principal)
	h.writeJSON(w, http.StatusCreated, uploadResponse{
		FileID: fileID, SHA256: contentSHA256, ScanStatus: lifecyclecontract.FileScanPending,
		URL:         url,
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
		ObjectKey:   objectKey,
		FieldKey:    fieldKey,
	})
}

func (h *UploadsHandler) fileScanStatus(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !principal.Known {
		h.writeError(w, r, http.StatusForbidden, "backend.role.unknown")
		return
	}
	if h.scans == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "backend.upload.scan_service_unavailable")
		return
	}
	evidence, err := h.scans.Status(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("fileID")))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if err := h.access.AuthorizeUpload(r.Context(), evidence.ObjectKey, evidence.FieldKey, principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if err := h.subjects.Authorize(r.Context(), principal.WorkspaceID, principal.UserID, evidence.FileID); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	evidence.WorkspaceID = ""
	evidence.ObjectKey, evidence.FieldKey, evidence.Filename = "", "", ""
	h.writeJSON(w, http.StatusOK, evidence)
}

func uploadStorageExhausted(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT)
}

func readUploadPrefix(reader io.Reader) ([]byte, error) {
	prefix := make([]byte, 512)
	prefixSize, err := io.ReadFull(reader, prefix)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return prefix[:prefixSize], nil
}

func copyBoundedUpload(writer io.Writer, prefix []byte, reader io.Reader, copyFn func(io.Writer, io.Reader) (int64, error)) (int64, error) {
	return copyBoundedUploadLimit(writer, prefix, reader, copyFn, maxUploadBytes)
}

func copyBoundedUploadLimit(writer io.Writer, prefix []byte, reader io.Reader, copyFn func(io.Writer, io.Reader) (int64, error), maximum int64) (int64, error) {
	if maximum < 1 || int64(len(prefix)) > maximum {
		return 0, errUploadTooLarge
	}
	if _, err := writer.Write(prefix); err != nil {
		return 0, err
	}
	written, err := copyFn(writer, io.LimitReader(reader, maximum-int64(len(prefix))+1))
	if err != nil {
		return 0, err
	}
	size := int64(len(prefix)) + written
	if size > maximum {
		return 0, errUploadTooLarge
	}
	return size, nil
}

func (h *UploadsHandler) serveUploadedFile(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !principal.Known {
		h.writeError(w, r, http.StatusForbidden, "backend.role.unknown")
		return
	}
	fileIdentifier := filepath.Base(strings.TrimSpace(r.PathValue("fileID")))
	if fileIdentifier == "." || strings.Contains(fileIdentifier, "..") {
		http.NotFound(w, r)
		return
	}
	objectKey := strings.TrimSpace(r.URL.Query().Get("object_key"))
	fieldKey := strings.TrimSpace(r.URL.Query().Get("field_key"))
	recordID := strings.TrimSpace(r.URL.Query().Get("record_id"))
	ticketAuthorized := false
	var scanEvidence lifecyclecontract.FileScanEvidence
	if token := strings.TrimSpace(r.URL.Query().Get("download_ticket")); token != "" {
		if h.tickets == nil {
			h.writeError(w, r, http.StatusServiceUnavailable, "backend.upload.download_ticket_unavailable")
			return
		}
		claims, err := h.tickets.Authorize(r.Context(), token, principal.WorkspaceID, principal.UserID, principal.EffectiveAuthorizationRevision(), fileIdentifier)
		if err != nil {
			h.writeServiceError(w, r, err)
			return
		}
		objectKey, fieldKey, recordID = claims.ObjectKey, claims.FieldKey, claims.RecordID
		ticketAuthorized = true
	}
	storageFilename := fileIdentifier
	if h.scans == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "backend.upload.scan_service_unavailable")
		return
	}
	if h.scans != nil {
		evidence, err := h.scans.Status(r.Context(), principal.WorkspaceID, fileIdentifier)
		switch {
		case err == nil:
			scanEvidence = evidence
			if evidence.Status != lifecyclecontract.FileScanClean {
				h.writeError(w, r, http.StatusForbidden, "backend.upload.scan_not_clean")
				return
			}
			// Ordinary download URLs remain bound to the field that authorized the
			// upload. A signed Action ticket instead carries the exact business
			// record binding already authorized by the Action executor.
			if !ticketAuthorized && (strings.TrimSpace(evidence.ObjectKey) != objectKey || strings.TrimSpace(evidence.FieldKey) != fieldKey) {
				h.writeError(w, r, http.StatusForbidden, "backend.upload.permission_denied")
				return
			}
			storageFilename = filepath.Base(strings.TrimSpace(evidence.Filename))
			if storageFilename == "." || strings.Contains(storageFilename, "..") {
				http.NotFound(w, r)
				return
			}
		case errors.Is(err, sql.ErrNoRows):
			h.writeError(w, r, http.StatusForbidden, "backend.upload.scan_not_clean")
			return
		default:
			h.writeServiceError(w, r, err)
			return
		}
	}
	if ticketAuthorized {
		h.access.RecordTicketDownload(r.Context(), objectKey, fieldKey, recordID, fileIdentifier, principal)
	} else {
		if err := h.access.AuthorizeDownload(r.Context(), objectKey, fieldKey, recordID, fileIdentifier, principal); err != nil {
			h.writeServiceError(w, r, err)
			return
		}
	}
	if h.blobs == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "backend.upload.storage_unavailable")
		return
	}
	info, err := h.blobs.Stat(r.Context(), principal.WorkspaceID, storageFilename)
	if err != nil || info.Size != scanEvidence.Size || !strings.EqualFold(info.ContentSHA256, scanEvidence.SHA256) {
		h.writeError(w, r, http.StatusNotFound, "backend.upload.file_unavailable")
		return
	}
	content, err := h.blobs.Open(r.Context(), principal.WorkspaceID, storageFilename)
	if err != nil {
		h.writeError(w, r, http.StatusNotFound, "backend.upload.file_unavailable")
		return
	}
	defer content.Close()
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline; filename="+storageFilename)
	w.Header().Set("Content-Type", scanEvidence.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	if _, err := io.Copy(w, content); err != nil {
		return
	}
}
