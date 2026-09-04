package uploads

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type uploadErrorReader struct{ err error }

func (reader uploadErrorReader) Read([]byte) (int, error) { return 0, reader.err }

type uploadErrorWriter struct{ err error }

func (writer uploadErrorWriter) Write([]byte) (int, error) { return 0, writer.err }

type uploadTemporaryFileStub struct {
	bytes.Buffer
	name     string
	closeErr error
}

func (file *uploadTemporaryFileStub) Name() string { return file.name }
func (file *uploadTemporaryFileStub) Close() error { return file.closeErr }

func TestUploadRequestLimitPrefixAndWriterFailures(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, uploadTestRole("document.update"))
	handler := uploadTestHandler(t, principal)
	response := httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "huge.bin", bytes.Repeat([]byte{'x'}, maxUploadRequestBytes+1)))
	if response.Code != http.StatusRequestEntityTooLarge || response.Header().Get("X-Error-Code") != "backend.upload.file_too_large" {
		t.Fatalf("request limit status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	prefixErr := errors.New("prefix read failed")
	if _, err := readUploadPrefix(uploadErrorReader{err: prefixErr}); !errors.Is(err, prefixErr) {
		t.Fatalf("prefix error=%v", err)
	}
	handler.readPrefix = func(io.Reader) ([]byte, error) { return nil, prefixErr }
	response = httptest.NewRecorder()
	handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "file.txt", []byte("content")))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "backend.upload.read_failed" {
		t.Fatalf("prefix handler status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	if prefix, err := readUploadPrefix(bytes.NewReader([]byte("short"))); err != nil || string(prefix) != "short" {
		t.Fatalf("short prefix=%q err=%v", prefix, err)
	}
	if prefix, err := readUploadPrefix(bytes.NewReader(bytes.Repeat([]byte{'x'}, 512))); err != nil || len(prefix) != 512 {
		t.Fatalf("full prefix length=%d err=%v", len(prefix), err)
	}
	writeErr := errors.New("prefix write failed")
	if _, err := copyBoundedUpload(uploadErrorWriter{err: writeErr}, []byte("prefix"), bytes.NewReader(nil), io.Copy); !errors.Is(err, writeErr) {
		t.Fatalf("prefix write error=%v", err)
	}
}

func TestUploadStorageFailureMappingAtEveryFilesystemStage(t *testing.T) {
	originalMkdirAll, originalCreateTemp, originalRename, originalAbs := uploadMkdirAll, uploadCreateTemp, uploadRename, uploadAbs
	t.Cleanup(func() {
		uploadMkdirAll, uploadCreateTemp, uploadRename, uploadAbs = originalMkdirAll, originalCreateTemp, originalRename, originalAbs
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, uploadTestRole("document.update"))

	request := func(t *testing.T, handler *UploadsHandler) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.uploadFile(response, multipartUploadRequest(t, "/uploads?object_key=document&field_key=file_url", "file.txt", []byte("content")))
		return response
	}
	assertError := func(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if response.Code != status || response.Header().Get("X-Error-Code") != code {
			t.Fatalf("status=%d code=%q want=%d/%q", response.Code, response.Header().Get("X-Error-Code"), status, code)
		}
	}

	uploadMkdirAll = func(string, os.FileMode) error { return syscall.ENOSPC }
	assertError(t, request(t, uploadTestHandler(t, principal)), http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
	uploadMkdirAll = originalMkdirAll

	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "create temp exhausted", err: syscall.EDQUOT, status: http.StatusInsufficientStorage, code: "backend.upload.storage_exhausted"},
		{name: "create temp failed", err: errors.New("create failed"), status: http.StatusInternalServerError, code: "backend.upload.save_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			uploadCreateTemp = func(string, string) (uploadTemporaryFile, error) { return nil, test.err }
			assertError(t, request(t, uploadTestHandler(t, principal)), test.status, test.code)
		})
	}
	uploadCreateTemp = originalCreateTemp

	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "close exhausted", err: syscall.ENOSPC, status: http.StatusInsufficientStorage, code: "backend.upload.storage_exhausted"},
		{name: "close failed", err: errors.New("close failed"), status: http.StatusInternalServerError, code: "backend.upload.save_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			uploadCreateTemp = func(dir, _ string) (uploadTemporaryFile, error) {
				return &uploadTemporaryFileStub{name: filepath.Join(dir, "temporary"), closeErr: test.err}, nil
			}
			assertError(t, request(t, uploadTestHandler(t, principal)), test.status, test.code)
		})
	}
	uploadCreateTemp = originalCreateTemp

	uploadRename = func(string, string) error { return syscall.EDQUOT }
	assertError(t, request(t, uploadTestHandler(t, principal)), http.StatusInsufficientStorage, "backend.upload.storage_exhausted")
	uploadRename = originalRename

	uploadAbs = func(string) (string, error) { return "", errors.New("absolute path failed") }
	assertError(t, request(t, uploadTestHandler(t, principal)), http.StatusForbidden, "backend.workspace_scope_required")
	downloadHandler := uploadTestHandler(t, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, uploadTestRole("asset.read")))
	downloadRequest := httptest.NewRequest(http.MethodGet, "/uploads/file.txt?object_key=asset&field_key=file_url", nil)
	downloadRequest.SetPathValue("filename", "file.txt")
	downloadResponse := httptest.NewRecorder()
	downloadHandler.serveUploadedFile(downloadResponse, downloadRequest)
	assertError(t, downloadResponse, http.StatusForbidden, "backend.workspace_scope_required")
}

func TestWorkspaceUploadDirValidationAndStorageErrorClassification(t *testing.T) {
	handler := uploadTestHandler(t, principalmodel.Principal{})
	if _, err := handler.workspaceUploadDir(" "); err == nil {
		t.Fatal("blank workspace was accepted")
	}
	handler.uploadDir = " "
	if _, err := handler.workspaceUploadDir("workspace-a"); err == nil {
		t.Fatal("blank upload root was accepted")
	}
	if uploadStorageExhausted(errors.New("ordinary failure")) {
		t.Fatal("ordinary failure classified as storage exhaustion")
	}
}
