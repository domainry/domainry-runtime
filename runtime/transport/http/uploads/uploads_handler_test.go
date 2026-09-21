package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"syscall"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type uploadArtifactStoreStub struct {
	artifact  lifecyclecontract.UploadArtifact
	artifacts []lifecyclecontract.UploadArtifact
	err       error
}

type uploadFileScanStoreStub struct {
	byID map[string]lifecyclecontract.FileScanEvidence
	err  error
}

type uploadBlobStoreStub struct {
	content   map[string][]byte
	stageErr  error
	commitErr error
}

func newUploadBlobStoreStub() *uploadBlobStoreStub {
	return &uploadBlobStoreStub{content: map[string][]byte{}}
}

func (*uploadBlobStoreStub) Descriptor() runtimefile.AdapterDescriptor {
	return runtimefile.AdapterDescriptor{Provider: "test-memory", Revision: "v1"}
}

func (s *uploadBlobStoreStub) Stage(_ context.Context, request runtimefile.BlobStageRequest) (runtimefile.BlobInfo, error) {
	if s.stageErr != nil {
		return runtimefile.BlobInfo{}, s.stageErr
	}
	content, err := io.ReadAll(io.LimitReader(request.Content, request.MaxBytes+1))
	if err != nil {
		return runtimefile.BlobInfo{}, err
	}
	if int64(len(content)) > request.MaxBytes {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobTooLarge
	}
	digest := sha256.Sum256(content)
	key := "stage-" + request.StageID
	s.content[request.WorkspaceID+"\x00"+key] = append([]byte(nil), content...)
	return runtimefile.BlobInfo{WorkspaceID: request.WorkspaceID, BlobKey: key, ContentSHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}, nil
}

func (s *uploadBlobStoreStub) Commit(_ context.Context, request runtimefile.BlobCommitRequest) (runtimefile.BlobInfo, error) {
	if s.commitErr != nil {
		return runtimefile.BlobInfo{}, s.commitErr
	}
	stageIdentity := request.WorkspaceID + "\x00" + request.StageKey
	content, ok := s.content[stageIdentity]
	if !ok {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobNotFound
	}
	delete(s.content, stageIdentity)
	s.content[request.WorkspaceID+"\x00"+request.BlobKey] = append([]byte(nil), content...)
	return runtimefile.BlobInfo{WorkspaceID: request.WorkspaceID, BlobKey: request.BlobKey, ContentSHA256: request.ContentSHA256, Size: request.Size}, nil
}

