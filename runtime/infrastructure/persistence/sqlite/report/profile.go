package report

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain string, _ bool) (string, error) {
	formats := map[string]string{"day": "%Y-%m-%dT00:00:00Z", "month": "%Y-%m-01T00:00:00Z", "year": "%Y-01-01T00:00:00Z"}
	if grain == "week" {
		return "strftime('%Y-%m-%dT00:00:00Z', " + value + ", '-' || ((CAST(strftime('%w', " + value + ") AS INTEGER) + 6) % 7) || ' days')", nil
	}
	if grain == "quarter" {
		return "printf('%04d-%02d-01T00:00:00Z', CAST(strftime('%Y', " + value + ") AS INTEGER), ((CAST(strftime('%m', " + value + ") AS INTEGER) - 1) / 3) * 3 + 1)", nil
	}
	return "strftime('" + formats[grain] + "', " + value + ")", nil
}
