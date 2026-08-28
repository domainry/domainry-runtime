package record

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestGovernedProfileIdentityFieldRequiresBindingCommand(t *testing.T) {
	service := &RecordApplicationService{identityProfileExtensions: func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{
			{ObjectKey: "member_profile", IdentityRelationField: "identity_user", BindingLifecycle: profilebindingmodel.Lifecycle{AllowUnbound: true}},
			{ObjectKey: "legacy_profile", IdentityRelationField: "identity_user"},
		}
	}}
	for _, test := range []struct {
		name     string
		object   string
		values   map[string]any
		creating bool
		denied   bool
	}{
		{name: "unbound create omitted", object: "member_profile", values: map[string]any{"name": "Member"}, creating: true},
		{name: "unbound create nil", object: "member_profile", values: map[string]any{"identity_user": nil}, creating: true},
		{name: "bound create", object: "member_profile", values: map[string]any{"identity_user": "user-1"}, creating: true, denied: true},
		{name: "direct rebind", object: "member_profile", values: map[string]any{"identity_user": "user-2"}, denied: true},
		{name: "direct unlink", object: "member_profile", values: map[string]any{"identity_user": nil}, denied: true},
		{name: "unrelated patch", object: "member_profile", values: map[string]any{"status": "active"}},
		{name: "legacy relation", object: "legacy_profile", values: map[string]any{"identity_user": "user-1"}},
		{name: "unknown object", object: "other", values: map[string]any{"identity_user": "user-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := service.validateProfileBindingMutation(test.object, test.values, test.creating)
			if test.denied {
				if apperror.CodeOf(err) != "backend.identity.profile_binding_command_required" {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := (*RecordApplicationService)(nil).validateProfileBindingMutation("member_profile", map[string]any{"identity_user": "user-1"}, false); err != nil {
		t.Fatal(err)
	}
}
