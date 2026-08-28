package contract

import "testing"

func TestActiveTransactionBoundaryIsExplicitAndNilSafe(t *testing.T) {
	if ActiveTransaction(nil) || ActiveTransaction(t.Context()) {
		t.Fatal("ordinary context must not claim an active transaction")
	}
	if !ActiveTransaction(WithActiveTransaction(t.Context())) {
		t.Fatal("transaction context marker was not preserved")
	}
}