func (s *uploadBlobStoreStub) Open(_ context.Context, workspaceID, blobKey string) (io.ReadCloser, error) {
	content, ok := s.content[workspaceID+"\x00"+blobKey]
	if !ok {
		return nil, runtimefile.ErrBlobNotFound
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (s *uploadBlobStoreStub) Stat(_ context.Context, workspaceID, blobKey string) (runtimefile.BlobInfo, error) {
	content, ok := s.content[workspaceID+"\x00"+blobKey]
	if !ok {
		return runtimefile.BlobInfo{}, runtimefile.ErrBlobNotFound
	}
	digest := sha256.Sum256(content)
	return runtimefile.BlobInfo{WorkspaceID: workspaceID, BlobKey: blobKey, ContentSHA256: hex.EncodeToString(digest[:]), Size: int64(len(content))}, nil
}

func (s *uploadBlobStoreStub) Delete(_ context.Context, workspaceID, blobKey string) error {
	delete(s.content, workspaceID+"\x00"+blobKey)
	return nil
}

func putUploadBlob(t *testing.T, handler *UploadsHandler, workspaceID, blobKey string, content []byte) runtimefile.BlobInfo {
	t.Helper()
	blobs := handler.blobs.(*uploadBlobStoreStub)
	blobs.content[workspaceID+"\x00"+blobKey] = append([]byte(nil), content...)
	info, err := blobs.Stat(t.Context(), workspaceID, blobKey)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func (s *uploadFileScanStoreStub) FindFileScan(_ context.Context, workspaceID, fileID string) (lifecyclecontract.FileScanEvidence, error) {
	if s.err != nil {
		return lifecyclecontract.FileScanEvidence{}, s.err
	}
	evidence, ok := s.byID[workspaceID+"\x00"+fileID]
	if !ok {
		return lifecyclecontract.FileScanEvidence{}, sql.ErrNoRows
	}
	return evidence, nil
}

func (s *uploadFileScanStoreStub) RecordFileScan(_ context.Context, evidence lifecyclecontract.FileScanEvidence) error {
	if s.byID == nil {
		s.byID = map[string]lifecyclecontract.FileScanEvidence{}
	}
	s.byID[evidence.WorkspaceID+"\x00"+evidence.FileID] = evidence
	return s.err
}

func (s *uploadArtifactStoreStub) RegisterUpload(_ context.Context, artifact lifecyclecontract.UploadArtifact) error {
	s.artifact = artifact
	s.artifacts = append(s.artifacts, artifact)
	return s.err
}

func (*uploadArtifactStoreStub) ReconcileUploadArtifacts(context.Context, lifecycleaccess.SystemScope, time.Time, int) (lifecyclecontract.UploadCleanupResult, error) {
	return lifecyclecontract.UploadCleanupResult{}, nil
}

func uploadTestHandler(t *testing.T, principal principalmodel.Principal) *UploadsHandler {
	t.Helper()
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "uploads", TemplateVersion: "1", Objects: uploadTestObjects()})
	return NewUploadsHandler(UploadsDependencies{
		Access: uploadapplication.NewUploadAccessApplicationService(records.Applications().Schema, records.Applications().Audit, uploadTestRecordQuery{}), Subjects: uploadapplication.NewUploadSubjectRegistry(uploadSubjectTestMemory{}), Scans: uploadapplication.NewFileScanReceiptVerifier(&uploadFileScanStoreStub{byID: map[string]lifecyclecontract.FileScanEvidence{"workspace-a\x00asset.txt": {FileID: "asset.txt", WorkspaceID: "workspace-a", Filename: "asset.txt", ObjectKey: "asset", FieldKey: "file_url", Status: lifecyclecontract.FileScanClean}, "workspace-a\x00file.txt": {FileID: "file.txt", WorkspaceID: "workspace-a", Filename: "file.txt", ObjectKey: "asset", FieldKey: "file_url", Status: lifecyclecontract.FileScanClean}}}, bytes.Repeat([]byte("k"), 32)), Blobs: newUploadBlobStoreStub(), Principal: func(*http.Request) principalmodel.Principal { return principal },
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
		{Key: "document", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"scan_required": false}}, {Key: "sensitive", Type: "boolean"}}},
		{Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"scan_required": false}}}},
	}
}

