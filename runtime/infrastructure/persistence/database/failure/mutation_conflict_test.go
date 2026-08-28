package failure

import (
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/mutation"
)

func TestMutationConstraintErrorMapsOnlyDuplicateConstraints(t *testing.T) {
	if err := ConstraintError(nil, "customer", "customer-1", mutation.MutationConflictUnique); err != nil {
		t.Fatalf("nil constraint error = %v", err)
	}
	for _, message := range []string{
		"UNIQUE constraint failed: customer.id",
		"Error 1062: Duplicate entry 'customer-1' for key 'PRIMARY'",
		`pq: duplicate key value violates unique constraint "customer_pkey"`,
		`ERROR: duplicate key value violates unique constraint "customer_pkey" (SQLSTATE 23505)`,
		`constraint failed (2067)`,
		`duplicate key`,
		`primary key constraint`,
		`sqlstate 23505`,
		`error 1062`,
		`constraint failed (1555)`,
	} {
		err := ConstraintError(errors.New(message), "customer", "customer-1", mutation.MutationConflictUnique)
		if !mutation.IsMutationConflict(err, mutation.MutationConflictUnique) {
			t.Fatalf("expected duplicate constraint to map to typed conflict for %q, got %v", message, err)
		}
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "constraint") {
			t.Fatalf("typed conflict leaked dialect error for %q: %v", message, err)
		}
	}
	idempotencyConflict := ConstraintError(errors.New("UNIQUE constraint failed: receipt.scope"), "receipt", "receipt-1", mutation.MutationConflictIdempotency)
	if !mutation.IsMutationConflict(idempotencyConflict, mutation.MutationConflictIdempotency) {
		t.Fatalf("idempotency duplicate was not mapped uniformly: %v", idempotencyConflict)
	}
	if code := mutation.StableConflictCode(mutation.MutationConflictIdempotency); code != "backend.mutation.idempotency_conflict" {
		t.Fatalf("stable idempotency conflict code = %q", code)
	}

	notNull := errors.New("NOT NULL constraint failed: customer.created_at")
	if err := ConstraintError(notNull, "customer", "customer-1", mutation.MutationConflictUnique); !errors.Is(err, notNull) || mutation.IsMutationConflict(err, mutation.MutationConflictUnique) {
		t.Fatalf("non-duplicate constraint must remain a storage error, got %v", err)
	}
}

func TestMutationTransactionErrorMapsIndependentTransientKinds(t *testing.T) {
	if err := TransactionError(nil, "customer", "customer-1"); err != nil {
		t.Fatalf("nil transaction error = %v", err)
	}
	for _, testCase := range []struct {
		message string
		kind    mutation.TransactionTransientKind
	}{
		{message: "database is locked", kind: mutation.TransactionTransientLockTimeout},
		{message: "database table is locked: customer", kind: mutation.TransactionTransientLockTimeout},
		{message: "Error 1205: Lock wait timeout exceeded; try restarting transaction", kind: mutation.TransactionTransientLockTimeout},
		{message: "Error 1213: Deadlock found when trying to get lock; try restarting transaction", kind: mutation.TransactionTransientDeadlock},
		{message: "pq: could not serialize access due to concurrent update (SQLSTATE 40001)", kind: mutation.TransactionTransientSerializationFailure},
		{message: "pq: deadlock detected (SQLSTATE 40P01)", kind: mutation.TransactionTransientDeadlock},
	} {
		err := TransactionError(errors.New(testCase.message), "customer", "customer-1")
		if !mutation.IsTransactionTransient(err, testCase.kind) {
			t.Fatalf("expected transient kind %q for %q, got %v", testCase.kind, testCase.message, err)
		}
		if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
			t.Fatalf("transient failure must not be reported as optimistic conflict: %v", err)
		}
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(testCase.message)) {
			t.Fatalf("transient failure leaked dialect error for %q: %v", testCase.message, err)
		}
	}

	original := errors.New("connection reset by peer")
	if err := TransactionError(original, "customer", "customer-1"); !errors.Is(err, original) || mutation.IsMutationConflict(err, "") {
		t.Fatalf("non-lock transaction error must remain unchanged, got %v", err)
	}
	existing := mutation.MutationConflict("customer", "customer-1", mutation.MutationConflictUnique, errors.New("duplicate"))
	if err := TransactionError(existing, "customer", "customer-1"); !mutation.IsMutationConflict(err, mutation.MutationConflictUnique) {
		t.Fatalf("existing typed conflict must be preserved, got %v", err)
	}
	existingTransient := mutation.TransactionTransient("customer", "customer-1", mutation.TransactionTransientDeadlock, errors.New("dialect deadlock"))
	if err := TransactionError(existingTransient, "customer", "customer-1"); !mutation.IsTransactionTransient(err, mutation.TransactionTransientDeadlock) {
		t.Fatalf("existing typed transient failure must be preserved, got %v", err)
	}
}
