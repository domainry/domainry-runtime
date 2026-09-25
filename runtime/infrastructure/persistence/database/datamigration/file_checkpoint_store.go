package datamigration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

type checkpointTemporaryFile interface {
	Name() string
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type FileCheckpointStore struct {
	Path       string
	createTemp func(string, string) (checkpointTemporaryFile, error)
	rename     func(string, string) error
}

func (s FileCheckpointStore) Load(ctx context.Context) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	raw, err := os.ReadFile(strings.TrimSpace(s.Path))
	if os.IsNotExist(err) {
		return Checkpoint{Version: 1, Tables: map[string]TableCheckpoint{}}, nil
	}
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read data migration checkpoint: %w", err)
	}
	var checkpoint Checkpoint
	if err := timevalue.UnmarshalJSON(raw, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("parse data migration checkpoint: %w", err)
	}
	if checkpoint.Version != 1 {
		return Checkpoint{}, fmt.Errorf("unsupported data migration checkpoint version %d", checkpoint.Version)
	}
	if checkpoint.Tables == nil {
		checkpoint.Tables = map[string]TableCheckpoint{}
	}
	return checkpoint, nil
}

func (s FileCheckpointStore) Save(ctx context.Context, checkpoint Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := strings.TrimSpace(s.Path)
	if path == "" {
		return fmt.Errorf("data migration checkpoint path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create data migration checkpoint directory: %w", err)
	}
	checkpoint.Version, checkpoint.UpdatedAt = 1, time.Now().UTC()
	raw, err := timevalue.MarshalJSONIndent(checkpoint)
	if err != nil {
		return err
	}
	createTemp := s.createTemp
	if createTemp == nil {
		createTemp = func(directory, pattern string) (checkpointTemporaryFile, error) {
			return os.CreateTemp(directory, pattern)
		}
	}
	temporary, err := createTemp(filepath.Dir(path), ".runtime-data-migration-*.tmp")
	if err != nil {
		return fmt.Errorf("create data migration checkpoint: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	rename := s.rename
	if rename == nil {
		rename = os.Rename
	}
	if err := rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish data migration checkpoint: %w", err)
	}
	return nil
}
