package action

import (
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// Regression for platform finding #11: an action declaring a multi-segment
// requires_permission (e.g. "ticket.transition.start") passed the first
// authorization layer (ActionAllowed, via the 3-segment compatibility branch)
// but was re-derived by ActionAuthorization.Validate as the never-granted key
// "ticket.start", so every legitimate caller received 403
// backend.permission.denied. Both layers must interpret the declared
// permission identically: a role granted the exact declared string passes both.
func TestActionAuthorizationMultiSegmentPermissionKeyConsistent(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key:                "ticket.start_progress",
		ObjectKey:          "ticket",
		Kind:               "transition_state",
		RequiresPermission: "ticket.transition.start",
	}
	granted := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "agent_wang", WorkspaceID: "ws1", Known: true}}, accessfixture.Bundle{
		Key:         "support_agent",
		Permissions: []string{"ticket.read", "ticket.transition.start"},
		DataPolicies: []accessfixture.DataPolicyFixture{
			{ObjectKey: "ticket", Scope: "all_records", Read: true, Write: true},
		},
	},
	)
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema {
			return []definitionmodel.ObjectSchema{{Key: "ticket"}}
		},
	})
	authorization := ActionAuthorization{ObjectForAction: queryPolicy.ObjectForAction}

	if !ActionAllowed(granted, action) {
		t.Fatal("layer 1 (ActionAllowed) must accept a role granted the exact declared permission string")
	}
	if err := authorization.Validate(granted, action); err != nil {
		t.Fatalf("layer 2 (ActionAuthorization.Validate) must agree with layer 1 for the exact declared permission string, got: %v", err)
	}

	// A role without the grant must be denied by both layers.
	denied := granted
	denied = accessfixture.With(denied, accessfixture.Bundle{
		Key:         "viewer",
		Permissions: []string{"ticket.read"},
		DataPolicies: []accessfixture.DataPolicyFixture{
			{ObjectKey: "ticket", Scope: "all_records", Read: true, Write: true},
		},
	})
	if ActionAllowed(denied, action) {
		t.Fatal("layer 1 must deny a role without the declared permission")
	}
	err := authorization.Validate(denied, action)
	if err == nil {
		t.Fatal("layer 2 must deny a role without the declared permission")
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindForbidden {
		t.Fatalf("denial must stay a forbidden error, got: %v", err)
	}

	// The object-level wildcard must keep passing both layers.
	wildcard := granted
	wildcard = accessfixture.WithMutation(wildcard, func(role *accessfixture.Bundle) { role.Permissions = []string{"ticket.*"} })
	if !ActionAllowed(wildcard, action) {
		t.Fatal("layer 1 must accept the object wildcard grant")
	}
	if err := authorization.Validate(wildcard, action); err != nil {
		t.Fatalf("layer 2 must accept the object wildcard grant, got: %v", err)
	}

	// Two-segment keys keep their existing behavior in both layers.
	twoSegment := action
	twoSegment.RequiresPermission = "ticket.transition_start"
	granted2 := granted
	granted2 = accessfixture.WithMutation(granted2, func(role *accessfixture.Bundle) { role.Permissions = []string{"ticket.transition_start"} })
	if !ActionAllowed(granted2, twoSegment) {
		t.Fatal("layer 1 must accept the exact two-segment grant")
	}
	if err := authorization.Validate(granted2, twoSegment); err != nil {
		t.Fatalf("layer 2 must accept the exact two-segment grant, got: %v", err)
	}
}
