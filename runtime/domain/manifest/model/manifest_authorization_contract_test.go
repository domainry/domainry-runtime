package manifestmodel

import "testing"

func TestDecodeManifestAcceptsRoleDataPermissionsAndActionDefinition(t *testing.T) {
	raw := []byte(`{
		"schema_version":"2","template_id":"gym","version":"1","objects":[],
		"roles":[{"key":"member","name":"Member","permissions":["booking.read","booking.book"],"record_scope":"custom","data_permissions":[{"object_key":"booking","scope":"custom","read":true,"write":true,"predicate":{"operator":"eq","field_key":"member_id","value_source":"actor_claim","claim_key":"profile_id"}}]}],
		"actions":[{"key":"booking.book","object_key":"booking","label":"Book","kind":"record_operation","preconditions":[],"audit_event":"booking.booked"}]
	}`)
	manifest, _, err := DecodeManifest(raw)
	if err != nil {
		t.Fatalf("strict Runtime manifest rejected RoleSchema authorization contract: %v", err)
	}
	if len(manifest.Roles) != 1 || len(manifest.Roles[0].DataPermissions) != 1 || manifest.Roles[0].DataPermissions[0].Predicate == nil {
		t.Fatalf("role/data_permissions were not preserved: %#v", manifest.Roles)
	}
	if len(manifest.Actions) != 1 || manifest.Actions[0].Key != "booking.book" {
		t.Fatalf("Action definition was not preserved: %#v", manifest.Actions)
	}
}

func TestDecodeManifestRejectsRemovedActionRoleAllowlist(t *testing.T) {
	raw := []byte(`{
		"schema_version":"2","template_id":"gym","version":"1","objects":[],"roles":[],
		"actions":[{"key":"booking.book","object_key":"booking","label":"Book","kind":"record_operation","preconditions":[],"audit_event":"booking.booked","authorization":{"allowed_roles":["member"]}}]
	}`)
	if _, _, err := DecodeManifest(raw); err == nil {
		t.Fatal("removed Action authorization role allowlist was silently accepted")
	}
}
