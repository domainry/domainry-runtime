package validation

import (
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestValidateBusinessSeedKeyAndRecordID(t *testing.T) {
	if err := ValidateBusinessSeedKey("customer.acme-1"); err != nil {
		t.Fatal(err)
	}
	for value, code := range map[string]string{"": "backend.business_seed.key_required", "Customer Acme": "backend.business_seed.key_invalid", "1customer": "backend.business_seed.key_invalid"} {
		if err := ValidateBusinessSeedKey(value); apperror.CodeOf(err) != code {
			t.Fatalf("value=%q code=%q err=%v", value, apperror.CodeOf(err), err)
		}
	}
	if got := BusinessSeedRecordID("customer", "acme.primary-1"); got != "customer_acme_primary_1" {
		t.Fatalf("record id=%q", got)
	}
}
