package principal

import (
	"testing"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

// TestSoleActiveBusinessProfileIsSelectedWithoutAHeader pins the case a
// delivered frontend actually produces: it sends no X-Business-Profile-Key or
// -ID, and the signed-in staff user holds exactly one active profile. Leaving
// that principal unselected made every display-name attribution fall back to a
// raw user id and refused profile-gated Actions outright.
func TestSoleActiveBusinessProfileIsSelectedWithoutAHeader(t *testing.T) {
	sole := []profilebindingmodel.Reference{{BindingKey: "staff", ObjectKey: "staff_account", RecordID: "rec-1"}}
	selected, found, err := selectBusinessProfile(sole, "", "")
	if err != nil {
		t.Fatalf("a sole active profile must resolve, got %v", err)
	}
	if !found || selected.RecordID != "rec-1" {
		t.Fatalf("a sole active profile was not selected: found=%t selected=%+v", found, selected)
	}

	// Two profiles is a real ambiguity: only the caller can settle it, so the
	// principal stays unselected rather than guessing.
	two := append(append([]profilebindingmodel.Reference{}, sole...),
		profilebindingmodel.Reference{BindingKey: "vendor", ObjectKey: "vendor_account", RecordID: "rec-2"})
	if _, found, err := selectBusinessProfile(two, "", ""); err != nil || found {
		t.Fatalf("two profiles must stay unselected without a selector: found=%t err=%v", found, err)
	}

	// No profile at all stays unselected and is not an error.
	if _, found, err := selectBusinessProfile(nil, "", ""); err != nil || found {
		t.Fatalf("no profile must stay unselected: found=%t err=%v", found, err)
	}

	// An explicit selector still has to match exactly one.
	if _, found, err := selectBusinessProfile(sole, "staff", ""); err != nil || !found {
		t.Fatalf("an explicit matching selector must still resolve: found=%t err=%v", found, err)
	}
	if _, _, err := selectBusinessProfile(sole, "missing", ""); err == nil {
		t.Fatal("a selector matching no profile must still be refused")
	}
}
