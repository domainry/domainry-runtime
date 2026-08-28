package policy

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// Regression for platform finding #11: the permission subject must be derived
// relative to the Action's object key so multi-segment keys such as
// "ticket.transition.start" are evaluated as (ticket, transition.start) by
// every authorization layer instead of being re-split into the never-granted
// (ticket, start).
func TestActionPermissionSubjectObjectRelative(t *testing.T) {
	for _, test := range []struct {
		name       string
		action     definitionmodel.ActionSchema
		wantObject string
		wantAction string
	}{
		{
			name:       "multi-segment key prefixed with object key",
			action:     definitionmodel.ActionSchema{Key: "ticket.start_progress", ObjectKey: "ticket", RequiresPermission: "ticket.transition.start"},
			wantObject: "ticket", wantAction: "transition.start",
		},
		{
			name:       "two-segment key prefixed with object key",
			action:     definitionmodel.ActionSchema{Key: "ticket.start_progress", ObjectKey: "ticket", RequiresPermission: "ticket.transition_start"},
			wantObject: "ticket", wantAction: "transition_start",
		},
		{
			name:       "permission defaults to action key",
			action:     definitionmodel.ActionSchema{Key: "ticket.start_progress", ObjectKey: "ticket"},
			wantObject: "ticket", wantAction: "start_progress",
		},
		{
			name:       "legacy fallback without object prefix",
			action:     definitionmodel.ActionSchema{Key: "ignored", RequiresPermission: "sales.order.approve"},
			wantObject: "order", wantAction: "approve",
		},
	} {
		object, action := definitionmodel.ActionPermissionSubject(test.action)
		if object != test.wantObject || action != test.wantAction {
			t.Fatalf("%s: subject = %q/%q, want %q/%q", test.name, object, action, test.wantObject, test.wantAction)
		}
	}
	if got := ActionName(definitionmodel.ActionSchema{Key: "ticket.start_progress", ObjectKey: "ticket", RequiresPermission: "ticket.transition.start"}); got != "transition.start" {
		t.Fatalf("ActionName must stay consistent with the permission subject, got %q", got)
	}
}