func uploadTestRole(permissions ...string) accessfixture.Bundle {
	role := accessfixture.Bundle{Permissions: permissions}
	for _, permission := range permissions {
		switch permission {
		case "document.create", "document.update":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "document", Scope: "all", Read: true, Write: true})
		case "document.read":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "document", Scope: "all", Read: true})
		case "asset.create", "asset.update":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "asset", Scope: "all", Read: true, Write: true})
		case "asset.read":
			role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{ObjectKey: "asset", Scope: "all", Read: true})
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
	unknown.uploadFile(response, httptest.NewRequest(http.MethodPost, "/uploads?object_key=document&field_key=file_url", nil))
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "backend.role.unknown" {
		t.Fatalf("unknown upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.update", "asset.update"))
	handler := uploadTestHandler(t, principal)
	for _, test := range []struct {
		name   string
		target string
		code   string
		status int
	}{
		{name: "missing context", target: "/uploads", code: "backend.upload.object_field_required", status: http.StatusBadRequest},
		{name: "missing field", target: "/uploads?object_key=document", code: "backend.upload.object_field_required", status: http.StatusBadRequest},
		{name: "unknown object", target: "/uploads?object_key=missing&field_key=file_url", code: "backend.object.not_found", status: http.StatusNotFound},
		{name: "unknown field", target: "/uploads?object_key=document&field_key=missing", code: "backend.upload.field_not_defined", status: http.StatusBadRequest},
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
	denied.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "file.txt", []byte("hello")))
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "backend.upload.permission_denied" {
		t.Fatalf("denied upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	response = httptest.NewRecorder()
	handler.uploadFile(response, httptest.NewRequest(http.MethodPost, "/uploads?object_key=document&field_key=file_url", bytes.NewBufferString("not multipart")))
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
	request := httptest.NewRequest(http.MethodPost, "/uploads?object_key=document&field_key=file_url", &noFileBody)
	request.Header.Set("Content-Type", noFileWriter.FormDataContentType())
	response = httptest.NewRecorder()
	handler.uploadFile(response, request)
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.file_required" {
		t.Fatalf("missing file status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "empty.txt", nil))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.empty_file" {
		t.Fatalf("empty upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "large.bin", bytes.Repeat([]byte{'x'}, maxUploadBytes+1)))
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
	unsupportedRequest := httptest.NewRequest(http.MethodPost, "/uploads?object_key=document&field_key=file_url", &unsupportedBody)
	unsupportedRequest.Header.Set("Content-Type", unsupportedWriter.FormDataContentType())
	handler.uploadFile(response, unsupportedRequest)
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.unsupported_file_type" {
		t.Fatalf("unsupported upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequestWithPartType(t, "/uploads?object_key=document&field_key=file_url", "audio.mp3", ";", append([]byte("ID3"), bytes.Repeat([]byte{0}, 600)...)))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.unsupported_file_type" {
		t.Fatalf("invalid media type status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestUploadReadFailureAndDetectedContentType(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.update"))
	handler := uploadTestHandler(t, principal)
	handler.blobs.(*uploadBlobStoreStub).stageErr = errors.New("read failed")
	response := httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "file.txt", []byte("content")))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.read_failed" {
		t.Fatalf("read failure status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	handler = uploadTestHandler(t, principal)
	handler.blobs.(*uploadBlobStoreStub).stageErr = syscall.ENOSPC
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "file.txt", []byte("content")))
	if response.Code != http.StatusInsufficientStorage || response.Header().Get("X-Error-Code") != "backend.upload.storage_exhausted" {
		t.Fatalf("disk full status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	handler = uploadTestHandler(t, principal)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 600)...)
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "image.png", png))
	if response.Code != http.StatusCreated {
		t.Fatalf("png upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestUploadFileSuccessAndStorageFailure(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.update"))
	handler := uploadTestHandler(t, principal)
	response := httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "note.txt", []byte("hello upload")))
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
	if _, err := handler.blobs.Stat(t.Context(), principal.WorkspaceID, payload.Filename); err != nil {
		t.Fatal(err)
	}
	handler.blobs.(*uploadBlobStoreStub).commitErr = errors.New("commit failed")
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "note.txt", []byte("hello upload")))
	if response.Code != http.StatusInternalServerError || response.Header().Get("X-Error-Code") != "backend.upload.save_failed" {
		t.Fatalf("save failure status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestRepeatedUploadGetsDistinctRegisteredFileIdentity(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.update"))
	handler := uploadTestHandler(t, principal)
	registry := &uploadArtifactStoreStub{}
	handler.artifacts = registry
	var payloads []uploadResponse
	for range 2 {
		response := httptest.NewRecorder()
		handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "same.png", append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 600)...)))
		if response.Code != http.StatusCreated {
			t.Fatalf("upload status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
		}
		var payload uploadResponse
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	if payloads[0].SHA256 != payloads[1].SHA256 || payloads[0].FileID == payloads[1].FileID || payloads[0].Filename == payloads[1].Filename {
		t.Fatalf("repeated upload identities=%#v", payloads)
	}
	if len(registry.artifacts) != 2 || registry.artifacts[0].ID != payloads[0].FileID || registry.artifacts[1].ID != payloads[1].FileID || registry.artifacts[0].Filename != payloads[0].Filename || registry.artifacts[1].Filename != payloads[1].Filename {
		t.Fatalf("registered artifacts=%#v payloads=%#v", registry.artifacts, payloads)
	}
}

