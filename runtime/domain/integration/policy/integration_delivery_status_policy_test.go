package policy

import "testing"

func TestIntegrationDeliveryStatusAdvancesMonotonicallyAcrossOutOfOrderCallbacks(t *testing.T) {
	tests := []struct {
		current string
		next    string
		want    bool
	}{
		{current: "sent", next: "delivered", want: true},
		{current: "delivered", next: "read", want: true},
		{current: "failed", next: "delivered", want: true},
		{current: "quarantined", next: "delivered", want: true},
		{current: "sent", next: "failed", want: true},
		{current: "delivered", next: "failed", want: false},
		{current: "read", next: "failed", want: false},
		{current: "read", next: "delivered", want: false},
		{current: "delivered", next: "sent", want: false},
		{current: "delivered", next: "delivered", want: false},
		{current: "sent", next: " ", want: false},
		{current: "sent", next: "queued", want: false},
	}
	for _, test := range tests {
		if got := IntegrationDeliveryStatusAdvances(test.current, test.next); got != test.want {
			t.Errorf("%s -> %s got=%v want=%v", test.current, test.next, got, test.want)
		}
	}
}
