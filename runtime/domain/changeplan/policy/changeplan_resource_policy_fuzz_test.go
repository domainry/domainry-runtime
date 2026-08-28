package policy

import (
	"strings"
	"testing"
)

func FuzzChangePlanCanonicalResourceTypeIsIdempotent(f *testing.F) {
	f.Add(" automation_rule ")
	f.Add("custom_resource")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		canonical := ChangePlanCanonicalResourceType(value)
		if canonical != strings.TrimSpace(canonical) {
			t.Fatalf("canonical value retains outer whitespace: %q", canonical)
		}
		if second := ChangePlanCanonicalResourceType(canonical); second != canonical {
			t.Fatalf("canonicalization is not idempotent: %q -> %q", canonical, second)
		}
	})
}
