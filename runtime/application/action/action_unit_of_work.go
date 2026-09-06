package action

import (
	"context"
	"fmt"
	"strings"
	"sync"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

// ActionUnitOfWorkManager creates the Runtime-owned commit boundary shared by
// System Operations and Business Handlers. Executors may only describe
// canonical mutations; they cannot commit or roll back storage themselves.
type ActionUnitOfWorkManager struct {
	executions *actionruntime.ActionExecutionRuntime
}

type actionUnitOfWork struct {
	manager                         *ActionUnitOfWorkManager
	claim                           actionmodel.ActionExecutionClaimResult
	phases                          *actionExecutionPhaseMachine
	mu                              sync.Mutex
	transaction                     actioncontract.ActionExecutionTransaction
	activeSynchronousConnectorCalls int
}

type actionSynchronousConnectorCallLease struct {
	unitOfWork *actionUnitOfWork
	once       sync.Once
}

func (l *actionSynchronousConnectorCallLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.unitOfWork == nil {
			return
		}
		l.unitOfWork.mu.Lock()
		defer l.unitOfWork.mu.Unlock()
		if l.unitOfWork.activeSynchronousConnectorCalls > 0 {
			l.unitOfWork.activeSynchronousConnectorCalls--
		}
	})
}

func NewActionUnitOfWorkManager(executions *actionruntime.ActionExecutionRuntime) *ActionUnitOfWorkManager {
	return &ActionUnitOfWorkManager{executions: executions}
}

func (m *ActionUnitOfWorkManager) ValidationErrors() []error {
	if m == nil || !m.executions.Available() {
		return []error{fmt.Errorf("action unit of work execution store is required")}
	}
	if !m.executions.TransactionAvailable() {
		return []error{fmt.Errorf("action unit of work transaction store is required")}
	}
	return nil
}

func (m *ActionUnitOfWorkManager) begin(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema) (actionmodel.ActionInvocationResult, *actionUnitOfWork, bool, error) {
	if m == nil {
		m = NewActionUnitOfWorkManager(nil)
	}
	fingerprintPayload := any(invocation.Input)
	if targetOrganizationID := strings.TrimSpace(invocation.TargetOrganizationID); targetOrganizationID != "" {
		fingerprintPayload = map[string]any{"input": invocation.Input, "target_organization_id": targetOrganizationID}
	}
	fingerprint := idempotency.FingerprintInput{UseCase: "action.invoke", ResourceType: "action", TargetID: action.Key, Payload: fingerprintPayload}
	var cached actionmodel.ActionInvocationResult
	var claim actionmodel.ActionExecutionClaimResult
	var replay bool
	var err error
	if invocation.RecordID == "" {
		var object actionmodel.ActionObjectResult
		object, claim, replay, err = m.executions.BeginObject(ctx, action.ObjectKey, action.Key, invocation.IdempotencyKey, fingerprint, invocation.Principal)
		if replay {
			cached = invocationResultFromObject(invocation, action, object)
		}
	} else {
		var record actionmodel.ActionResult
		record, claim, replay, err = m.executions.BeginRecord(ctx, action.ObjectKey, invocation.RecordID, action.Key, invocation.IdempotencyKey, fingerprint, invocation.Principal)
		if replay {
			cached = invocationResultFromRecord(invocation, action, record)
		}
	}
	return cached, &actionUnitOfWork{manager: m, claim: claim, phases: newActionExecutionPhaseMachine()}, replay, err
}

func (u *actionUnitOfWork) executionID() string {
	if u == nil {
		return ""
	}
	return strings.TrimSpace(u.claim.Execution.ID)
}

