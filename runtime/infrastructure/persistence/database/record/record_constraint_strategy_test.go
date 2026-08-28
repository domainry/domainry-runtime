package record

import "testing"

func TestRecordConstraintLockClauseIsDialectSafe(t *testing.T) {
	for _, test := range []struct {
		driver string
		want   string
	}{
		{driver: "sqlite", want: ""},
		{driver: "mysql", want: " FOR UPDATE"},
		{driver: "postgres", want: " FOR UPDATE"},
	} {
		if got := recordConstraintLockClause(test.driver); got != test.want {
			t.Fatalf("driver=%s clause=%q want=%q", test.driver, got, test.want)
		}
	}
}
