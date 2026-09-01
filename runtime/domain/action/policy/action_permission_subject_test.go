package policy

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// The subject decomposition is metadata only: joining both parts must preserve
// the complete Action key. Authorization compares ActionSchema.Key directly.
func TestActionPermissionSubjectObjectRelative(t *testing.T) {
	for _, test := range []struct {
		name       string
		action     definitionmodel.ActionSchema
		wantObject string
		wantAction string
	}{
		{
			name:       "multi-segment action key prefixed with object key",
			action:     definitionmodel.ActionSchema{Key: "ticket.transition.start", ObjectKey: "ticket"},
			wantObject: "ticket", wantAction: "transition.start",
		},
		{
			name:       "two-segment key prefixed with object key",
			action:     definitionmodel.ActionSchema{Key: "ticket.transition_start", ObjectKey: "ticket"},
			wantObject: "ticket", wantAction: "transition_start",
		},
		{
			name:       "two-segment action key",
			action:     definitionmodel.ActionSchema{Key: "ticket.start_progress", ObjectKey: "ticket"},
			wantObject: "ticket", wantAction: "start_progress",
		},
		{
			name:       "multi-segment action key without object context",
			action:     definitionmodel.ActionSchema{Key: "sales.order.approve"},
			wantObject: "sales.order", wantAction: "approve",
		},
	} {
		object, action := definitionmodel.ActionPermissionSubject(test.action)
		if object != test.wantObject || action != test.wantAction {
			t.Fatalf("%s: subject = %q/%q, want %q/%q", test.name, object, action, test.wantObject, test.wantAction)
		}
	}
	if got := ActionName(definitionmodel.ActionSchema{Key: "ticket.transition.start", ObjectKey: "ticket"}); got != "transition.start" {
		t.Fatalf("ActionName must stay consistent with the permission subject, got %q", got)
	}
}
