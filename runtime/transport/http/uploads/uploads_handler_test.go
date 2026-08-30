package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type uploadArtifactStoreStub struct {
	artifact lifecyclecontract.UploadArtifact
	err      error
}

func (s *uploadArtifactStoreStub) RegisterUpload(_ context.Context, artifact lifecyclecontract.UploadArtifact) error {
	s.artifact = artifact
	return s.err
}

func (*uploadArtifactStoreStub) ReconcileUploadArtifacts(context.Context, lifecycleaccess.SystemScope, time.Time, int) (lifecyclecontract.UploadCleanupResult, error) {
	return lifecyclecontract.UploadCleanupResult{}, nil
}

func uploadTestHandler(t *testing.T, principal principalmodel.Principal) *UploadsHandler {
	t.Helper()
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "uploads", TemplateVersion: "1", Objects: uploadTestObjects()})
	return NewUploadsHandler(UploadsDependencies{
		Access: uploadapplication.NewUploadAccessApplicationService(records.Applications().Schema, records.Applications().Audit, nil), UploadDir: t.TempDir(), Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.Header().Set("X-Error-Code", code)
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := http.StatusInternalServerError
			switch apperror.KindOf(err) {
			case apperror.KindBadRequest:
				status = http.StatusBadRequest
			case apperror.KindForbidden:
				status = http.StatusForbidden
			case apperror.KindNotFound:
				status = http.StatusNotFound
			}
			w.Header().Set("X-Error-Code", apperror.CodeOf(err))
			w.WriteHeader(status)
		},
	})
}

func uploadTestObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{
		{Key: "document", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: "text"}, {Key: "sensitive", Type: "boolean"}}},
		{Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: "text"}}},
	}
}

func uploadTestRole(permissions ...string) accessfixture.Bundle {
	role := accessfixture.Bundle{Permissions: permissions}
	for _, permission := range permissions {
		switch permission {
		case "document.*":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "document", Scope: "all_records", Read: true, Write: true})
		case "document.read":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "document", Scope: "all_records", Read: true})
		case "asset.*":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "asset", Scope: "all_records", Read: true, Write: true})
		case "asset.read":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "asset", Scope: "all_records", Read: true})
		}
	}
	return role
}

