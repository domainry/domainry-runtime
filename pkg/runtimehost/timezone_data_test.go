package runtimehost

import (
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

// The release smoke test runs this same host test binary without system
// zoneinfo or a Go installation. Do not import time/tzdata from the test:
// availability must come from the production host package.
func TestRuntimeHostApplicationTimeZones(t *testing.T) {
	for _, test := range []struct {
		zone     string
		instant  string
		wantTime string
	}{
		{"Asia/Tokyo", "2026-09-06T21:00:00Z", "2026-09-07T06:00:00+09:00"},
		{"Asia/Shanghai", "2026-09-06T21:00:00Z", "2026-09-07T05:00:00+08:00"},
		{"Asia/Kolkata", "2026-09-06T21:00:00Z", "2026-09-07T02:30:00+05:30"},
		{"America/New_York", "2026-01-15T12:00:00Z", "2026-01-15T07:00:00-05:00"},
		{"America/New_York", "2026-07-15T12:00:00Z", "2026-07-15T08:00:00-04:00"},
		{"UTC", "2026-09-06T21:00:00Z", "2026-09-06T21:00:00Z"},
	} {
		t.Run(test.zone+"/"+test.instant, func(t *testing.T) {
			transform := runtimeext.CrossWorkspaceAggregateDateBucketTransform{Grain: "hour", TimeZone: test.zone}
			if !transform.Valid() {
				t.Fatalf("application time zone rejected by Runtime descriptor: %s", test.zone)
			}
			location, err := time.LoadLocation(test.zone)
			if err != nil {
				t.Fatalf("load application time zone %s: %v", test.zone, err)
			}
			instant, err := time.Parse(time.RFC3339, test.instant)
			if err != nil {
				t.Fatal(err)
			}
			if got := instant.In(location).Format(time.RFC3339); got != test.wantTime {
				t.Fatalf("application time conversion: got %s, want %s", got, test.wantTime)
			}
		})
	}
	for _, zone := range []string{"Not/AZone", "Local", "+09:00", ""} {
		if (runtimeext.CrossWorkspaceAggregateDateBucketTransform{Grain: "hour", TimeZone: zone}).Valid() {
			t.Errorf("invalid application time zone accepted: %q", zone)
		}
	}
}
