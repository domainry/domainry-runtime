package policy

import (
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionCheckPreconditionsOperatorMatrix(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	tests := []struct {
		name      string
		condition string
		data      map[string]any
		wantError bool
	}{
		{"not in allowed", "status not in closed,lost", map[string]any{"status": "open"}, false},
		{"not in rejected", "status not in closed,lost", map[string]any{"status": "lost"}, true},
		{"in rejected", "status in draft,open", map[string]any{"status": "closed"}, true},
		{"present rejected", "customer is present", map[string]any{"customer": " "}, true},
		{"positive rejected type", "amount > 0", map[string]any{"amount": true}, true},
		{"positive rejected zero", "amount > 0", map[string]any{"amount": 0}, true},
		{"through today", "due_at <= today", map[string]any{"due_at": today}, false},
		{"through today future", "due_at <= today", map[string]any{"due_at": tomorrow}, true},
		{"through today invalid", "due_at <= today", map[string]any{"due_at": "bad"}, true},
		{"before today", "due_at < today", map[string]any{"due_at": yesterday}, false},
		{"before today rejected", "due_at < today", map[string]any{"due_at": today}, true},
		{"before today invalid", "due_at < today", map[string]any{"due_at": "bad"}, true},
		{"greater field", "actual > expected", map[string]any{"actual": 2, "expected": 1}, false},
		{"less equal literal", "actual <= 2", map[string]any{"actual": 2}, false},
		{"greater equal literal", "actual >= 2", map[string]any{"actual": 1}, true},
		{"less literal", "actual < bad", map[string]any{"actual": 1}, true},
		{"equals label", "approval status == APPROVED", map[string]any{"approval_status": "approved"}, false},
		{"equals rejected", "approval status == approved", map[string]any{"approval_status": "rejected"}, true},
		{"not equals quoted", "status != 'closed'", map[string]any{"status": "closed"}, true},
		{"not equals empty", "status != closed", map[string]any{"status": ""}, false},
		{"not equals blank right", "status != ''", map[string]any{"status": "open"}, false},
		{"not equals different", "status != closed", map[string]any{"status": "open"}, false},
		{"unknown ignored", "human readable note", nil, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ActionCheckPreconditions(definitionmodel.ActionSchema{Preconditions: []string{test.condition}}, test.data)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v wantError=%v", err, test.wantError)
			}
		})
	}
}

func TestActionPreconditionComparisonAndNumericHelpers(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	if !comparePrecondition(map[string]any{"date": today}, "date", "today", "<=") {
		t.Fatal("today comparison should match")
	}
	if comparePrecondition(map[string]any{"date": "bad"}, "date", "today", "<=") {
		t.Fatal("invalid date should not match")
	}
	if comparePrecondition(map[string]any{}, "left", "1", ">") || comparePrecondition(map[string]any{"left": 1}, "left", "bad", ">") {
		t.Fatal("invalid numeric operands should not match")
	}
	for _, test := range []struct {
		operator string
		left     float64
		right    float64
		want     bool
	}{
		{">=", 2, 2, true}, {"<=", 2, 2, true}, {">", 2, 1, true}, {"<", 1, 2, true}, {"?", 1, 1, false},
	} {
		if got := compareNumbers(test.left, test.right, test.operator); got != test.want {
			t.Fatalf("%v %s %v = %v", test.left, test.operator, test.right, got)
		}
	}
	for _, test := range []struct {
		value any
		want  float64
		ok    bool
	}{
		{float64(1.5), 1.5, true}, {float32(2.5), 2.5, true}, {int(3), 3, true}, {int64(4), 4, true}, {"5.5", 5.5, true}, {"bad", 0, false}, {true, 0, false},
	} {
		got, ok := ActionNumericValue(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("value=%#v got=%v,%v", test.value, got, ok)
		}
	}
}