func multipartUploadRequest(t *testing.T, target, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func multipartUploadRequestWithPartType(t *testing.T, target, filename, contentType string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestUploadFileAuthorizationAndValidation(t *testing.T) {
	unknown := uploadTestHandler(t, principalmodel.Principal{})
	response := httptest.NewRecorder()
	unknown.uploadFile(response, httptest.NewRequest(http.MethodPost, "/files?object_key=document&field_key=file_url", nil))
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "backend.role.unknown" {
		t.Fatalf("unknown upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.*", "asset.*"))
	handler := uploadTestHandler(t, principal)
	for _, test := range []struct {
		name   string
		target string
		code   string
		status int
	}{
		{name: "missing context", target: "/files", code: "backend.upload.object_field_required", status: http.StatusBadRequest},
		{name: "missing field", target: "/files?object_key=document", code: "backend.upload.object_field_required", status: http.StatusBadRequest},
		{name: "unknown object", target: "/files?object_key=missing&field_key=file_url", code: "backend.object.not_found", status: http.StatusNotFound},
		{name: "unknown field", target: "/files?object_key=document&field_key=missing", code: "backend.upload.field_not_defined", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.uploadFile(response, httptest.NewRequest(http.MethodPost, test.target, nil))
			if response.Code != test.status || response.Header().Get("X-Error-Code") != test.code {
				t.Fatalf("status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
			}
		})
	}

	denied := uploadTestHandler(t, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, uploadTestRole("document.read")))
	response = httptest.NewRecorder()
	denied.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "file.txt", []byte("hello")))
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "backend.upload.permission_denied" {
		t.Fatalf("denied upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	response = httptest.NewRecorder()
	handler.uploadFile(response, httptest.NewRequest(http.MethodPost, "/files?object_key=document&field_key=file_url", bytes.NewBufferString("not multipart")))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.invalid_multipart" {
		t.Fatalf("invalid multipart status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	var noFileBody bytes.Buffer
	noFileWriter := multipart.NewWriter(&noFileBody)
	if err := noFileWriter.WriteField("description", "missing file"); err != nil {
		t.Fatal(err)
	}
	if err := noFileWriter.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/files?object_key=document&field_key=file_url", &noFileBody)
	request.Header.Set("Content-Type", noFileWriter.FormDataContentType())
	response = httptest.NewRecorder()
	handler.uploadFile(response, request)
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.file_required" {
		t.Fatalf("missing file status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "empty.txt", nil))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.empty_file" {
		t.Fatalf("empty upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "large.bin", bytes.Repeat([]byte{'x'}, maxUploadBytes+1)))
	if response.Code != http.StatusRequestEntityTooLarge || response.Header().Get("X-Error-Code") != "backend.upload.file_too_large" {
		t.Fatalf("large upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	response = httptest.NewRecorder()
	var unsupportedBody bytes.Buffer
	unsupportedWriter := multipart.NewWriter(&unsupportedBody)
	unsupportedHeader := textproto.MIMEHeader{}
	unsupportedHeader.Set("Content-Disposition", `form-data; name="file"; filename="audio.mp3"`)
	unsupportedHeader.Set("Content-Type", "audio/mpeg")
	unsupportedPart, err := unsupportedWriter.CreatePart(unsupportedHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unsupportedPart.Write(append([]byte("ID3"), bytes.Repeat([]byte{0}, 600)...)); err != nil {
		t.Fatal(err)
	}
	if err := unsupportedWriter.Close(); err != nil {
		t.Fatal(err)
	}
	unsupportedRequest := httptest.NewRequest(http.MethodPost, "/files?object_key=document&field_key=file_url", &unsupportedBody)
	unsupportedRequest.Header.Set("Content-Type", unsupportedWriter.FormDataContentType())
	handler.uploadFile(response, unsupportedRequest)
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.unsupported_file_type" {
		t.Fatalf("unsupported upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequestWithPartType(t, "/files?object_key=document&field_key=file_url", "audio.mp3", ";", append([]byte("ID3"), bytes.Repeat([]byte{0}, 600)...)))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.unsupported_file_type" {
		t.Fatalf("invalid media type status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestUploadReadFailureAndDetectedContentType(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, uploadTestRole("document.*"))
	handler := uploadTestHandler(t, principal)
	handler.copyUpload = func(io.Writer, io.Reader) (int64, error) { return 0, errors.New("read failed") }
	response := httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "file.txt", []byte("content")))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.read_failed" {
		t.Fatalf("read failure status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	handler = uploadTestHandler(t, principal)
	handler.copyUpload = func(io.Writer, io.Reader) (int64, error) { return 0, syscall.ENOSPC }
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "file.txt", []byte("content")))
	if response.Code != http.StatusInsufficientStorage || response.Header().Get("X-Error-Code") != "backend.upload.storage_exhausted" {
		t.Fatalf("disk full status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	handler = uploadTestHandler(t, principal)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 600)...)
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "image.png", png))
	if response.Code != http.StatusCreated {
		t.Fatalf("png upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestUploadFileSuccessAndStorageFailure(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.*"))
	handler := uploadTestHandler(t, principal)
	response := httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "note.txt", []byte("hello upload")))
	if response.Code != http.StatusCreated {
		t.Fatalf("upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	var payload uploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.FileID == "" || payload.SHA256 == "" || payload.ScanStatus != lifecyclecontract.FileScanPending || payload.Filename == "" || payload.ContentType != "application/octet-stream" || payload.Size != int64(len("hello upload")) {
		t.Fatalf("upload response=%#v", payload)
	}
	workspaceDir, err := handler.workspaceUploadDir(principal.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, payload.Filename)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(workspaceDir, payload.Filename)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspaceDir, payload.Filename), 0o700); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "note.txt", []byte("hello upload")))
	if response.Code != http.StatusInternalServerError || response.Header().Get("X-Error-Code") != "backend.upload.save_failed" {
		t.Fatalf("save failure status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler.uploadDir = filepath.Join(blocker, "child")
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "note.txt", []byte("hello")))
	if response.Code != http.StatusInternalServerError || response.Header().Get("X-Error-Code") != "backend.upload.create_directory_failed" {
		t.Fatalf("directory failure status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestUploadFileRegistersLifecycleEvidenceAndRemovesUnregisteredContent(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.*"))
	handler := uploadTestHandler(t, principal)
	registry := &uploadArtifactStoreStub{}
	handler.artifacts = registry
	response := httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "note.txt", []byte("registered upload")))
	if response.Code != http.StatusCreated || registry.artifact.ID == "" || registry.artifact.WorkspaceID != "workspace-a" || registry.artifact.ObjectKey != "document" || registry.artifact.FieldKey != "file_url" || registry.artifact.Filename == "" || registry.artifact.SHA256 == "" {
		t.Fatalf("status=%d artifact=%#v", response.Code, registry.artifact)
	}

	registry.err = errors.New("registry unavailable")
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "failed.txt", []byte("unregistered upload")))
	if response.Code != http.StatusInternalServerError || response.Header().Get("X-Error-Code") != "backend.upload.register_failed" {
		t.Fatalf("status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	workspaceDir, err := handler.workspaceUploadDir(principal.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, registry.artifact.Filename)); !os.IsNotExist(err) {
		t.Fatalf("unregistered content remains: %v", err)
	}
}

