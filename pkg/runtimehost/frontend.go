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

const projectFrontendBundleRelativePath = "frontend/source-owned-business.tar"

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
	if len(files["index.html"]) == 0 || len(files["surface-asset-manifest.json"]) == 0 {
		return nil, fmt.Errorf("packaged frontend bundle is missing index.html or surface-asset-manifest.json")
	}
	return &projectFrontendAssets{files: files}, nil
}

func (a *projectFrontendAssets) wrap(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api" || strings.HasPrefix(request.URL.Path, "/api/") {
			cloned := request.Clone(request.Context())
			cloned.URL.Path = strings.TrimPrefix(request.URL.Path, "/api")
			if cloned.URL.Path == "" {
				cloned.URL.Path = "/"
			}
			next.ServeHTTP(writer, cloned)
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
		if request.URL.Path == "/business" || strings.HasPrefix(request.URL.Path, "/business/") ||
			request.URL.Path == "/portal" || strings.HasPrefix(request.URL.Path, "/portal/") {
			serveProjectFrontendAsset(writer, request, "index.html", a.files["index.html"])
			return
		}
		next.ServeHTTP(writer, request)
	})
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
