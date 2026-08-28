package filesystem

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
)

func TestLifecycleSubjectArtifactFilesystemFailureEdges(t *testing.T) {
	originalMkdir, originalCreate, originalRename := localMkdirAll, localCreateTemp, localRename
	originalReadFile, originalReadDir, originalInfo := localReadFile, localReadDir, localDirEntryInfo
	originalRemove, originalOpen, originalAbs := localRemove, localOpenFile, localAbsPath
	t.Cleanup(func() {
		localMkdirAll, localCreateTemp, localRename = originalMkdir, originalCreate, originalRename
		localReadFile, localReadDir, localDirEntryInfo = originalReadFile, originalReadDir, originalInfo
		localRemove, localOpenFile, localAbsPath = originalRemove, originalOpen, originalAbs
	})
	reset := func() {
		localMkdirAll, localCreateTemp, localRename = originalMkdir, originalCreate, originalRename
		localReadFile, localReadDir, localDirEntryInfo = originalReadFile, originalReadDir, originalInfo
		localRemove, localOpenFile, localAbsPath = originalRemove, originalOpen, originalAbs
	}
	store := NewSubjectStore(t.TempDir())
	if _, err := store.ReadSubjectExport(t.Context(), "workspace-a", "../invalid", time.Now()); err == nil {
		t.Fatal("invalid export reference read accepted")
	}
	put := func() error {
		_, err := store.PutSubjectExport(t.Context(), "workspace-a", "request-1", json.RawMessage(`{"ok":true}`), time.Now().Add(time.Hour))
		return err
	}
	localMkdirAll = func(string, os.FileMode) error { return errors.New("mkdir") }
	if err := put(); err == nil {
		t.Fatal("mkdir failure ignored")
	}
	reset()
	if _, err := store.PutSubjectExport(t.Context(), "workspace-a", "request-1", json.RawMessage("{"), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("marshal failure ignored")
	}
	localCreateTemp = func(string, string) (localTemporaryFile, error) { return nil, errors.New("create") }
	if err := put(); err == nil {
		t.Fatal("create failure ignored")
	}
	for _, stage := range []string{"chmod", "write", "sync", "close"} {
		reset()
		localCreateTemp = func(string, string) (localTemporaryFile, error) {
			return &failingTemporaryFile{name: filepath.Join(t.TempDir(), "stage"), fail: stage}, nil
		}
		if err := put(); err == nil {
			t.Fatalf("stage=%s failure ignored", stage)
		}
	}
	reset()
	localCreateTemp = func(string, string) (localTemporaryFile, error) {
		return &failingTemporaryFile{name: filepath.Join(t.TempDir(), "stage")}, nil
	}
	localRename = func(string, string) error { return errors.New("rename") }
	if err := put(); err == nil {
		t.Fatal("rename failure ignored")
	}

	reset()
	localReadDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("read-dir") }
	if _, err := store.DeleteExpiredSubjectExports(t.Context(), time.Now()); err == nil {
		t.Fatal("export read-dir failure ignored")
	}
	reset()
	now := time.Now().UTC()
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		t.Fatal(err)
	}
	expiredPath := filepath.Join(store.directory, "expired.json")
	raw, _ := json.Marshal(lifecycleSubjectArtifact{WorkspaceID: "workspace-a", ExpiresAt: now.Add(-time.Hour)})
	if err := os.WriteFile(expiredPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	localRemove = func(path string) error {
		if path == expiredPath {
			return errors.New("remove")
		}
		return originalRemove(path)
	}
	if _, err := store.DeleteExpiredSubjectExports(t.Context(), now); err == nil {
		t.Fatal("expired export remove failure ignored")
	}
	reset()
	localReadFile = func(path string) ([]byte, error) {
		if path == expiredPath {
			return nil, errors.New("read")
		}
		return originalReadFile(path)
	}
	if _, err := store.DeleteExpiredSubjectExports(t.Context(), now); err != nil {
		t.Fatalf("unreadable export should be skipped: %v", err)
	}

	reset()
	localAbsPath = func(string) (string, error) { return "", errors.New("abs") }
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging abs failure ignored")
	}
	reset()
	missingRootStore := NewSubjectStore(filepath.Join(t.TempDir(), "missing"))
	if deleted, err := missingRootStore.DeleteExpiredUploadStaging(t.Context(), now); err != nil || deleted != 0 {
		t.Fatalf("missing staging root deleted=%d err=%v", deleted, err)
	}
	localReadDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("read-dir") }
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging read-dir failure ignored")
	}
	reset()
	workspace := filepath.Join(store.uploadRoot, "workspace-test")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.uploadRoot, "workspace-file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, ".upload-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	stagePath := filepath.Join(workspace, ".upload-old")
	if err := os.WriteFile(stagePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(stagePath, old, old); err != nil {
		t.Fatal(err)
	}
	localReadDir = func(path string) ([]os.DirEntry, error) {
		if path == workspace {
			return nil, errors.New("workspace read")
		}
		return originalReadDir(path)
	}
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("workspace read failure ignored")
	}
	reset()
	localDirEntryInfo = func(os.DirEntry) (os.FileInfo, error) { return nil, errors.New("info") }
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging info failure ignored")
	}
	reset()
	localRemove = func(path string) error {
		if path == stagePath {
			return errors.New("remove")
		}
		return originalRemove(path)
	}
	if _, err := store.DeleteExpiredUploadStaging(t.Context(), now); err == nil {
		t.Fatal("staging remove failure ignored")
	}
}

