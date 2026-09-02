package projection

import "testing"

func TestOperationsRunbookCategories(t *testing.T) {
	cases := map[string]string{
		"backend.operations.database_retirement_blocked": "database-retirement",
		"backend.operations.bulk_confirmation_stale":     "bounded-bulk",
		"backend.operations.dead_letter_missing":         "dead-letter",
		"backend.operations.lease_owned":                 "lease-and-drain",
		"backend.operations.drain_timeout":               "lease-and-drain",
		"backend.operations.diagnostics_section":         "diagnostics",
		"backend.operations.break_glass_expired":         "break-glass",
		"backend.operations.control_revision":            "maintenance-and-worker-control",
		"backend.operations.other":                       "control-plane",
	}
	for code, category := range cases {
		link, ok := OperationsRunbookForError(" " + code + " ")
		if !ok || link.Category != category || link.ErrorCode != code || link.URL == "" || len(link.NextActions) != 3 {
			t.Errorf("%q => %#v, %v", code, link, ok)
		}
	}
	for _, code := range []string{"", "backend.other.error"} {
		if _, ok := OperationsRunbookForError(code); ok {
			t.Errorf("non-operations code %q accepted", code)
		}
	}
}

func TestOperationsDefinitionsAreIndependentAndMissingLookup(t *testing.T) {
	definitions := OperationsDefinitions()
	originalActionKey := definitions[0].ActionKey
	definitions[0].ActionKey = "mutated"
	definitions[0].Preconditions[0] = "mutated"
	definitions[0].FailureSemantics[0] = "mutated"
	fresh := OperationsDefinitions()
	if fresh[0].ActionKey != originalActionKey || fresh[0].Preconditions[0] == "mutated" || fresh[0].FailureSemantics[0] == "mutated" {
		t.Fatal("operations definitions leaked mutable slices")
	}
	if _, ok := OperationsDefinition("missing"); ok {
		t.Fatal("missing operation found")
	}
}
