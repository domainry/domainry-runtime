// Frontend capability persistence.
package frontendcapability

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type FrontendCapabilityStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewFrontendCapabilityStore(store *database.RuntimeStore) FrontendCapabilityStore {
	return FrontendCapabilityStore{store: store, db: store.DB()}
}

func (r FrontendCapabilityStore) Get(ctx context.Context, workspaceID string) (deploymentmodel.DeploymentFrontendCapabilityState, bool, error) {
	if err := ctx.Err(); err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, err
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, err
	}
	workspaceID = workspace.String()
	var record deploymentmodel.DeploymentFrontendCapabilityState
	var payload string
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "frontend_capability_manifests", workspaceID).Columns("workspace_id", "revision", "manifest_json", "updated_at").Build()
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, fmt.Errorf("build frontend capability manifest lookup: %w", err)
	}
	err = r.db.QueryRowContext(ctx, query, args...).Scan(&record.WorkspaceID, &record.Revision, &payload, &record.UpdatedAt)
	if err == sql.ErrNoRows {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, nil
	}
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, false, fmt.Errorf("get frontend capability manifest: %w", err)
	}
	record.ManifestJSON = []byte(payload)
	return record, true, nil
}

func (r FrontendCapabilityStore) Put(ctx context.Context, workspaceID string, payload []byte) (deploymentmodel.DeploymentFrontendCapabilityState, error) {
	if err := ctx.Err(); err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, err
	}
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, err
	}
	workspaceID = workspace.String()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	updateStatement, updateArgs, err := frontendCapabilityUpdate(r.store, workspaceID, payload, now)
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, err
	}
	result, err := r.db.ExecContext(ctx, updateStatement, updateArgs...)
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, fmt.Errorf("update frontend capability manifest: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		insertStatement, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "frontend_capability_manifests", workspaceID).Columns("revision", "manifest_json", "updated_at").Values(int64(1), string(payload), now).Build()
		if buildErr != nil {
			return deploymentmodel.DeploymentFrontendCapabilityState{}, fmt.Errorf("build frontend capability manifest insert: %w", buildErr)
		}
		_, err = r.db.ExecContext(ctx, insertStatement, insertArgs...)
		if err != nil {
			// A concurrent first writer won the insert. Apply this write as the
			// next revision rather than losing it.
			if _, updateErr := r.db.ExecContext(ctx, updateStatement, updateArgs...); updateErr != nil {
				return deploymentmodel.DeploymentFrontendCapabilityState{}, fmt.Errorf("persist frontend capability manifest: %w", updateErr)
			}
		}
	}
	record, ok, err := r.Get(ctx, workspaceID)
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, err
	}
	if !ok {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, sql.ErrNoRows
	}
	return record, nil
}

func frontendCapabilityUpdate(store *database.RuntimeStore, workspaceID string, payload []byte, now string) (string, []any, error) {
	return ormbuilder.NewWorkspaceUpdateBuilder(store.SQLRenderer, "frontend_capability_manifests", workspaceID).
		SetExpression("revision", ormbuilder.Add(ormbuilder.Column("revision"), ormbuilder.Value(1))).Set("manifest_json", string(payload)).Set("updated_at", now).Build()
}
