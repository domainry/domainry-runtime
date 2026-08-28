package policy

import "testing"

func TestActionKindsRemainDisjoint(t *testing.T) {
	if !ActionIsObjectKind("object_create") || ActionIsRecordKind("object_create") {
		t.Fatal("object_create must remain object-scoped")
	}
	if !ActionIsRecordKind("record_update") || ActionIsObjectKind("record_update") {
		t.Fatal("record_update must remain record-scoped")
	}
	for _, kind := range []string{"record_restore", "transition_state", "conditional_update"} {
		if !ActionIsRecordKind(kind) || ActionIsObjectKind(kind) {
			t.Fatalf("%s must remain record-scoped", kind)
		}
	}
	if ActionIsObjectKind("unknown") || ActionIsRecordKind("unknown") {
		t.Fatal("unknown kind must not enter an executable scope")
	}
}
