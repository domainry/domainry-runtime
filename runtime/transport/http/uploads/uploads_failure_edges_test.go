package uploads

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestUploadStorageFailureMappingAtAdapterStages(t *testing.T) {
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

	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "stage exhausted", err: syscall.EDQUOT, status: http.StatusInsufficientStorage, code: "backend.upload.storage_exhausted"},
		{name: "stage read failed", err: errors.New("stage failed"), status: http.StatusBadRequest, code: "backend.upload.read_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := uploadTestHandler(t, principal)
			handler.blobs.(*uploadBlobStoreStub).stageErr = test.err
			assertError(t, request(t, handler), test.status, test.code)
		})
	}

	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "commit exhausted", err: syscall.ENOSPC, status: http.StatusInsufficientStorage, code: "backend.upload.storage_exhausted"},
		{name: "commit failed", err: errors.New("commit failed"), status: http.StatusInternalServerError, code: "backend.upload.save_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := uploadTestHandler(t, principal)
			handler.blobs.(*uploadBlobStoreStub).commitErr = test.err
			assertError(t, request(t, handler), test.status, test.code)
		})
	}

	handler := uploadTestHandler(t, principal)
	handler.blobs = nil
	assertError(t, request(t, handler), http.StatusServiceUnavailable, "backend.upload.storage_unavailable")
}

func TestUploadStorageErrorClassification(t *testing.T) {
	if uploadStorageExhausted(errors.New("ordinary failure")) {
		t.Fatal("ordinary failure classified as storage exhaustion")
	}
}
