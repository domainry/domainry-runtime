package runtimehost

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagedFrontendServesHTMLNavigationAndAssetsWithoutOwningAPIRoutes(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "domainry-runtime")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, filepath.FromSlash(projectFrontendBundleRelativePath))
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for name, content := range map[string]string{
		"index.html":            "<!doctype html><div id=\"root\"></div>",
		"assets/application.js": "globalThis.__domainry = true",
		"asset-manifest.json":   `{"contract_version":"domainry-asset-manifest-v1"}`,
	} {
		value := []byte(content)
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(value)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(buffer.Bytes())
	assets, err := loadProjectFrontendAssets(binary, hex.EncodeToString(digest[:]), os.ReadFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadProjectFrontendAssets(binary, strings.Repeat("0", 64), os.ReadFile); err == nil {
		t.Fatal("tampered packaged frontend identity must be rejected")
	}
	forwardedPath := ""
	handler := assets.wrap(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		forwardedPath = request.URL.Path
		writer.WriteHeader(http.StatusTeapot)
		_, _ = writer.Write([]byte("api"))
	}))
	for _, path := range []string{"/", "/orders", "/orders/one"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept", "text/html,application/xhtml+xml")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`id="root"`)) {
			t.Fatalf("%s response=%d %q", path, response.Code, response.Body.String())
		}
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/application.js", nil))
	if asset.Code != http.StatusOK || asset.Body.String() != "globalThis.__domainry = true" {
		t.Fatalf("asset response=%d %q", asset.Code, asset.Body.String())
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/records/orders", nil))
	if api.Code != http.StatusTeapot || api.Body.String() != "api" {
		t.Fatalf("api response=%d %q", api.Code, api.Body.String())
	}
	prefixedAPI := httptest.NewRecorder()
	handler.ServeHTTP(prefixedAPI, httptest.NewRequest(http.MethodGet, "/api/records/orders", nil))
	if prefixedAPI.Code != http.StatusTeapot || forwardedPath != "/records/orders" {
		t.Fatalf("prefixed API response=%d path=%q", prefixedAPI.Code, forwardedPath)
	}
	post := httptest.NewRecorder()
	postRequest := httptest.NewRequest(http.MethodPost, "/orders", nil)
	postRequest.Header.Set("Accept", "text/html")
	handler.ServeHTTP(post, postRequest)
	if post.Code != http.StatusTeapot {
		t.Fatalf("non-GET frontend path response=%d", post.Code)
	}
	jsonRequest := httptest.NewRecorder()
	handler.ServeHTTP(jsonRequest, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if jsonRequest.Code != http.StatusTeapot {
		t.Fatalf("non-HTML frontend path response=%d", jsonRequest.Code)
	}
}

func TestPackagedFrontendRejectsUnsafeArchiveEntries(t *testing.T) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	content := []byte("unsafe")
	if err := writer.WriteHeader(&tar.Header{Name: "../index.html", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := loadProjectFrontendAssets("/release/bin/runtime", "", func(string) ([]byte, error) {
		return buffer.Bytes(), nil
	})
	if err == nil {
		t.Fatal("unsafe frontend archive must be rejected")
	}
}
