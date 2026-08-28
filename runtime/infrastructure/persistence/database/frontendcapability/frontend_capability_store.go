// Frontend capability persistence.
package frontendcapability

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

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
	err = r.db.QueryRowContext(ctx, "SELECT "+stringsJoinIdentifiers(r.store, "workspace_id", "revision", "manifest_json", "updated_at")+" FROM "+r.store.TableIdentifier("frontend_capability_manifests")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1), workspaceID).Scan(&record.WorkspaceID, &record.Revision, &payload, &record.UpdatedAt)
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("frontend_capability_manifests")+" SET "+r.store.Identifier("revision")+" = "+r.store.Identifier("revision")+" + 1, "+r.store.Identifier("manifest_json")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(3), string(payload), now, workspaceID)
	if err != nil {
		return deploymentmodel.DeploymentFrontendCapabilityState{}, fmt.Errorf("update frontend capability manifest: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		_, err = r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("frontend_capability_manifests")+" ("+stringsJoinIdentifiers(r.store, "workspace_id", "revision", "manifest_json", "updated_at")+") VALUES ("+stringsJoinPlaceholders(r.store, 4)+")", workspaceID, int64(1), string(payload), now)
		if err != nil {
			// A concurrent first writer won the insert. Apply this write as the
			// next revision rather than losing it.
			if _, updateErr := r.db.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("frontend_capability_manifests")+" SET "+r.store.Identifier("revision")+" = "+r.store.Identifier("revision")+" + 1, "+r.store.Identifier("manifest_json")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(2)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(3), string(payload), now, workspaceID); updateErr != nil {
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

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func stringsJoinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for index := 0; index < count; index++ {
		values = append(values, store.Placeholder(index+1))
	}
	return strings.Join(values, ", ")
}
