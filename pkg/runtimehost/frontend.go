package runtimehost

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const projectFrontendBundleRelativePath = "frontend/source-owned-app.tar"

type projectFrontendAssets struct {
	files map[string][]byte
}

func loadProjectFrontendAssets(executablePath, expectedSHA256 string, readFile func(string) ([]byte, error)) (*projectFrontendAssets, error) {
	if strings.TrimSpace(executablePath) == "" || readFile == nil {
		return nil, nil
	}
	executableDir := filepath.Dir(executablePath)
	candidates := []string{
		filepath.Join(executableDir, "..", filepath.FromSlash(projectFrontendBundleRelativePath)),
		filepath.Join(executableDir, filepath.FromSlash(projectFrontendBundleRelativePath)),
	}
	var content []byte
	var loadedPath string
	for _, candidate := range candidates {
		value, err := readFile(candidate)
		if err == nil {
			content, loadedPath = value, candidate
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read packaged frontend bundle %s: %w", candidate, err)
		}
	}
	if loadedPath == "" {
		if expectedSHA256 != "" {
			return nil, fmt.Errorf("attested packaged frontend bundle is missing")
		}
		return nil, nil
	}
	digest := sha256.Sum256(content)
	if expectedSHA256 != "" && hex.EncodeToString(digest[:]) != expectedSHA256 {
		return nil, fmt.Errorf("packaged frontend bundle differs from Runtime attestation")
	}
	if len(content) == 0 || len(content) > 128<<20 {
		return nil, fmt.Errorf("packaged frontend bundle size is invalid")
	}
	files := map[string][]byte{}
	reader := tar.NewReader(bytes.NewReader(content))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode packaged frontend bundle: %w", err)
		}
		name := filepath.ToSlash(filepath.Clean(header.Name))
		if header.Typeflag != tar.TypeReg || name == "." || name != header.Name || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
			return nil, fmt.Errorf("packaged frontend entry %q is invalid", header.Name)
		}
		if _, duplicate := files[name]; duplicate {
			return nil, fmt.Errorf("packaged frontend entry %q is duplicated", name)
		}
		if header.Size < 0 || header.Size > 32<<20 || len(files) >= 10000 {
			return nil, fmt.Errorf("packaged frontend entry %q exceeds limits", name)
		}
		value, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if err != nil || int64(len(value)) != header.Size {
			return nil, fmt.Errorf("read packaged frontend entry %q", name)
		}
		files[name] = value
	}
	if len(files["index.html"]) == 0 || len(files["asset-manifest.json"]) == 0 {
		return nil, fmt.Errorf("packaged frontend bundle is missing index.html or asset-manifest.json")
	}
	return &projectFrontendAssets{files: files}, nil
}

func (a *projectFrontendAssets) wrap(next http.Handler, hasProjectHTTP bool) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if backendPath, normalize := runtimeOwnedFrontendAPIPath(request.URL.Path, hasProjectHTTP); normalize {
			cloned := request.Clone(request.Context())
			cloned.URL.Path = backendPath
			next.ServeHTTP(newNormalizedAPIResponseWriter(writer, request.URL.Path, backendPath), cloned)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			next.ServeHTTP(writer, request)
			return
		}
		path := strings.TrimPrefix(request.URL.Path, "/")
		if value, ok := a.files[path]; ok {
			serveProjectFrontendAsset(writer, request, path, value)
			return
		}
		if acceptsHTML(request.Header.Get("Accept")) {
			serveProjectFrontendAsset(writer, request, "index.html", a.files["index.html"])
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type normalizedAPIResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func newNormalizedAPIResponseWriter(writer http.ResponseWriter, browserPath, backendPath string) http.ResponseWriter {
	if !strings.HasPrefix(browserPath, "/api/auth/") || !strings.HasPrefix(backendPath, "/auth/") {
		return writer
	}
	return &normalizedAPIResponseWriter{ResponseWriter: writer}
}

func (writer *normalizedAPIResponseWriter) WriteHeader(status int) {
	if !writer.wroteHeader {
		writer.rewriteCookiePaths()
		writer.wroteHeader = true
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *normalizedAPIResponseWriter) Write(content []byte) (int, error) {
	if !writer.wroteHeader {
		writer.rewriteCookiePaths()
		writer.wroteHeader = true
	}
	return writer.ResponseWriter.Write(content)
}

func (writer *normalizedAPIResponseWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *normalizedAPIResponseWriter) rewriteCookiePaths() {
	values := writer.Header().Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	writer.Header().Del("Set-Cookie")
	for _, value := range values {
		attributes := strings.Split(value, ";")
		for index, attribute := range attributes {
			if strings.TrimSpace(attribute) == "Path=/auth" {
				attributes[index] = strings.Repeat(" ", len(attribute)-len(strings.TrimLeft(attribute, " "))) + "Path=/api/auth"
			}
		}
		writer.Header().Add("Set-Cookie", strings.Join(attributes, ";"))
	}
}

// runtimeOwnedFrontendAPIPath keeps one browser-facing /api surface without
// stealing ProjectHTTP's source-owned /api namespace. Runtime and embedded
// owner modules publish canonical root paths; only those stable owner prefixes
// are normalized. Every other /api path remains unchanged for ProjectHTTP.
func runtimeOwnedFrontendAPIPath(path string, hasProjectHTTP bool) (string, bool) {
	if path == "/api" {
		return "/", true
	}
	if !strings.HasPrefix(path, "/api/") {
		return "", false
	}
	backendPath := strings.TrimPrefix(path, "/api")
	for _, prefix := range []string{
		"/agent",
		"/auth",
		"/data-exchange",
		"/discovery",
		"/identity",
		"/integration",
		"/lifecycle",
		"/metadata",
		"/monitoring",
		"/notification",
		"/operations",
		"/organization",
		"/records",
		"/report",
		"/scheduler",
		"/uploads",
		"/workflow",
	} {
		if hasProjectHTTP && prefix == "/records" {
			continue
		}
		if backendPath == prefix || strings.HasPrefix(backendPath, prefix+"/") {
			return backendPath, true
		}
	}
	return "", false
}

func acceptsHTML(accept string) bool {
	for _, mediaRange := range strings.Split(accept, ",") {
		mediaType := strings.TrimSpace(strings.SplitN(mediaRange, ";", 2)[0])
		if strings.EqualFold(mediaType, "text/html") || strings.EqualFold(mediaType, "application/xhtml+xml") {
			return true
		}
	}
	return false
}

func serveProjectFrontendAsset(writer http.ResponseWriter, request *http.Request, name string, content []byte) {
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" && name == "index.html" {
		contentType = "text/html; charset=utf-8"
	}
	if contentType != "" {
		writer.Header().Set("Content-Type", contentType)
	}
	if name == "index.html" {
		writer.Header().Set("Cache-Control", "no-cache")
	} else {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(writer, request, name, time.Unix(0, 0), bytes.NewReader(content))
}
