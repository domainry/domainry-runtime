package datamigration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/filelock"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

type FileCopyLease struct {
	Path     string
	Owner    string
	Now      func() time.Time
	random   io.Reader
	openFile func(string, int, os.FileMode) (*os.File, error)
	persist  func(*fileCopyLeaseHandle) error
}

type fileCopyLeaseEvidence struct {
	Token           string    `json:"token"`
	Owner           string    `json:"owner"`
	PlanFingerprint string    `json:"plan_fingerprint"`
	HeartbeatAt     time.Time `json:"heartbeat_at"`
}

type fileCopyLeaseHandle struct {
	mu       sync.Mutex
	file     *os.File
	evidence fileCopyLeaseEvidence
	now      func() time.Time
}

func (l FileCopyLease) Acquire(ctx context.Context, fingerprint string) (CopyLeaseHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Clean(strings.TrimSpace(l.Path))
	if path == "." || !filepath.IsAbs(path) || strings.TrimSpace(fingerprint) == "" {
		return nil, fmt.Errorf("absolute lease path and plan fingerprint are required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	openFile := l.openFile
	if openFile == nil {
		openFile = os.OpenFile
	}
	file, err := openFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := filelock.TryExclusive(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("copy lease is already held")
	}
	now := l.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	tokenBytes := make([]byte, 16)
	random := l.random
	if random == nil {
		random = rand.Reader
	}
	if _, err := io.ReadFull(random, tokenBytes); err != nil {
		_ = filelock.Unlock(file)
		_ = file.Close()
		return nil, err
	}
	handle := &fileCopyLeaseHandle{file: file, now: now, evidence: fileCopyLeaseEvidence{Token: hex.EncodeToString(tokenBytes), Owner: strings.TrimSpace(l.Owner), PlanFingerprint: fingerprint, HeartbeatAt: now().UTC()}}
	persist := l.persist
	if persist == nil {
		persist = func(handle *fileCopyLeaseHandle) error { return handle.persist() }
	}
	if err := persist(handle); err != nil {
		_ = handle.Release()
		return nil, err
	}
	return handle, nil
}

func (h *fileCopyLeaseHandle) Check(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.file == nil {
		return fmt.Errorf("lease handle is closed")
	}
	h.evidence.HeartbeatAt = h.now().UTC()
	return h.persist()
}

func (h *fileCopyLeaseHandle) persist() error {
	raw, _ := timevalue.MarshalJSON(h.evidence)
	return persistLeaseEvidence(h.file, raw)
}

type leaseEvidenceFile interface {
	Truncate(int64) error
	Seek(int64, int) (int64, error)
	Write([]byte) (int, error)
	Sync() error
}

func persistLeaseEvidence(file leaseEvidenceFile, raw []byte) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		return err
	}
	return file.Sync()
}

func (h *fileCopyLeaseHandle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file == nil {
		return nil
	}
	err := releaseLeaseFile(h.file, filelock.Unlock, func(file *os.File) error { return file.Close() })
	h.file = nil
	return err
}

func releaseLeaseFile(file *os.File, unlock func(*os.File) error, closeFile func(*os.File) error) error {
	err := unlock(file)
	if closeErr := closeFile(file); err == nil {
		err = closeErr
	}
	return err
}