func TestUploadFileRegistersLifecycleEvidenceAndRemovesUnregisteredContent(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("document.update"))
	handler := uploadTestHandler(t, principal)
	registry := &uploadArtifactStoreStub{}
	handler.artifacts = registry
	response := httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "note.txt", []byte("registered upload")))
	if response.Code != http.StatusCreated || registry.artifact.ID == "" || registry.artifact.WorkspaceID != "workspace-a" || registry.artifact.ObjectKey != "document" || registry.artifact.FieldKey != "file_url" || registry.artifact.Filename == "" || registry.artifact.SHA256 == "" {
		t.Fatalf("status=%d artifact=%#v", response.Code, registry.artifact)
	}

	registry.err = errors.New("registry unavailable")
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "failed.txt", []byte("unregistered upload")))
	if response.Code != http.StatusInternalServerError || response.Header().Get("X-Error-Code") != "backend.upload.register_failed" {
		t.Fatalf("status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	if _, err := handler.blobs.Stat(t.Context(), principal.WorkspaceID, registry.artifact.Filename); !errors.Is(err, runtimefile.ErrBlobNotFound) {
		t.Fatalf("unregistered content remains: %v", err)
	}
}

func TestServeUploadedFileAndDownloadAuthorization(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("asset.read"))
	handler := uploadTestHandler(t, principal)
	info := putUploadBlob(t, handler, principal.WorkspaceID, "asset.txt", []byte("asset"))
	handler.scans = uploadapplication.NewFileScanReceiptVerifier(&uploadFileScanStoreStub{byID: map[string]lifecyclecontract.FileScanEvidence{
		"workspace-a\x00asset.txt": {FileID: "asset.txt", WorkspaceID: "workspace-a", Filename: "asset.txt", ObjectKey: "asset", FieldKey: "file_url", Status: lifecyclecontract.FileScanClean, SHA256: info.ContentSHA256, Size: info.Size, ContentType: "text/plain"},
	}}, bytes.Repeat([]byte("k"), 32))
	request := httptest.NewRequest(http.MethodGet, "/uploads/asset.txt?object_key=asset&field_key=file_url&record_id=asset.txt", nil)
	request.SetPathValue("fileID", "asset.txt")
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
		{name: "unknown object", principal: principal, target: "/uploads/asset.txt?object_key=missing&field_key=file_url", filename: "asset.txt", status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := uploadTestHandler(t, test.principal)
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.SetPathValue("fileID", test.filename)
			response := httptest.NewRecorder()
			h.serveUploadedFile(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d", response.Code, test.status)
			}
		})
	}

}

