package workspaceprovision

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type Installation struct {
	WorkspaceID   string
	InitializedAt string
}

func LoadInstallation(ctx context.Context, store *database.RuntimeStore) (Installation, bool, error) {
	if store == nil {
		return Installation{}, false, fmt.Errorf("Runtime installation store is required")
	}
	installationIdentity, err := store.InstallationIdentity(ctx)
	if err != nil {
		return Installation{}, false, err
	}
	statement, arguments, err := query.NewSelectBuilder(store.RuntimeRenderer(), "_workspaces").
		Columns("id", "created_at").
		Where(query.Equal("initial_installation_identity", installationIdentity)).Build()
	if err != nil {
		return Installation{}, false, err
	}
	var result Installation
	err = store.DB().QueryRowContext(ctx, statement, arguments...).Scan(&result.WorkspaceID, &result.InitializedAt)
	if errors.Is(err, sql.ErrNoRows) {
		workspaceStatement, workspaceArguments, buildErr := query.NewSelectBuilder(store.RuntimeRenderer(), "_workspaces").
			Columns("id").Build()
		if buildErr != nil {
			return Installation{}, false, buildErr
		}
		var workspaceID string
		workspaceErr := store.DB().QueryRowContext(ctx, workspaceStatement, workspaceArguments...).Scan(&workspaceID)
		if workspaceErr == nil {
			return Installation{}, false, fmt.Errorf("Runtime initial Workspace authority is missing for existing Workspace %q; explicit migration is required", strings.TrimSpace(workspaceID))
		}
		if !errors.Is(workspaceErr, sql.ErrNoRows) {
			return Installation{}, false, fmt.Errorf("inspect Runtime initial Workspace state: %w", workspaceErr)
		}
		return Installation{}, false, nil
	}
	if err != nil {
		return Installation{}, false, fmt.Errorf("load Runtime initial Workspace: %w", err)
	}
	if strings.TrimSpace(result.WorkspaceID) == "" || strings.EqualFold(strings.TrimSpace(result.WorkspaceID), "default") {
		return Installation{}, false, fmt.Errorf("Runtime initial Workspace authority is corrupt")
	}
	return result, true, nil
}
