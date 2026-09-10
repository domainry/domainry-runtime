package runtimehost

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/domainry/domainry-foundation/mutation"
	"sync"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workspaceprovision "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
)

type runtimeExternalWorkspaceHost struct {
	mu          sync.RWMutex
	database    *bootstrap.ProjectDatabase
	manifest    manifestmodel.ManifestSchema
	participant runtimeext.WorkspaceBootstrapParticipant
	configured  bool
}

func (host *runtimeExternalWorkspaceHost) configure(manifest manifestmodel.ManifestSchema, participant runtimeext.WorkspaceBootstrapParticipant) {
	host.mu.Lock()
	defer host.mu.Unlock()
	host.manifest, host.participant, host.configured = manifest, participant, true
}

func (host *runtimeExternalWorkspaceHost) CreateExternalWorkspace(ctx context.Context, request identitysdk.ExternalWorkspaceCreate, transaction identitysdk.EmbeddedTransaction) error {
	host.mu.RLock()
	defer host.mu.RUnlock()
	if !host.configured {
		return fmt.Errorf("external Workspace bootstrap is not configured")
	}
	return workspaceprovision.CreateExternalWorkspace(ctx, host.database, request, transaction, host.manifest, host.participant)
}

func (host *runtimeExternalWorkspaceHost) InitializeExternalWorkspaceApplication(ctx context.Context, request identitysdk.ExternalWorkspaceCreate, transaction identitysdk.EmbeddedTransaction) error {
	host.mu.RLock()
	defer host.mu.RUnlock()
	if !host.configured {
		return fmt.Errorf("external Workspace bootstrap is not configured")
	}
	return workspaceprovision.InitializeExternalWorkspaceApplication(ctx, host.database, request, transaction, host.manifest, host.participant)
}

func (host *runtimeExternalWorkspaceHost) ExternalWorkspaceActive(ctx context.Context, workspaceID string) (bool, error) {
	return workspaceprovision.NewWorkspaceAdministrationStore(host.database).WorkspaceActive(ctx, workspaceID)
}

var _ identitysdk.ExternalWorkspaceHost = (*runtimeExternalWorkspaceHost)(nil)

// The callback is database-only and may be retried in its entirety after a
// rolled-back uniqueness or transient transaction conflict. Unknown commit
// outcomes are returned, so the caller can retry using its persistent owner key.
func (host *runtimeExternalWorkspaceHost) RunExternalWorkspaceTransaction(ctx context.Context, apply func(context.Context, identitysdk.EmbeddedTransaction) error) error {
	if apply == nil {
		return fmt.Errorf("external Workspace transaction callback is required")
	}
	for attempt := 0; ; attempt++ {
		err := host.externalTransactionAttempt(ctx, apply)
		classified := mutation.TransactionError(err, "external_identity", "")
		conflict := mutation.ConstraintError(err, "external_identity", "", mutation.MutationConflictKind("unique"))
		if err == nil || attempt >= 7 || (!mutation.IsTransactionTransient(classified, "") && !mutation.IsMutationConflict(conflict, "")) {
			return err
		}
		timer := time.NewTimer(time.Duration(10*(1<<attempt)) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (host *runtimeExternalWorkspaceHost) externalTransactionAttempt(ctx context.Context, apply func(context.Context, identitysdk.EmbeddedTransaction) error) error {
	tx, err := host.database.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := apply(ctx, identitysdk.EmbeddedTransaction{Executor: tx}); err != nil {
		return err
	}
	return tx.Commit()
}
