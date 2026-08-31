package report

import "strings"

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain, timeZone string, _ bool) (string, error) {
	return "runtime_date_bucket(" + value + ", '" + grain + "', '" + strings.ReplaceAll(timeZone, "'", "''") + "')", nil
}