func TestLifecycleSubjectFileFilesystemFailureEdges(t *testing.T) {
	originalOpen, originalRead, originalRemove, originalAbs := localOpenFile, localReadFile, localRemove, localAbsPath
	t.Cleanup(func() {
		localOpenFile, localReadFile, localRemove, localAbsPath = originalOpen, originalRead, originalRemove, originalAbs
	})
	root := t.TempDir()
	store := NewSubjectStore(root)
	reference := lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "/uploads/file.txt"}
	if _, err := store.ExportSubjectFile(t.Context(), lifecyclecontract.SubjectFileReference{}); err == nil {
		t.Fatal("invalid subject export accepted")
	}
	if _, _, err := NewSubjectStore("").subjectFilePath(reference); err == nil {
		t.Fatal("empty upload root accepted")
	}
	localOpenFile = func(string) (localReadableFile, error) { return &failingReadableFile{statErr: errors.New("stat")}, nil }
	if _, err := store.ExportSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("stat failure ignored")
	}
	localOpenFile = func(string) (localReadableFile, error) { return &failingReadableFile{info: fakeFileInfo{size: 1}}, nil }
	localReadFile = func(string) ([]byte, error) { return nil, errors.New("read") }
	if _, err := store.ExportSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("read failure ignored")
	}
	localOpenFile, localReadFile = originalOpen, originalRead
	path, _, err := store.subjectFilePath(reference)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	localRemove = func(string) error { return errors.New("remove") }
	if _, err := store.DeleteSubjectFile(t.Context(), reference); err == nil {
		t.Fatal("subject remove failure ignored")
	}
	if _, err := store.DeleteSubjectFile(t.Context(), lifecyclecontract.SubjectFileReference{}); err == nil {
		t.Fatal("invalid subject deletion accepted")
	}
	localAbsPath = func(string) (string, error) { return "", errors.New("abs") }
	if _, _, err := store.subjectFilePath(reference); err == nil {
		t.Fatal("subject abs failure ignored")
	}
	localAbsPath = originalAbs
	if _, _, err := store.subjectFilePath(lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: "%"}); err == nil {
		t.Fatal("invalid subject URL accepted")
	}
	if _, _, err := store.subjectFilePath(lifecyclecontract.SubjectFileReference{WorkspaceID: "workspace-a", Reference: ".."}); err == nil {
		t.Fatal("parent filename accepted")
	}
}

type failingTemporaryFile struct{ name, fail string }

func (file *failingTemporaryFile) Name() string { return file.name }
func (file *failingTemporaryFile) Chmod(os.FileMode) error {
	if file.fail == "chmod" {
		return errors.New("chmod")
	}
	return nil
}
func (file *failingTemporaryFile) Write(value []byte) (int, error) {
	if file.fail == "write" {
		return 0, errors.New("write")
	}
	return len(value), nil
}
func (file *failingTemporaryFile) Sync() error {
	if file.fail == "sync" {
		return errors.New("sync")
	}
	return nil
}
func (file *failingTemporaryFile) Close() error {
	if file.fail == "close" {
		return errors.New("close")
	}
	return nil
}

type failingReadableFile struct {
	info    os.FileInfo
	statErr error
}

func (file *failingReadableFile) Stat() (os.FileInfo, error) { return file.info, file.statErr }
func (*failingReadableFile) Close() error                    { return nil }

type fakeFileInfo struct{ size int64 }

func (fakeFileInfo) Name() string       { return "file" }
func (info fakeFileInfo) Size() int64   { return info.size }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }

var _ fs.FileInfo = fakeFileInfo{}
