package filesystem

import (
	"os"
	"path/filepath"
)

type localTemporaryFile interface {
	Name() string
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type localReadableFile interface {
	Stat() (os.FileInfo, error)
	Close() error
}

var (
	localMkdirAll     = os.MkdirAll
	localCreateTemp   = func(directory, pattern string) (localTemporaryFile, error) { return os.CreateTemp(directory, pattern) }
	localRename       = os.Rename
	localReadFile     = os.ReadFile
	localReadDir      = os.ReadDir
	localDirEntryInfo = func(entry os.DirEntry) (os.FileInfo, error) { return entry.Info() }
	localRemove       = os.Remove
	localOpenFile     = func(path string) (localReadableFile, error) { return os.Open(path) }
	localAbsPath      = filepath.Abs
)
