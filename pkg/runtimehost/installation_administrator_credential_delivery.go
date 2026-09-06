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

type installationAdministratorCredentialFileDelivery struct{ path string }

func NewInstallationAdministratorCredentialFileDelivery(path string) (InstallationAdministratorCredentialDelivery, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("installation administrator credential file path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve installation administrator credential file: %w", err)
	}
	directory := filepath.Dir(absolute)
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect installation administrator credential directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("installation administrator credential directory must be a private, non-symlink directory")
	}
	if _, err := os.Lstat(absolute); err == nil {
		return nil, fmt.Errorf("installation administrator credential file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect installation administrator credential file: %w", err)
	}
	return &installationAdministratorCredentialFileDelivery{path: absolute}, nil
}

func (delivery *installationAdministratorCredentialFileDelivery) DeliverInstallationAdministratorCredential(ctx context.Context, credential InstallationAdministratorCredential) (InstallationAdministratorCredentialDeliveryAcknowledgment, error) {
	if delivery == nil || strings.TrimSpace(delivery.path) == "" || ctx == nil || strings.TrimSpace(credential.InitialPassword) == "" {
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, fmt.Errorf("installation administrator credential delivery is invalid")
	}
	select {
	case <-ctx.Done():
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, ctx.Err()
	default:
	}
	payload, err := json.Marshal(map[string]any{
		"workspace_code": credential.CanonicalWorkspaceCode, "login_id": credential.LoginID,
		"initial_password": credential.InitialPassword, "must_change_password": credential.MustChangePassword,
	})
	if err != nil {
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, err
	}
	directory := filepath.Dir(delivery.path)
	temporary, err := os.CreateTemp(directory, ".installation-administrator-credential-*")
	if err != nil {
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, fmt.Errorf("create installation administrator credential temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, err
	}
	if _, err := temporary.Write(append(payload, '\n')); err != nil {
		_ = temporary.Close()
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, err
	}
	if err := temporary.Close(); err != nil {
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, err
	}
	if err := os.Link(temporaryPath, delivery.path); err != nil {
		return InstallationAdministratorCredentialDeliveryAcknowledgment{}, fmt.Errorf("publish installation administrator credential file: %w", err)
	}
	if directoryHandle, err := os.Open(directory); err == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return InstallationAdministratorCredentialDeliveryAcknowledgment{Accepted: true}, nil
}

type InstallationAdministratorCredentialDeliveryError struct{ Cause error }

func (*InstallationAdministratorCredentialDeliveryError) Error() string {
	return "installation administrator is committed but its credential is unavailable; password reset is required"
}

func (err *InstallationAdministratorCredentialDeliveryError) Unwrap() error { return err.Cause }