func TestServeUploadedFileResolvesOpaqueFileIdentityInsideWorkspace(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("asset.read"))
	handler := uploadTestHandler(t, principal)
	storageFilename := "content-addressed-storage-name.zip"
	info := putUploadBlob(t, handler, principal.WorkspaceID, storageFilename, []byte("archive"))
	store := &uploadFileScanStoreStub{byID: map[string]lifecyclecontract.FileScanEvidence{
		"workspace-a\x00derived-export": {
			FileID: "derived-export", WorkspaceID: "workspace-a", Filename: storageFilename,
			ObjectKey: "asset", FieldKey: "file_url", Status: lifecyclecontract.FileScanClean, SHA256: info.ContentSHA256, Size: info.Size, ContentType: "application/zip",
		},
	}}
	handler.scans = uploadapplication.NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32))

	request := httptest.NewRequest(http.MethodGet, "/uploads/derived-export?object_key=asset&field_key=file_url&record_id=derived-export", nil)
	request.SetPathValue("fileID", "derived-export")
	response := httptest.NewRecorder()
	handler.serveUploadedFile(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "archive" {
		t.Fatalf("opaque file identity status=%d body=%q", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/uploads/derived-export?object_key=document&field_key=file_url", nil)
	request.SetPathValue("fileID", "derived-export")
	response = httptest.NewRecorder()
	handler.serveUploadedFile(response, request)
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "backend.upload.permission_denied" {
		t.Fatalf("artifact binding mismatch status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestServeUploadedFileAcceptsExactActionDownloadTicketWithoutNativeRecordGrant(t *testing.T) {
	now := time.Date(2026, 9, 13, 5, 6, 7, 0, time.UTC)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "listed-member", WorkspaceID: "workspace-a"}}, uploadTestRole("document.read"))
	handler := uploadTestHandler(t, principal)
	storageFilename := "stored-document.pdf"
	info := putUploadBlob(t, handler, principal.WorkspaceID, storageFilename, []byte("authorized bytes"))
	store := &uploadFileScanStoreStub{byID: map[string]lifecyclecontract.FileScanEvidence{
		"workspace-a\x00opaque-document-file": {
			FileID: "opaque-document-file", WorkspaceID: "workspace-a", Filename: storageFilename,
			ObjectKey: "document_upload", FieldKey: "file_url", Status: lifecyclecontract.FileScanClean, SHA256: info.ContentSHA256, Size: info.Size, ContentType: "application/pdf",
		},
	}}
	handler.scans = uploadapplication.NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32))
	tickets, err := uploadapplication.NewFileDownloadTicketService(bytes.Repeat([]byte("t"), 32), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	handler.tickets = tickets
	ticket, err := tickets.Issue(t.Context(), "workspace-a", principal.UserID, principal.EffectiveAuthorizationRevision(), runtimeext.FileDownloadRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{FileID: "opaque-document-file", ContentSHA256: strings.Repeat("a", 64), ScanReceipt: "receipt"},
		Binding:                 runtimeext.FileRecordBinding{ObjectKey: "document_file_version", RecordID: "version-1", FileIDField: "runtime_file_id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, ticket.ProtectedDownload, nil)
	request.SetPathValue("fileID", "opaque-document-file")
	response := httptest.NewRecorder()
	handler.serveUploadedFile(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "authorized bytes" {
		t.Fatalf("ticket download status=%d body=%q code=%q", response.Code, response.Body.String(), response.Header().Get("X-Error-Code"))
	}

	other := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "other-member", WorkspaceID: "workspace-a"}}, uploadTestRole("document.read"))
	handler.principal = func(*http.Request) principalmodel.Principal { return other }
	request = httptest.NewRequest(http.MethodGet, ticket.ProtectedDownload, nil)
	request.SetPathValue("fileID", "opaque-document-file")
	response = httptest.NewRecorder()
	handler.serveUploadedFile(response, request)
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "backend.upload.download_ticket_invalid" {
		t.Fatalf("cross-user ticket status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
}

func TestUploadStorageIsIsolatedByWorkspace(t *testing.T) {
	principalA := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u", WorkspaceID: "workspace-a"}}, uploadTestRole("document.update", "asset.read"))
	handlerA := uploadTestHandler(t, principalA)
	response := httptest.NewRecorder()
	handlerA.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "same.txt", []byte("same content")))
	if response.Code != http.StatusCreated {
		t.Fatalf("workspace-a upload status=%d", response.Code)
	}
	var payload uploadResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}

	principalB := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u", WorkspaceID: "workspace-b"}}, uploadTestRole("asset.read"))
	handlerB := uploadTestHandler(t, principalB)
	handlerB.blobs = handlerA.blobs
	request := httptest.NewRequest(http.MethodGet, "/uploads/"+payload.Filename+"?object_key=asset&field_key=file_url", nil)
	request.SetPathValue("fileID", payload.FileID)
	response = httptest.NewRecorder()
	handlerB.serveUploadedFile(response, request)
	if response.Code != http.StatusForbidden {
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

type handoffReviewCatalog struct{}

func (handoffReviewCatalog) ObjectMap(context.Context) map[string]definitionmodel.ObjectSchema {
	return map[string]definitionmodel.ObjectSchema{"asset": {Key: "asset", Fields: []definitionmodel.FieldSchema{{Key: "file_url", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"scan_required": false}}}}}
}

