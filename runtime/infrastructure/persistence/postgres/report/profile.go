package report

import "strings"

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain, timeZone string, date bool) (string, error) {
	castType := "TIMESTAMPTZ"
	if date {
		castType = "DATE"
	}
	zoned := "CAST(" + value + " AS " + castType + ")"
	if timeZone != "UTC" {
		zoned += " AT TIME ZONE '" + strings.ReplaceAll(timeZone, "'", "''") + "'"
	}
	return "DATE_TRUNC('" + grain + "', " + zoned + ")", nil
}
