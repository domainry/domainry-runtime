package runtimeext

import (
	"errors"
	"testing"
)

type applicationZoneExecution struct {
	ActionExecution
	zone string
	err  error
}

func (e applicationZoneExecution) ApplicationTimeZone() (string, error) { return e.zone, e.err }

func TestApplicationTimeZoneDoesNotUseHostDefaults(t *testing.T) {
	t.Setenv("TZ", "America/Los_Angeles")
	for _, zone := range []string{"UTC", "Asia/Tokyo", "America/New_York", "Asia/Kathmandu"} {
		got, err := ResolveApplicationTimeZone(applicationZoneExecution{zone: zone})
		if err != nil || got != zone {
			t.Fatalf("zone=%q got=%q error=%v", zone, got, err)
		}
	}
	for _, zone := range []string{"", "Local", "EST", "GMT", " Asia/Tokyo", "Asia/Tokyo ", "+09:00", "No/SuchZone", "/etc/localtime"} {
		if got, err := ResolveApplicationTimeZone(applicationZoneExecution{zone: zone}); err == nil || got != "" {
			t.Fatalf("accepted unavailable/invalid zone %q: %q %v", zone, got, err)
		}
	}
	if _, err := ResolveApplicationTimeZone(struct{ ActionExecution }{}); err == nil {
		t.Fatal("an old adapter without the capability silently chose a default")
	}
	wanted := errors.New("catalog unavailable")
	if _, err := ResolveApplicationTimeZone(applicationZoneExecution{err: wanted}); err != wanted {
		t.Fatalf("source error changed: %v", err)
	}
}
