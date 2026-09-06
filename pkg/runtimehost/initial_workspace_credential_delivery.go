package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type initialWorkspaceCredentialFileDelivery struct{ path string }

func NewInitialWorkspaceCredentialFileDelivery(path string) (InitialWorkspaceCredentialDelivery, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("initial Workspace credential file path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve initial Workspace credential file: %w", err)
	}
	directory := filepath.Dir(absolute)
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect initial Workspace credential directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("initial Workspace credential directory must be a private, non-symlink directory")
	}
	if _, err := os.Lstat(absolute); err == nil {
		return nil, fmt.Errorf("initial Workspace credential file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect initial Workspace credential file: %w", err)
	}
	return &initialWorkspaceCredentialFileDelivery{path: absolute}, nil
}

func (delivery *initialWorkspaceCredentialFileDelivery) DeliverInitialWorkspaceCredential(ctx context.Context, credential InitialWorkspaceCredential) (InitialWorkspaceCredentialDeliveryAcknowledgment, error) {
	if delivery == nil || strings.TrimSpace(delivery.path) == "" || ctx == nil || strings.TrimSpace(credential.InitialPassword) == "" {
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, fmt.Errorf("initial Workspace credential delivery is invalid")
	}
	select {
	case <-ctx.Done():
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, ctx.Err()
	default:
	}
	payload, err := json.Marshal(map[string]any{
		"workspace_code": credential.CanonicalCode, "login_id": credential.LoginID,
		"initial_password": credential.InitialPassword, "must_change_password": credential.MustChangePassword,
	})
	if err != nil {
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, err
	}
	directory := filepath.Dir(delivery.path)
	temporary, err := os.CreateTemp(directory, ".initial-workspace-credential-*")
	if err != nil {
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, fmt.Errorf("create initial Workspace credential temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, err
	}
	if _, err := temporary.Write(append(payload, '\n')); err != nil {
		_ = temporary.Close()
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, err
	}
	if err := temporary.Close(); err != nil {
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, err
	}
	// Hard-link publication is atomic and create-only: an existing destination
	// is never replaced, even under concurrent startup attempts.
	if err := os.Link(temporaryPath, delivery.path); err != nil {
		return InitialWorkspaceCredentialDeliveryAcknowledgment{}, fmt.Errorf("publish initial Workspace credential file: %w", err)
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return InitialWorkspaceCredentialDeliveryAcknowledgment{Accepted: true}, nil
}

type InitialWorkspaceCredentialDeliveryError struct {
	CanonicalCode string
	Cause         error
}

func (err *InitialWorkspaceCredentialDeliveryError) Error() string {
	return "initial Workspace is committed but its credential is unavailable; administrator password reset is required"
}

func (err *InitialWorkspaceCredentialDeliveryError) Unwrap() error { return err.Cause }
