package profilebindingmodel

import "testing"

func TestReservedFieldOwnerForIdentityUserFacts(t *testing.T) {
	for _, field := range []string{"account_status", "password", "org_id", "manager_user_id", "reporting_path", "worker_no", "worker_type", "work_status", "start_date", "end_date"} {
		if owner, owned := ReservedFieldOwnerFor(field); !owned || owner != ReservedFieldOwnerIdentityUser {
			t.Errorf("identity user field %q owner=%q owned=%v", field, owner, owned)
		}
	}
	for _, field := range []string{"email", "phone", "gender", "member_status", "student_no"} {
		if owner, owned := ReservedFieldOwnerFor(field); owned || owner != "" {
			t.Errorf("business field %q owner=%q owned=%v", field, owner, owned)
		}
	}
}
