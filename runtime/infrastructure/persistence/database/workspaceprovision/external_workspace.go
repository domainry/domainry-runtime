package workspaceprovision

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// InitializeExternalInstallation records technical installation authority without
// creating an account or promoting the first external user to administrator.
func InitializeExternalInstallation(ctx context.Context, store *database.RuntimeStore) (Installation, error) {
	if err := store.EnsureRuntimeSchema(ctx); err != nil {
		return Installation{}, err
	}
	if existing, found, err := LoadInstallation(ctx, store); err != nil || found {
		return existing, err
	}
	installationID, err := store.InstallationIdentity(ctx)
	if err != nil {
		return Installation{}, err
	}
	id, err := randomID("installation")
	if err != nil {
		return Installation{}, err
	}
	tx, err := store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Installation{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statement, arguments, err := query.NewInsertBuilder(store.RuntimeRenderer(), "_workspaces").
		Columns("id", "canonical_code", "name", "status", "initial_installation_identity", "revision", "created_at", "updated_at").
		Values(id, id, "Installation", "active", installationID, 1, now, now).Build()
	if err != nil {
		return Installation{}, err
	}
	if _, err := tx.ExecContext(ctx, statement, arguments...); err != nil {
		_ = tx.Rollback()
		if existing, found, readErr := LoadInstallation(ctx, store); readErr == nil && found {
			return existing, nil
		}
		return Installation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Installation{}, err
	}
	return Installation{WorkspaceID: id, InitializedAt: now}, nil
}

// CreateExternalWorkspace is the Runtime-owned part of a source module's
// transaction. Workspace ownership, identity projections and application roles
// are committed by that module in the same transaction.
func CreateExternalWorkspace(ctx context.Context, store *database.RuntimeStore, request identitysdk.ExternalWorkspaceCreate, transaction identitysdk.EmbeddedTransaction, manifest manifestmodel.ManifestSchema, participant runtimeext.WorkspaceBootstrapParticipant) error {
	tx, ok := transaction.Executor.(*sql.Tx)
	if !ok || tx == nil || strings.TrimSpace(request.WorkspaceID) == "" || strings.EqualFold(request.WorkspaceID, "default") || strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.Name) == "" {
		return fmt.Errorf("external Workspace requires a transaction and verified identity")
	}
	provision := &WorkspaceProvisionStore{runtime: store}
	result := workspaceprovisionmodel.Result{WorkspaceID: request.WorkspaceID, CanonicalCode: request.WorkspaceID, InitialAdminUserID: request.UserID}
	return provision.insertRuntimeWorkspace(ctx, tx, workspaceprovisionmodel.Request{WorkspaceName: request.Name}, result, "")
}

// InitializeExternalWorkspaceApplication runs once per application assignment,
// including when the owner already has a personal Workspace from another app.
func InitializeExternalWorkspaceApplication(ctx context.Context, store *database.RuntimeStore, request identitysdk.ExternalWorkspaceCreate, transaction identitysdk.EmbeddedTransaction, manifest manifestmodel.ManifestSchema, participant runtimeext.WorkspaceBootstrapParticipant) error {
	tx, ok := transaction.Executor.(*sql.Tx)
	if !ok || tx == nil || strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.UserID) == "" {
		return fmt.Errorf("external application bootstrap requires transaction and owner")
	}
	provision := &WorkspaceProvisionStore{runtime: store, manifest: manifest, participant: participant}
	if err := provision.validateApplicationBootstrapRequest(request.ApplicationBootstrap); err != nil {
		return err
	}
	return provision.insertApplicationBootstrap(ctx, tx, request.ApplicationBootstrap, workspaceprovisionmodel.Result{WorkspaceID: request.WorkspaceID, CanonicalCode: request.WorkspaceID, InitialAdminUserID: request.UserID})
}
