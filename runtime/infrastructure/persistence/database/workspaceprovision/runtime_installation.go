package workspaceprovision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const installationKey = "primary"

// Installation is the single durable tenant boundary that must exist before
// Runtime opens tenant-bound modules, HTTP APIs, or workers.
type Installation struct {
	TenantRegistryID string
	WorkspaceID      string
	InitializedAt    string
}

func LoadInstallation(ctx context.Context, store *database.RuntimeStore) (Installation, bool, error) {
	if store == nil {
		return Installation{}, false, fmt.Errorf("Runtime installation store is required")
	}
	statement, arguments, err := ormbuilder.NewSelectBuilder(store.RuntimeRenderer(), "_tenant_installation").
		Columns("tenant_registry_id", "workspace_id", "initialized_at").
		Where(ormbuilder.Equal("installation_key", installationKey)).Build()
	if err != nil {
		return Installation{}, false, err
	}
	var result Installation
	err = store.DB().QueryRowContext(ctx, statement, arguments...).Scan(&result.TenantRegistryID, &result.WorkspaceID, &result.InitializedAt)
	if errors.Is(err, sql.ErrNoRows) {
		legacyStatement, legacyArguments, buildErr := ormbuilder.NewSelectBuilder(store.RuntimeRenderer(), "_workspaces").
			Columns("id").Build()
		if buildErr != nil {
			return Installation{}, false, buildErr
		}
		var legacyWorkspaceID string
		legacyErr := store.DB().QueryRowContext(ctx, legacyStatement, legacyArguments...).Scan(&legacyWorkspaceID)
		if legacyErr == nil {
			return Installation{}, false, fmt.Errorf("Runtime tenant initialization marker is missing for existing workspace %q; explicit migration is required", strings.TrimSpace(legacyWorkspaceID))
		}
		if !errors.Is(legacyErr, sql.ErrNoRows) {
			return Installation{}, false, fmt.Errorf("inspect Runtime tenant initialization state: %w", legacyErr)
		}
		return Installation{}, false, nil
	}
	if err != nil {
		return Installation{}, false, fmt.Errorf("load Runtime tenant initialization: %w", err)
	}
	if strings.TrimSpace(result.TenantRegistryID) == "" || strings.TrimSpace(result.WorkspaceID) == "" || strings.EqualFold(strings.TrimSpace(result.TenantRegistryID), "default") || strings.EqualFold(strings.TrimSpace(result.WorkspaceID), "default") {
		return Installation{}, false, fmt.Errorf("Runtime tenant initialization is corrupt")
	}
	return result, true, nil
}
