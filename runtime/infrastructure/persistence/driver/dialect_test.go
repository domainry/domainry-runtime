package driver

import "testing"

func TestSQLIdentifierAndPlaceholderContract(t *testing.T) {
	for _, value := range []string{"A", "a", "_", "aA", "Runtime_01", "snake_case"} {
		if !ValidSQLIdentifier(value) {
			t.Errorf("valid identifier %q rejected", value)
		}
	}
	for _, value := range []string{"", "1table", "-table", "étable", "aé", "table-name", "table.name", "table name"} {
		if ValidSQLIdentifier(value) {
			t.Errorf("invalid identifier %q accepted", value)
		}
	}
	if got := QuoteIdentifier("runtime_table", `"`); got != `"runtime_table"` {
		t.Fatalf("quoted identifier = %q", got)
	}
	if got := QuestionPlaceholder(17); got != "?" {
		t.Fatalf("question placeholder = %q", got)
	}
}

func TestQuoteIdentifierRejectsUnsafeValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("unsafe identifier did not panic")
		}
	}()
	QuoteIdentifier("runtime;drop table", `"`)
}
