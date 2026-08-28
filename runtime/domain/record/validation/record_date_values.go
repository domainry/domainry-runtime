package validation

import (
	"fmt"
	"strings"
	"time"
)

// RecordDateOnly normalizes Runtime domain date values for comparisons that do not
// carry time-of-day semantics.
func RecordDateOnly(value any) (time.Time, bool) {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return time.Time{}, false
	}
	if len(text) >= 10 {
		text = text[:10]
	}
	parsed, err := time.Parse("2006-01-02", text)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
