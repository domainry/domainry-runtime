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
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// Store adapts Runtime transaction and system-scope semantics to the reusable
// Audit application store contract. Audit persistence remains module-owned.
type AuditStore struct {
	binding auditsdk.Binding
}

func NewAuditStore(binding auditsdk.Binding) *AuditStore { return &AuditStore{binding: binding} }

// NewAuditStoreFromRuntimeStore opens the source-owned module against a caller-
// supplied construction context for tests and narrow host integrations.
func NewAuditStoreFromRuntimeStore(ctx context.Context, store *persistence.RuntimeStore) *AuditStore {
	binding, err := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{}).OpenModule(ctx, auditsdk.ApplicationRef{InstallationID: "domainry-runtime"}, NewHost(store, nil, nil, nil))
	if err != nil {
		panic(err)
	}
	return NewAuditStore(binding)
}

func (r *AuditStore) InsertAuditEvent(ctx context.Context, workspaceID string, event auditmodel.AuditEvent) error {
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

func (r *AuditStore) ListAuditEvents(ctx context.Context, workspaceID string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	if r == nil || r.binding == nil {
		return nil, fmt.Errorf("audit.binding_unavailable")
	}
	return r.binding.Reader().List(ctx, workspaceID, query)
}

func (r *AuditStore) ListAuditEventsForSystem(ctx context.Context, scope principalmodel.SystemScope, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	if r == nil || r.binding == nil {
		return nil, fmt.Errorf("audit.binding_unavailable")
	}
	return r.binding.Reader().ListSystem(ctx, query)
}

func (r *AuditStore) ListAuditOptions(ctx context.Context, workspaceID string, query auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
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

var _ auditrepository.AuditRepository = (*AuditStore)(nil)

type SubjectLifecycle struct {
	lifecycle sdkcontract.SubjectLifecycle
	resources func(context.Context, string, string) ([]sdkcontract.SubjectResource, error)
}

func (s *SubjectLifecycle) BindSubjectResourceResolver(resolve func(context.Context, string, string) ([]sdkcontract.SubjectResource, error)) {
	s.resources = resolve
}

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

func (s *SubjectLifecycle) ExportSubjectForRequest(ctx context.Context, _ string, workspaceID, identity string) (json.RawMessage, error) {
	return s.ExportSubject(ctx, workspaceID, identity)
}
func (s *SubjectLifecycle) EraseSubject(ctx context.Context, workspaceID, identity string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	if len(holds) > 0 {
		return nil, fmt.Errorf("audit subject erasure blocked by legal hold")
	}
	return s.lifecycle.EraseSubject(ctx, workspaceID, identity)
}

func (s *SubjectLifecycle) EraseSubjectForRequest(ctx context.Context, _ string, workspaceID, identity string, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	return s.EraseSubject(ctx, workspaceID, identity, holds)
}

var _ lifecyclecontract.SubjectExecutionHandler = (*SubjectLifecycle)(nil)

type subjectErasurePlan struct {
	RequestID      string                        `json:"request_id"`
	WorkspaceID    string                        `json:"workspace_id"`
	SubjectID      string                        `json:"subject_id"`
	PreparedCounts json.RawMessage               `json:"prepared_counts"`
	Resources      []sdkcontract.SubjectResource `json:"resources"`
}

func (s *SubjectLifecycle) PrepareSubjectErasure(ctx context.Context, requestID, workspaceID, subjectID string) (json.RawMessage, error) {
	counts, err := s.PreviewSubject(ctx, workspaceID, subjectID)
	if err != nil {
		return nil, err
	}
	resources := []sdkcontract.SubjectResource{}
	if s.resources != nil {
		resources, err = s.resources(ctx, workspaceID, subjectID)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(subjectErasurePlan{RequestID: requestID, WorkspaceID: workspaceID, SubjectID: subjectID, PreparedCounts: counts, Resources: resources})
}
func (s *SubjectLifecycle) ErasePreparedSubject(ctx context.Context, requestID, workspaceID, subjectID string, raw json.RawMessage, holds []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	var plan subjectErasurePlan
	if json.Unmarshal(raw, &plan) != nil || requestID == "" || plan.RequestID != requestID || plan.WorkspaceID != workspaceID || plan.SubjectID != subjectID {
		return nil, fmt.Errorf("audit subject erasure plan scope mismatch")
	}
	if len(holds) > 0 {
		return nil, fmt.Errorf("audit subject erasure blocked by legal hold")
	}
	source, ok := s.lifecycle.(sdkcontract.SubjectResourceLifecycle)
	if !ok {
		return nil, fmt.Errorf("audit subject resource erasure unavailable")
	}
	if _, err := source.EraseSubjectResources(ctx, workspaceID, subjectID, plan.Resources); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Plan   subjectErasurePlan `json:"plan"`
		Erased bool               `json:"erased"`
	}{plan, true})
}

var _ lifecyclecontract.PreparedSubjectErasureHandler = (*SubjectLifecycle)(nil)
