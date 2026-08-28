package policy

import (
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestCheckPreconditionsWithoutAggregate(t *testing.T) {
	action := definitionmodel.ActionSchema{Preconditions: []string{"status in draft,open", "amount > 0", "customer is present"}}
	valid := map[string]any{"status": "draft", "amount": 10, "customer": "customer-1"}
	if err := ActionCheckPreconditions(action, valid); err != nil {
		t.Fatalf("valid preconditions rejected: %v", err)
	}
	invalid := map[string]any{"status": "closed", "amount": 0}
	if err := ActionCheckPreconditions(action, invalid); err == nil || err.Error() != "backend.action.precondition_failed" {
		t.Fatalf("invalid preconditions error=%v", err)
	}
}

func TestCheckPreconditionsFieldAndDateComparisons(t *testing.T) {
	action := definitionmodel.ActionSchema{Preconditions: []string{
		"forecast_quantity < minimum_quantity",
		"available_balance >= requested_days",
		"response_due_at >= today",
		"ready == true",
	}}
	valid := map[string]any{
		"forecast_quantity": 3, "minimum_quantity": 5,
		"available_balance": 10, "requested_days": 3,
		"response_due_at": time.Now().UTC().AddDate(0, 0, 1).Format(time.RFC3339), "ready": true,
	}
	if err := ActionCheckPreconditions(action, valid); err != nil {
		t.Fatalf("valid generic comparisons rejected: %v", err)
	}
	valid["forecast_quantity"] = 6
	if err := ActionCheckPreconditions(action, valid); err == nil || err.Error() != "backend.action.precondition_failed" {
		t.Fatalf("invalid field comparison error=%v", err)
	}
}
