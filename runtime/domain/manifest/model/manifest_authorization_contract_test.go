package manifestmodel

import "testing"

func TestDecodeManifestAcceptsV6RoleDataPermissionsAndHandlerAuthorization(t *testing.T) {
	raw := []byte(`{
		"schema_version":"2","template_id":"gym","version":"1","objects":[],"views":[],
		"roles":[{"key":"member","name":"Member","permissions":["booking.read","booking.book"],"record_scope":"custom","data_permissions":[{"object_key":"booking","scope":"custom","read":true,"write":true,"predicate":{"operator":"eq","field_key":"member_id","value_source":"actor_claim","claim_key":"profile_id"}}]}],
		"actions":[{"key":"booking.book","object_key":"booking","label":"Book","kind":"record_operation","requires_permission":"booking.book","preconditions":[],"audit_event":"booking.booked","authorization":{"allowed_roles":["member"]}}]
	}`)
	manifest, _, err := DecodeManifest(raw)
	if err != nil {
		t.Fatalf("strict Runtime manifest rejected v6 authorization contract: %v", err)
	}
	if len(manifest.Roles) != 1 || len(manifest.Roles[0].DataPermissions) != 1 || manifest.Roles[0].DataPermissions[0].Predicate == nil {
		t.Fatalf("role/data_permissions were not preserved: %#v", manifest.Roles)
	}
	if len(manifest.Actions) != 1 || manifest.Actions[0].Authorization == nil || len(manifest.Actions[0].Authorization.AllowedRoles) != 1 {
		t.Fatalf("Handler authorization was not preserved: %#v", manifest.Actions)
	}
}
