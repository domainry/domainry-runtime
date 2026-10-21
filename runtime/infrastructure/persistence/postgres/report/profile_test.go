package report

import (
	"strings"
	"testing"
)

func TestDateBucketUsesUnixMillisecondsAndCanonicalZone(t *testing.T) {
	got, err := (Profile{}).DateBucket(`"event"."occurred_at"`, "day", "Asia/Shanghai", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`TO_TIMESTAMP(("event"."occurred_at")::DOUBLE PRECISION / 1000.0)`,
		`AT TIME ZONE 'Asia/Shanghai'`,
		`DATE_TRUNC('day'`,
		`TO_CHAR(`,
		`YYYY-MM-DD"T"HH24:MI:SS`,
		`EXTRACT(EPOCH FROM`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("date bucket missing %q: %s", fragment, got)
		}
	}
}

func TestDateBucketDateAndUTC(t *testing.T) {
	dateBucket, err := (Profile{}).DateBucket(`"period"."business_date"`, "week", "UTC", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dateBucket, `CAST("period"."business_date" AS TIMESTAMP)`) || !strings.Contains(dateBucket, `DATE_TRUNC('week'`) || !strings.HasSuffix(dateBucket, ` || 'Z')`) || strings.Contains(dateBucket, "TO_TIMESTAMP") {
		t.Fatalf("invalid date-backed UTC bucket: %s", dateBucket)
	}
}

func TestDateBucketRejectsInvalidZone(t *testing.T) {
	if _, err := (Profile{}).DateBucket("value", "day", "Not/AZone", false); err == nil {
		t.Fatal("invalid time zone was accepted")
	}
}
