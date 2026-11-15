package runtimehost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	connector "github.com/domainry/domainry-connector-sdk"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectorTransportSendsSDKContentOverRealHTTP(t *testing.T) {
	body := []byte("# 已审核知识\n\x00raw binary\xff\n")
	digest := sha256.Sum256(body)
	var received []byte
	var receivedLength int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedLength = r.ContentLength
		received, _ = io.ReadAll(r.Body)
		if r.Header.Get("Content-Type") != "application/octet-stream" || r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("lost transport headers")
		}
		_, _ = w.Write([]byte(`{"err_code":0}`))
	}))
	defer server.Close()
	response, err := newConnectorTransport().RoundTripHTTP(t.Context(), connector.HTTPRequest{Method: http.MethodPost, URL: server.URL, Headers: map[string][]string{"Content-Type": {"application/octet-stream"}}, SecretHeaders: map[string][]string{"Authorization": {"Bearer test-only"}}, Content: bytes.NewReader(body), ContentLength: int64(len(body)), ContentSHA256: hex.EncodeToString(digest[:])})
	if err != nil || response.StatusCode != 200 || receivedLength != int64(len(body)) || !bytes.Equal(received, body) {
		t.Fatalf("content stream lost: error=%v length=%d bytes=%d", err, receivedLength, len(received))
	}
}

func TestConnectorTransportRejectsAmbiguousContentBeforeHTTP(t *testing.T) {
	for _, mutate := range []func(*connector.HTTPRequest){
		func(q *connector.HTTPRequest) { q.Body = []byte("second body") },
		func(q *connector.HTTPRequest) { q.SecretForm = map[string]string{"key": "private"} },
		func(q *connector.HTTPRequest) { q.SecretJSON = map[string]string{"key": "private"} },
		func(q *connector.HTTPRequest) { q.ContentLength = 0 },
		func(q *connector.HTTPRequest) { q.ContentSHA256 = "bad" },
		func(q *connector.HTTPRequest) { q.Content = nil },
	} {
		q := connector.HTTPRequest{Method: http.MethodPost, URL: "https://must-not-dispatch.invalid", Content: bytes.NewReader([]byte("x")), ContentLength: 1, ContentSHA256: hex.EncodeToString(make([]byte, 32))}
		mutate(&q)
		if _, err := connectorHTTPRequestBody(q); err == nil {
			t.Fatal("ambiguous content accepted")
		}
	}
}
