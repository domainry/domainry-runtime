package manifestmodel

import "testing"

func TestManifestApplicationTimeZone(t *testing.T) {
	for _, zone := range []string{"", "UTC", "Asia/Tokyo", "America/New_York"} {
		manifest := ManifestSchema{TimeZone: zone}
		if err := manifest.ValidateTimeZone(); err != nil {
			t.Fatal(err)
		}
		want := zone
		if want == "" {
			want = "UTC"
		}
		if manifest.EffectiveTimeZone() != want {
			t.Fatal("legacy default changed")
		}
	}
	for _, zone := range []string{"Local", "+09:00", "JST", "Not/AZone", " Asia/Tokyo ", " "} {
		if err := (ManifestSchema{TimeZone: zone}).ValidateTimeZone(); err == nil {
			t.Fatalf("invalid time zone %q accepted", zone)
		}
	}
}
