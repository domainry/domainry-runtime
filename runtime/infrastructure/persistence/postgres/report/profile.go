package report

import (
	"fmt"
	"strings"
	"time"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain, timeZone string, date bool) (string, error) {
	if _, err := time.LoadLocation(timeZone); err != nil {
		return "", fmt.Errorf("invalid report timezone %q: %w", timeZone, err)
	}
	zone := strings.ReplaceAll(timeZone, "'", "''")
	local := "TO_TIMESTAMP((" + value + ")::DOUBLE PRECISION / 1000.0) AT TIME ZONE '" + zone + "'"
	if date {
		local = "CAST(" + value + " AS TIMESTAMP)"
	}
	bucket := "DATE_TRUNC('" + grain + "', " + local + ")"
	formatted := "TO_CHAR(" + bucket + ", 'YYYY-MM-DD\"T\"HH24:MI:SS')"
	if timeZone == "UTC" {
		return "(" + formatted + " || 'Z')", nil
	}
	offset := "EXTRACT(EPOCH FROM (" + bucket + " - ((" + bucket + " AT TIME ZONE '" + zone + "') AT TIME ZONE 'UTC')))"
	suffix := "(CASE WHEN " + offset + " >= 0 THEN '+' ELSE '-' END || " +
		"LPAD(FLOOR(ABS(" + offset + ") / 3600)::TEXT, 2, '0') || ':' || " +
		"LPAD(FLOOR(MOD(ABS(" + offset + ")::NUMERIC, 3600) / 60)::TEXT, 2, '0'))"
	return "(" + formatted + " || " + suffix + ")", nil
}
