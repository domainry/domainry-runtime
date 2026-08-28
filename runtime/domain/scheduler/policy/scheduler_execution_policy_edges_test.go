package policy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestSchedulerExecutionPolicyCompleteMatrix(t *testing.T) {
	for _, test := range []struct {
		attempt int
		status  string
		want    int
	}{{0, "queued", 1}, {2, "queued", 2}, {0, "leased", 1}, {2, "leased", 2}, {0, "failed", 1}, {2, "failed", 3}} {
		run := recordmodel.Record{Data: map[string]any{"attempt": test.attempt}}
		if got := SchedulerNextAttempt(run, test.status); got != test.want {
			t.Fatalf("next attempt %d/%s = %d, want %d", test.attempt, test.status, got, test.want)
		}
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, key := range []string{"lease_expires_at", "next_retry_at"} {
		for _, test := range []struct {
			value any
			want  bool
		}{{nil, true}, {"", true}, {"<nil>", true}, {"invalid", true}, {now.Add(-time.Second).Format(time.RFC3339), true}, {now.Format(time.RFC3339), true}, {now.Add(time.Second).Format(time.RFC3339), false}} {
			run := recordmodel.Record{Data: map[string]any{key: test.value}}
			var got bool
			if key == "lease_expires_at" {
				got = SchedulerLeaseExpired(run, now)
			} else {
				got = SchedulerRetryDue(run, now)
			}
			if got != test.want {
				t.Fatalf("%s %#v = %v, want %v", key, test.value, got, test.want)
			}
		}
	}
	for _, test := range []struct {
		value    any
		fallback int
		want     int
	}{{1, 9, 1}, {int64(2), 9, 2}, {3.9, 9, 3}, {json.Number("4"), 9, 4}, {json.Number("bad"), 9, 9}, {" 4 ", 9, 4}, {"bad", 9, 9}, {true, 9, 9}} {
		if got := SchedulerInt(test.value, test.fallback); got != test.want {
			t.Fatalf("scheduler int %#v = %d, want %d", test.value, got, test.want)
		}
	}
	if SchedulerMetadataJSON(nil) != "{}" || SchedulerMetadataJSON(map[string]any{"bad": make(chan int)}) != "{}" {
		t.Fatal("empty/unencodable metadata must use empty object")
	}
	if got := SchedulerMetadataJSON(map[string]any{"attempt": 2}); !strings.Contains(got, `"attempt":2`) {
		t.Fatalf("metadata JSON = %q", got)
	}
	for _, test := range []struct {
		value string
		want  string
	}{{" Daily / Billing ", "daily_billing"}, {"___", "job"}, {"", "job"}, {"A--B", "a_b"}} {
		if got := SchedulerSlug(test.value); got != test.want {
			t.Fatalf("slug %q = %q, want %q", test.value, got, test.want)
		}
	}
}
