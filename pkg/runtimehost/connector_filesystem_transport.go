package runtimehost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	connector "github.com/domainry/domainry-connector-sdk"
)

const maxConnectorFilesystemReadBytes = int64(64 << 20)

func (t *connectorTransport) ExecuteFilesystem(ctx context.Context, request connector.FilesystemRequest) (connector.FilesystemResult, error) {
	if t == nil {
		return connector.FilesystemResult{}, errors.New("Connector filesystem transport is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return connector.FilesystemResult{}, err
	}
	rootPath := strings.TrimSpace(request.Root)
	if rootPath == "" || !filepath.IsAbs(rootPath) {
		return connector.FilesystemResult{}, errors.New("Connector filesystem root must be absolute")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("open Connector filesystem root: %w", err)
	}
	defer root.Close()
	if request.Operation == connector.FilesystemOperationProbe {
		return connector.FilesystemResult{Exists: true}, nil
	}
	relative, err := connectorFilesystemPath(request.Path)
	if err != nil {
		return connector.FilesystemResult{}, err
	}
	switch request.Operation {
	case connector.FilesystemOperationRead:
		return readConnectorFilesystem(ctx, root, relative, request.MaxReadBytes)
	case connector.FilesystemOperationWrite:
		return writeConnectorFilesystem(ctx, root, relative, request.Content)
	case connector.FilesystemOperationDelete:
		err := root.Remove(relative)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return connector.FilesystemResult{}, fmt.Errorf("delete Connector filesystem entry: %w", err)
		}
		return connector.FilesystemResult{Path: filepath.ToSlash(relative), Exists: false}, nil
	default:
		return connector.FilesystemResult{}, errors.New("Connector filesystem operation is unsupported")
	}
}

func connectorFilesystemPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return "", errors.New("Connector filesystem path must be relative")
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("Connector filesystem path escapes its root")
	}
	return cleaned, nil
}

func readConnectorFilesystem(ctx context.Context, root *os.Root, relative string, limit int64) (connector.FilesystemResult, error) {
	if limit < 1 || limit > maxConnectorFilesystemReadBytes {
		return connector.FilesystemResult{}, fmt.Errorf("Connector filesystem read limit must be between 1 and %d bytes", maxConnectorFilesystemReadBytes)
	}
	file, err := root.Open(relative)
	if err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("open Connector filesystem entry: %w", err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("read Connector filesystem entry: %w", err)
	}
	if int64(len(content)) > limit {
		return connector.FilesystemResult{}, fmt.Errorf("Connector filesystem entry exceeds %d bytes", limit)
	}
	if err := ctx.Err(); err != nil {
		return connector.FilesystemResult{}, err
	}
	return connector.FilesystemResult{Path: filepath.ToSlash(relative), Content: content, Size: int64(len(content)), Exists: true}, nil
}

func writeConnectorFilesystem(ctx context.Context, root *os.Root, relative string, content []byte) (connector.FilesystemResult, error) {
	directory := filepath.Dir(relative)
	if err := root.MkdirAll(directory, 0o750); err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("create Connector filesystem directory: %w", err)
	}
	stage, stageName, err := createConnectorFilesystemStage(root, directory, filepath.Base(relative))
	if err != nil {
		return connector.FilesystemResult{}, err
	}
	committed := false
	defer func() {
		_ = stage.Close()
		if !committed {
			_ = root.Remove(stageName)
		}
	}()
	if _, err := stage.Write(content); err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("write Connector filesystem stage: %w", err)
	}
	if err := stage.Sync(); err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("sync Connector filesystem stage: %w", err)
	}
	if err := stage.Close(); err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("close Connector filesystem stage: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return connector.FilesystemResult{}, err
	}
	if err := root.Rename(stageName, relative); err != nil {
		return connector.FilesystemResult{}, fmt.Errorf("publish Connector filesystem entry: %w", err)
	}
	committed = true
	if directoryHandle, err := root.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return connector.FilesystemResult{Path: filepath.ToSlash(relative), Size: int64(len(content)), Exists: true}, nil
}

func createConnectorFilesystemStage(root *os.Root, directory, filename string) (*os.File, string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		random := make([]byte, 12)
		if _, err := rand.Read(random); err != nil {
			return nil, "", fmt.Errorf("create Connector filesystem stage name: %w", err)
		}
		name := filepath.Join(directory, ".domainry-stage-"+filename+"-"+hex.EncodeToString(random))
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", fmt.Errorf("create Connector filesystem stage: %w", err)
		}
	}
	return nil, "", errors.New("create Connector filesystem stage: name collisions exhausted")
}