func (u *actionUnitOfWork) commit(ctx context.Context, result any, commits []transactionmodel.RecordMutationCommit, audits []auditmodel.AuditEvent) error {
	if u == nil || u.manager == nil {
		return nil
	}
	if u.transaction == nil {
		if _, err := u.beginWriting(ctx); err != nil {
			return err
		}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.activeSynchronousConnectorCalls > 0 {
		return apperror.New(apperror.KindConflict, runtimeext.ActionWriteDuringConnectorCallErrorCode, nil, nil)
	}
	err := u.manager.executions.CommitTransaction(ctx, u.transaction, u.claim, result, commits, audits)
	if err != nil {
		if !mutation.IsTransactionCommitUnknown(err) {
			_ = u.phases.rollBack()
		}
		return err
	}
	return u.phases.commit()
}

func (u *actionUnitOfWork) beginWriting(ctx context.Context) (context.Context, error) {
	if u == nil || u.manager == nil {
		return ctx, apperror.New(apperror.KindInternal, "backend.action.transaction_unavailable", nil, nil)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.activeSynchronousConnectorCalls > 0 {
		return ctx, apperror.New(apperror.KindConflict, runtimeext.ActionWriteDuringConnectorCallErrorCode, nil, nil)
	}
	if u.transaction != nil {
		return u.transaction.Context(ctx), u.phases.beginWriting()
	}
	transaction, err := u.manager.executions.BeginTransaction(ctx)
	if err != nil {
		_ = u.phases.rollBack()
		return ctx, err
	}
	u.transaction = transaction
	if err := u.phases.beginWriting(); err != nil {
		_ = transaction.RollBack(ctx)
		return ctx, err
	}
	return transaction.Context(ctx), nil
}

// beginDeferredWriting closes the synchronous Connector-call window without
// taking a database connection. Create planners may resolve relations through
// independently composed modules backed by the same SQLite pool; opening the
// Action transaction first would retain the pool's sole connection while that
// resolver waits for another one. The physical transaction is still opened by
// a later locking operation or by commit, so all staged mutations remain part
// of the same atomic commit.
func (u *actionUnitOfWork) beginDeferredWriting(ctx context.Context) (context.Context, error) {
	if u == nil || u.manager == nil {
		return ctx, apperror.New(apperror.KindInternal, "backend.action.transaction_unavailable", nil, nil)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.activeSynchronousConnectorCalls > 0 {
		return ctx, apperror.New(apperror.KindConflict, runtimeext.ActionWriteDuringConnectorCallErrorCode, nil, nil)
	}
	if err := u.phases.beginWriting(); err != nil {
		return ctx, err
	}
	if u.transaction != nil {
		return u.transaction.Context(ctx), nil
	}
	return ctx, nil
}

func (u *actionUnitOfWork) acquireSynchronousConnectorCall() (runtimeext.SynchronousConnectorCallLease, error) {
	if u == nil {
		return nil, apperror.New(apperror.KindInternal, runtimeext.ConnectorActionExecutionRequiredErrorCode, nil, nil)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	phase := u.phases.current()
	if u.transaction != nil || !u.phases.allowsSynchronousConnectorCall() {
		return nil, apperror.New(apperror.KindConflict, runtimeext.ConnectorCallAfterWriteErrorCode, nil, map[string]string{"phase": string(phase)})
	}
	u.activeSynchronousConnectorCalls++
	return &actionSynchronousConnectorCallLease{unitOfWork: u}, nil
}

func (u *actionUnitOfWork) executionContext(ctx context.Context) context.Context {
	if u == nil {
		return ctx
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.transaction != nil {
		return u.transaction.Context(ctx)
	}
	return ctx
}

func (u *actionUnitOfWork) rollBack(ctx context.Context) {
	if u != nil {
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.transaction != nil {
			_ = u.transaction.RollBack(ctx)
			u.transaction = nil
		}
		_ = u.phases.rollBack()
	}
}

func (u *actionUnitOfWork) fail(ctx context.Context, result actionmodel.ActionInvocationResult, err error, audits []auditmodel.AuditEvent) error {
	if u == nil || u.manager == nil || mutation.IsTransactionCommitUnknown(err) {
		return nil
	}
	return u.manager.executions.Fail(ctx, u.claim, result, err, audits)
}

func (m *ActionUnitOfWorkManager) executionRuntime() *actionruntime.ActionExecutionRuntime {
	if m == nil {
		return nil
	}
	return m.executions
}
