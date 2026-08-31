package report

import "strings"

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain, timeZone string, _ bool) (string, error) {
	if timeZone != "UTC" {
		value = "CONVERT_TZ(" + value + ", '+00:00', '" + strings.ReplaceAll(timeZone, "'", "''") + "')"
	}
	formats := map[string]string{"hour": "%Y-%m-%d %H:00:00", "day": "%Y-%m-%d 00:00:00", "week": "%x-%v-1 00:00:00", "month": "%Y-%m-01 00:00:00", "year": "%Y-01-01 00:00:00"}
	if grain == "quarter" {
		return "STR_TO_DATE(CONCAT(YEAR(" + value + "), '-', LPAD(((QUARTER(" + value + ") - 1) * 3) + 1, 2, '0'), '-01'), '%Y-%m-%d')", nil
	}
	return "DATE_FORMAT(" + value + ", '" + formats[grain] + "')", nil
}
