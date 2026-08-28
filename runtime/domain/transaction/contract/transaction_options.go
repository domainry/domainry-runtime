package contract

import "time"

// Isolation is a storage-neutral transaction isolation decision owned by the
// use case/database policy rather than by Transport or a SQL adapter caller.
type Isolation string

const (
	IsolationDefault        Isolation = "default"
	IsolationReadCommitted  Isolation = "read_committed"
	IsolationRepeatableRead Isolation = "repeatable_read"
	IsolationSerializable   Isolation = "serializable"
)

// TransactionSideEffectMode declares whether an operation can be safely
// repeated after its database transaction rolls back.
type TransactionSideEffectMode string

const (
	TransactionSideEffectsUnspecified       TransactionSideEffectMode = "unspecified"
	TransactionSideEffectsTransactionalOnly TransactionSideEffectMode = "transactional_only"
	TransactionSideEffectsMayEscape         TransactionSideEffectMode = "may_escape_transaction"
)

// TransactionRetryPolicy enables bounded retries only for operations whose
// effects are fully contained by the transaction.
type TransactionRetryPolicy struct {
	MaxAttempts      int
	SideEffects      TransactionSideEffectMode
	IdempotencyScope TransactionIdempotencyScope
	InitialBackoff   time.Duration
	MaxBackoff       time.Duration
	Deadline         time.Time
	RetryBudget      time.Duration
}

const MaxTransactionRetryAttempts = 5

// Options is the explicit policy supplied when composing an owner UnitOfWork.
type Options struct {
	Isolation Isolation
	ReadOnly  bool
	Retry     TransactionRetryPolicy
}