func TestServeUploadedFileAndDownloadAuthorization(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("asset.read"))
	handler := uploadTestHandler(t, principal)
	workspaceDir, err := handler.workspaceUploadDir(principal.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "asset.txt"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/uploads/asset.txt?object_key=asset&field_key=file_url", nil)
	request.SetPathValue("filename", "asset.txt")
	response := httptest.NewRecorder()
	handler.serveUploadedFile(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "asset" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("served status=%d body=%q", response.Code, response.Body.String())
	}

	for _, test := range []struct {
		name      string
		principal principalmodel.Principal
		target    string
		filename  string
		status    int
	}{
		{name: "unknown principal", principal: principalmodel.Principal{}, target: "/uploads/asset.txt", filename: "asset.txt", status: http.StatusForbidden},
		{name: "invalid filename", principal: principal, target: "/uploads/x", filename: "..", status: http.StatusNotFound},
		{name: "dot filename", principal: principal, target: "/uploads/x", filename: ".", status: http.StatusNotFound},
		{name: "missing context", principal: principal, target: "/uploads/asset.txt", filename: "asset.txt", status: http.StatusForbidden},
		{name: "unknown object", principal: principal, target: "/uploads/asset.txt?object_key=missing&field_key=file_url", filename: "asset.txt", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := uploadTestHandler(t, test.principal)
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.SetPathValue("filename", test.filename)
			response := httptest.NewRecorder()
			h.serveUploadedFile(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d", response.Code, test.status)
			}
		})
	}

}

func TestUploadStorageIsIsolatedByWorkspace(t *testing.T) {
	principalA := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u", WorkspaceID: "workspace-a"}}, uploadTestRole("document.*", "asset.read"))
	handlerA := uploadTestHandler(t, principalA)
	response := httptest.NewRecorder()
	handlerA.uploadFile(response, multipartUploadRequest(t, "/files?object_key=document&field_key=file_url", "same.txt", []byte("same content")))
	if response.Code != http.StatusCreated {
		t.Fatalf("workspace-a upload status=%d", response.Code)
	}
	var payload uploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}

	principalB := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u", WorkspaceID: "workspace-b"}}, uploadTestRole("asset.read"))
	handlerB := uploadTestHandler(t, principalB)
	handlerB.uploadDir = handlerA.uploadDir
	dirA, err := handlerA.workspaceUploadDir(principalA.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	dirB, err := handlerB.workspaceUploadDir(principalB.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if dirA == dirB {
		t.Fatal("workspace storage roots must differ")
	}
	request := httptest.NewRequest(http.MethodGet, "/uploads/"+payload.Filename+"?object_key=asset&field_key=file_url", nil)
	request.SetPathValue("filename", payload.Filename)
	response = httptest.NewRecorder()
	handlerB.serveUploadedFile(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("workspace-b must not read workspace-a upload, status=%d", response.Code)
	}
}

func TestUploadHelpersAndRoutes(t *testing.T) {
	handler := uploadTestHandler(t, principalmodel.Principal{})
	handler.UseAccess(nil)
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "uploads", TemplateVersion: "1"})
	handler.UseAccess(uploadapplication.NewUploadAccessApplicationService(records.Applications().Schema, records.Applications().Audit, nil))
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/uploads/file.txt", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("registered download route status=%d", response.Code)
	}
}
