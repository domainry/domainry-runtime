package runtimehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	connector "github.com/domainry/domainry-connector-sdk"
)

func TestConnectorFilesystemTransportReadWriteDeleteAndProbe(t *testing.T) {
	root := t.TempDir()
	transport := newConnectorTransport().(connector.FilesystemTransport)
	if result, err := transport.ExecuteFilesystem(t.Context(), connector.FilesystemRequest{Root: root, Operation: connector.FilesystemOperationProbe}); err != nil || !result.Exists {
		t.Fatalf("probe=%+v err=%v", result, err)
	}
	write := connector.FilesystemRequest{Root: root, Path: "evidence/a.txt", Operation: connector.FilesystemOperationWrite, Content: []byte("approved")}
	if result, err := transport.ExecuteFilesystem(t.Context(), write); err != nil || result.Path != "evidence/a.txt" || result.Size != 8 {
		t.Fatalf("write=%+v err=%v", result, err)
	}
	if result, err := transport.ExecuteFilesystem(t.Context(), connector.FilesystemRequest{Root: root, Path: "evidence/a.txt", Operation: connector.FilesystemOperationRead, MaxReadBytes: 8}); err != nil || string(result.Content) != "approved" {
		t.Fatalf("read=%+v err=%v", result, err)
	}
	if _, err := transport.ExecuteFilesystem(t.Context(), connector.FilesystemRequest{Root: root, Path: "evidence/a.txt", Operation: connector.FilesystemOperationDelete}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "evidence", "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted entry remains: %v", err)
	}
}

func TestConnectorFilesystemTransportRejectsEscapeSymlinksLimitsAndCancellation(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	transport := newConnectorTransport().(connector.FilesystemTransport)
	for _, request := range []connector.FilesystemRequest{
		{Root: "relative", Operation: connector.FilesystemOperationProbe},
		{Root: root, Path: "../escape", Operation: connector.FilesystemOperationRead, MaxReadBytes: 1},
		{Root: root, Path: "/absolute", Operation: connector.FilesystemOperationRead, MaxReadBytes: 1},
		{Root: root, Path: "a", Operation: "unknown"},
	} {
		if _, err := transport.ExecuteFilesystem(t.Context(), request); err == nil {
			t.Fatalf("accepted request=%+v", request)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.ExecuteFilesystem(t.Context(), connector.FilesystemRequest{Root: root, Path: "escape/file", Operation: connector.FilesystemOperationWrite, Content: []byte("leak")}); err == nil {
		t.Fatal("followed symlink outside root")
	}
	if err := os.WriteFile(filepath.Join(root, "large"), []byte("large"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.ExecuteFilesystem(t.Context(), connector.FilesystemRequest{Root: root, Path: "large", Operation: connector.FilesystemOperationRead, MaxReadBytes: 2}); err == nil {
		t.Fatal("accepted oversized read")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := transport.ExecuteFilesystem(ctx, connector.FilesystemRequest{Root: root, Operation: connector.FilesystemOperationProbe}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}
