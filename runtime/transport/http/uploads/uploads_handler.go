package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const maxUploadBytes = 5 << 20
const maxUploadRequestBytes = maxUploadBytes + (1 << 20)

var errUploadTooLarge = errors.New("upload exceeds configured file limit")

type uploadTemporaryFile interface {
	io.Writer
	Name() string
	Close() error
}

var (
	uploadMkdirAll   = os.MkdirAll
	uploadCreateTemp = func(dir, pattern string) (uploadTemporaryFile, error) {
		return os.CreateTemp(dir, pattern)
	}
	uploadRename = os.Rename
	uploadAbs    = filepath.Abs
)

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
	uploadDir         string
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	copyUpload        func(io.Writer, io.Reader) (int64, error)
	readPrefix        func(io.Reader) ([]byte, error)
	artifacts         lifecyclecontract.UploadArtifactStore
	scans             *uploadapplication.FileScanReceiptVerifier
}

type UploadsDependencies struct {
	Access            *uploadapplication.UploadAccessApplicationService
	UploadDir         string
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Artifacts         lifecyclecontract.UploadArtifactStore
	Scans             *uploadapplication.FileScanReceiptVerifier
}

func NewUploadsHandler(deps UploadsDependencies) *UploadsHandler {
	return &UploadsHandler{
		access: deps.Access, uploadDir: deps.UploadDir, principal: deps.Principal, artifacts: deps.Artifacts, scans: deps.Scans,
		writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
		copyUpload: io.Copy, readPrefix: readUploadPrefix,
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
	if err := h.access.AuthorizeUpload(r.Context(), objectKey, fieldKey, principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBytes)
	if err := r.ParseMultipartForm(maxUploadRequestBytes); err != nil {
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
	workspaceDir, err := h.workspaceUploadDir(principal.WorkspaceID)
	if err != nil {
		h.writeError(w, r, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	if err := uploadMkdirAll(workspaceDir, 0o700); err != nil {
		if uploadStorageExhausted(err) {
			h.writeError(w, r, http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
			return
		}
		h.writeError(w, r, http.StatusInternalServerError, "backend.upload.create_directory_failed")
		return
	}
	temporary, err := uploadCreateTemp(workspaceDir, ".upload-*")
	if err != nil {
		if uploadStorageExhausted(err) {
			h.writeError(w, r, http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
			return
		}
		h.writeError(w, r, http.StatusInternalServerError, "backend.upload.save_failed")
		return
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	hash := sha256.New()
	writer := io.MultiWriter(temporary, hash)
	size, err := copyBoundedUpload(writer, prefix, file, h.copyUpload)
	if err != nil {
		if uploadStorageExhausted(err) {
			h.writeError(w, r, http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
			return
		}
		if errors.Is(err, errUploadTooLarge) {
			h.writeError(w, r, http.StatusRequestEntityTooLarge, "backend.upload.file_too_large")
			return
		}
		h.writeError(w, r, http.StatusBadRequest, "backend.upload.read_failed")
		return
	}
	if err := temporary.Close(); err != nil {
		if uploadStorageExhausted(err) {
			h.writeError(w, r, http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
			return
		}
		h.writeError(w, r, http.StatusInternalServerError, "backend.upload.save_failed")
		return
	}
	filename := hex.EncodeToString(hash.Sum(nil)[:16]) + extension
	path := filepath.Join(workspaceDir, filename)
	if err := uploadRename(temporaryPath, path); err != nil {
		if uploadStorageExhausted(err) {
			h.writeError(w, r, http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
			return
		}
		h.writeError(w, r, http.StatusInternalServerError, "backend.upload.save_failed")
		return
	}
	fileID := requestcontext.NewRequestID()
	contentSHA256 := hex.EncodeToString(hash.Sum(nil))
	if h.artifacts != nil {
		artifact := lifecyclecontract.UploadArtifact{ID: fileID, WorkspaceID: principal.WorkspaceID, ObjectKey: objectKey, FieldKey: fieldKey, Filename: filename, ContentType: contentType, SHA256: contentSHA256, Size: size, CreatedAt: time.Now().UTC()}
		if err := h.artifacts.RegisterUpload(r.Context(), artifact); err != nil {
			_ = os.Remove(path)
			h.writeError(w, r, http.StatusInternalServerError, "backend.upload.register_failed")
			return
		}
	}
	keepTemporary = true
	url := "/uploads/" + filename
	h.access.RecordUploaded(r.Context(), objectKey, fieldKey, filename, contentType, int(size), principal)
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
	if _, err := writer.Write(prefix); err != nil {
		return 0, err
	}
	written, err := copyFn(writer, io.LimitReader(reader, maxUploadBytes-int64(len(prefix))+1))
	if err != nil {
		return 0, err
	}
	size := int64(len(prefix)) + written
	if size > maxUploadBytes {
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
	filename := filepath.Base(strings.TrimSpace(r.PathValue("filename")))
	if filename == "." || strings.Contains(filename, "..") {
		http.NotFound(w, r)
		return
	}
	objectKey := strings.TrimSpace(r.URL.Query().Get("object_key"))
	fieldKey := strings.TrimSpace(r.URL.Query().Get("field_key"))
	recordID := strings.TrimSpace(r.URL.Query().Get("record_id"))
	if err := h.access.AuthorizeDownload(r.Context(), objectKey, fieldKey, recordID, filename, principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	workspaceDir, err := h.workspaceUploadDir(principal.WorkspaceID)
	if err != nil {
		h.writeError(w, r, http.StatusForbidden, "backend.workspace_scope_required")
		return
	}
	path := filepath.Join(workspaceDir, filename)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline; filename="+filename)
	http.ServeFile(w, r, path)
}

func (h *UploadsHandler) workspaceUploadDir(workspaceID string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if _, err := principalmodel.NewWorkspaceID(workspaceID); err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(workspaceID))
	segment := "workspace-" + hex.EncodeToString(digest[:16])
	root, err := uploadAbs(strings.TrimSpace(h.uploadDir))
	if err != nil || strings.TrimSpace(h.uploadDir) == "" {
		return "", errors.New("upload root is required")
	}
	return filepath.Join(root, segment), nil
}
