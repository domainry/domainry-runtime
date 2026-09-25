package timevalue

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func Millis(value any) int64 {
	if instant, ok := value.(time.Time); ok {
		if instant.IsZero() {
			return 0
		}
		return instant.UTC().UnixMilli()
	}
	if millis, ok := value.(int64); ok {
		return millis
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return 0
	}
	if millis, err := strconv.ParseInt(text, 10, 64); err == nil {
		return millis
	}
	if instant, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return instant.UTC().UnixMilli()
	}
	return 0
}

func String(value any) string {
	millis := Millis(value)
	if millis == 0 {
		return ""
	}
	return time.UnixMilli(millis).UTC().Format(time.RFC3339Nano)
}

func Now() int64 { return time.Now().UTC().UnixMilli() }
