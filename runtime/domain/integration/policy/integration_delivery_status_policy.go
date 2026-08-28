package policy

import "strings"

// IntegrationDeliveryStatusAdvances protects provider acknowledgement facts
// from delayed, duplicated, or out-of-order callbacks. Positive provider
// evidence is monotonic; a late failure cannot overwrite delivered/read.
func IntegrationDeliveryStatusAdvances(current, next string) bool {
	current, next = strings.TrimSpace(current), strings.TrimSpace(next)
	if current == next || next == "" {
		return false
	}
	ranks := map[string]int{"sent": 1, "delivered": 2, "read": 3}
	if next == "failed" {
		return current != "failed" && ranks[current] < ranks["delivered"]
	}
	nextRank := ranks[next]
	if nextRank == 0 {
		return false
	}
	return nextRank > ranks[current]
}
