package report

import (
	"fmt"
	"strings"
	"time"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain, timeZone string, date bool) (string, error) {
	target, fixed, err := mysqlTimeZoneTarget(timeZone)
	if err != nil {
		return "", err
	}
	utc := "TIMESTAMPADD(SECOND, (" + value + " DIV 1000), '1970-01-01 00:00:00')"
	local := utc
	if date {
		local = "CAST(" + value + " AS DATETIME)"
	} else if target != "+00:00" {
		local = "CONVERT_TZ(" + utc + ", '+00:00', '" + strings.ReplaceAll(target, "'", "''") + "')"
	}
	bucket, err := mysqlDateBucketExpression(local, grain)
	if err != nil {
		return "", err
	}
	if target == "+00:00" {
		return "CONCAT(" + bucket + ", 'Z')", nil
	}
	if fixed {
		return "CONCAT(" + bucket + ", '" + target + "')", nil
	}
	if date {
		utc = "CONVERT_TZ(" + local + ", '" + strings.ReplaceAll(target, "'", "''") + "', '+00:00')"
	}
	offset := "TIMESTAMPDIFF(SECOND, " + utc + ", " + local + ")"
	suffix := "CONCAT(CASE WHEN " + offset + " >= 0 THEN '+' ELSE '-' END, " +
		"LPAD(FLOOR(ABS(" + offset + ") / 3600), 2, '0'), ':', " +
		"LPAD(FLOOR(MOD(ABS(" + offset + "), 3600) / 60), 2, '0'))"
	return "CONCAT(" + bucket + ", " + suffix + ")", nil
}

func mysqlDateBucketExpression(value, grain string) (string, error) {
	formats := map[string]string{
		"hour":  "%Y-%m-%dT%H:00:00",
		"day":   "%Y-%m-%dT00:00:00",
		"month": "%Y-%m-01T00:00:00",
		"year":  "%Y-01-01T00:00:00",
	}
	if format := formats[grain]; format != "" {
		return "DATE_FORMAT(" + value + ", '" + format + "')", nil
	}
	switch grain {
	case "week":
		return "DATE_FORMAT(DATE_SUB(DATE(" + value + "), INTERVAL WEEKDAY(" + value + ") DAY), '%Y-%m-%dT00:00:00')", nil
	case "quarter":
		return "DATE_FORMAT(STR_TO_DATE(CONCAT(YEAR(" + value + "), '-', LPAD(((QUARTER(" + value + ") - 1) * 3) + 1, 2, '0'), '-01'), '%Y-%m-%d'), '%Y-%m-%dT00:00:00')", nil
	default:
		return "", fmt.Errorf("invalid report date grain %q", grain)
	}
}

// MySQL needs populated time-zone tables before CONVERT_TZ accepts an IANA
// name. Fixed-offset zones do not need that operational dependency. Detect
// zones whose offset is stable throughout the supported reporting window and
// render their numeric offset; variable-offset zones retain the IANA name so
// daylight-saving transitions remain correct when the server tables exist.
func mysqlTimeZoneTarget(zone string) (string, bool, error) {
	zone = strings.TrimSpace(zone)
	location, err := time.LoadLocation(zone)
	if err != nil {
		return "", false, fmt.Errorf("invalid report timezone %q: %w", zone, err)
	}
	start, end := time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC), time.Date(2101, 1, 1, 12, 0, 0, 0, time.UTC)
	_, offset := start.In(location).Zone()
	for instant := start.AddDate(0, 0, 1); instant.Before(end); instant = instant.AddDate(0, 0, 1) {
		if _, candidate := instant.In(location).Zone(); candidate != offset {
			return zone, false, nil
		}
	}
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return fmt.Sprintf("%s%02d:%02d", sign, offset/3600, offset%3600/60), true, nil
}
