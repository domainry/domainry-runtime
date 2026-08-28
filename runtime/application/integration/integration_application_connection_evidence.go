package integration

import (
	"context"
	"errors"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) PersistConnectionTestStatus(ctx context.Context, connection integrationmodel.IntegrationConnection, status string, principal principalmodel.Principal) (integrationmodel.IntegrationConnection, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return connection, err
	}
	if err := ctx.Err(); err != nil {
		return connection, err
	}
	if connection.Status == "active" && status == "verified" {
		return connection, nil
	}
	before := connectionAuditShape(connection)
	connection.Status = status
	saved, err := s.upsertConnectionAndSyncBackground(ctx, connection)
	if err != nil {
		return connection, err
	}
	s.audit(ctx, "integration_connection_test_status_updated", "integration_connection", saved.Key, principal, "Updated integration connection test status "+saved.Key, before, connectionAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "connector_key": saved.ConnectorKey, "connection_key": saved.Key, "status": saved.Status,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) RecordConnectionTestStatus(ctx context.Context, connection integrationmodel.IntegrationConnection, status string, principal principalmodel.Principal) integrationmodel.IntegrationConnection {
	saved, err := s.PersistConnectionTestStatus(ctx, connection, status, principal)
	if err != nil {
		return connection
	}
	return saved
}

func (s *IntegrationApplicationService) RecordCredentialTestEvidence(ctx context.Context, connection integrationmodel.IntegrationConnection, success bool, testErr error) error {
	if err := integrationAuthorizeWorkspaceCommand(connection.WorkspaceID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	status, errorCode := "succeeded", ""
	if !success {
		status, errorCode = "failed", integrationErrorCode(testErr)
		if errorCode == "" {
			errorCode = "backend.integration.credential.validation_failed"
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	seen := map[string]bool{}
	for _, reference := range connection.SecretRefs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(strings.TrimSpace(reference), "secret:") {
			continue
		}
		key := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reference), "secret:"))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		secret, ok, err := s.findSecret(ctx, key, connection.WorkspaceID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		secret.LastTestedAt, secret.LastTestStatus, secret.LastTestError = now, status, errorCode
		if _, err := s.configRepo.UpsertSecret(ctx, secret.WorkspaceID, secret); err != nil {
			return err
		}
	}
	return nil
}

func integrationErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return strings.TrimSpace(appErr.Code)
	}
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return strings.TrimSpace(coded.ErrorCode())
	}
	return ""
}
