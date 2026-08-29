package auditmodule

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	sdkcontract "github.com/domainry/domainry-audit-sdk/contract"
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// Repository is the temporary Runtime domain facade over the external Audit
// Binding. Persistence and query behavior are owned by domainry-audit.
type Repository struct {
	binding auditsdk.Binding
}

func NewRepository(binding auditsdk.Binding) *Repository { return &Repository{binding: binding} }

// NewRepositoryFromStore is the embedded-host convenience constructor used by
// tests and narrow integrations that do not own the full Runtime assembly.
func NewRepositoryFromStore(store *persistence.RuntimeStore) *Repository {
	binding := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{}).MustOpenBorrowedDatabase(
		auditsdk.ApplicationRef{InstallationID: "domainry-runtime"},
		auditsdk.DatabaseHandle{Pool: store.DB(), Driver: store.Driver(), Schema: store.DatabaseSchema(), SchemaManager: store, InstallationWorkspaceID: principalmodel.InstallationWorkspaceID},
	)
	return NewRepository(binding)
}

func (r *Repository) InsertAuditEvent(ctx context.Context, workspaceID string, event auditmodel.AuditEvent) error {
	if r == nil || r.binding == nil {
		return fmt.Errorf("audit.binding_unavailable")
	}
	if event.WorkspaceID != workspaceID {
		return fmt.Errorf("audit event workspace %q does not match repository workspace %q", event.WorkspaceID, workspaceID)
	}
	if tx := persistence.ActionExecutionTransaction(ctx); tx != nil {
		return r.binding.PreparedAppender().AppendPreparedWithin(ctx, transactionAdapter{tx}, event)
	}
	return r.binding.PreparedAppender().AppendPrepared(ctx, event)
}

func (r *Repository) ListAuditEvents(ctx context.Context, workspaceID string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	if r == nil || r.binding == nil {
		return nil, fmt.Errorf("audit.binding_unavailable")
	}
	return r.binding.Reader().List(ctx, workspaceID, query)
}

func (r *Repository) ListAuditEventsForSystem(ctx context.Context, scope principalmodel.SystemScope, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if r == nil || r.binding == nil {
		return nil, fmt.Errorf("audit.binding_unavailable")
	}
	return r.binding.Reader().ListSystem(ctx, query)
}

func (r *Repository) ListAuditOptions(ctx context.Context, workspaceID string, query auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	if r == nil || r.binding == nil {
		return nil, fmt.Errorf("audit.binding_unavailable")
	}
	return r.binding.Reader().Options(ctx, workspaceID, query)
}

type transactionAdapter struct {
	executor persistence.ActionExecutionExecutor
}

func NewTransaction(executor persistence.ActionExecutionExecutor) sdkcontract.Transaction {
	return transactionAdapter{executor: executor}
}

func (a transactionAdapter) ExecContext(ctx context.Context, statement string, args ...any) (sdkcontract.Result, error) {
	result, err := a.executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	return sqlResult{Result: result}, nil
}

func (a transactionAdapter) QueryRowContext(ctx context.Context, statement string, args ...any) sdkcontract.Row {
	return a.executor.QueryRowContext(ctx, statement, args...)
}

type sqlResult struct{ sql.Result }

var _ auditrepository.AuditRepository = (*Repository)(nil)

type SubjectLifecycle struct{ lifecycle sdkcontract.SubjectLifecycle }

func NewSubjectLifecycle(binding auditsdk.Binding) *SubjectLifecycle {
	return &SubjectLifecycle{lifecycle: binding.SubjectLifecycle()}
}
func (*SubjectLifecycle) Owner(context.Context) string { return "audit" }
func (s *SubjectLifecycle) PreviewSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	return s.lifecycle.PreviewSubject(ctx, workspaceID, identity)
}
func (s *SubjectLifecycle) ExportSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	return s.lifecycle.ExportSubject(ctx, workspaceID, identity)
}
func (s *SubjectLifecycle) EraseSubject(ctx context.Context, workspaceID, identity string, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return s.lifecycle.EraseSubject(ctx, workspaceID, identity)
}