type handoffReviewAudit struct{}

func (handoffReviewAudit) AppendWithMetadata(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
}

type handoffReviewRecord struct{}

func (handoffReviewRecord) GetRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{ID: "own-record", Data: map[string]any{"file_url": uploadRecordFileReference("review-file", "review.png")}}, nil
}
func TestServeUploadedFileRejectsNonCleanRecordBoundDownloads(t *testing.T) {
	for _, status := range []string{"clean", "pending", "quarantined", "failed"} {
		t.Run(status, func(t *testing.T) {
			principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "u1", WorkspaceID: "workspace-a"}}, uploadTestRole("asset.read"))
			handler := uploadTestHandler(t, principal)
			handler.access = uploadapplication.NewUploadAccessApplicationService(handoffReviewCatalog{}, handoffReviewAudit{}, handoffReviewRecord{})
			info := putUploadBlob(t, handler, principal.WorkspaceID, "review.png", []byte("review bytes"))
			store := &uploadFileScanStoreStub{byID: map[string]lifecyclecontract.FileScanEvidence{"workspace-a\x00review-file": {FileID: "review-file", WorkspaceID: "workspace-a", Filename: "review.png", ObjectKey: "asset", FieldKey: "file_url", Status: status, SHA256: info.ContentSHA256, Size: info.Size, ContentType: "image/png"}}}
			handler.scans = uploadapplication.NewFileScanReceiptVerifier(store, bytes.Repeat([]byte("k"), 32))
			request := httptest.NewRequest(http.MethodGet, "/uploads/review-file?object_key=asset&field_key=file_url&record_id=own-record", nil)
			request.SetPathValue("fileID", "review-file")
			response := httptest.NewRecorder()
			handler.serveUploadedFile(response, request)
			if status == "clean" {
				if response.Code != 200 {
					t.Fatalf("clean control returned %d", response.Code)
				}
				return
			}
			if response.Code == 200 {
				t.Errorf("unsafe download allowed: status=%s HTTP=%d bytes=%q with valid record_id", status, response.Code, response.Body.String())
			}
		})
	}
}

type uploadSubjectTestMemory map[string]uploadapplication.UploadSubjectBinding

func (s uploadSubjectTestMemory) InsertUploadSubject(_ context.Context, value uploadapplication.UploadSubjectBinding) error {
	s[value.WorkspaceID+"\x00"+value.FileID] = value
	return nil
}
func (s uploadSubjectTestMemory) FindUploadSubject(_ context.Context, workspace, id string) (uploadapplication.UploadSubjectBinding, error) {
	for _, value := range s {
		if value.WorkspaceID == workspace && (value.FileID == id || value.Filename == id) {
			return value, nil
		}
	}
	return uploadapplication.UploadSubjectBinding{}, sql.ErrNoRows
}

type uploadTestRecordQuery struct{}

func (uploadTestRecordQuery) GetRecord(_ context.Context, object, id string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{ID: id, OwnerUserID: principal.UserID, Data: map[string]any{"file_url": uploadRecordFileReference(id, id)}}, nil
}

func uploadRecordFileReference(fileID, filename string) map[string]any {
	return map[string]any{
		"file_id": fileID, "filename": filename, "content_type": "application/octet-stream",
		"size": int64(1), "content_sha256": strings.Repeat("a", 64),
	}
}
