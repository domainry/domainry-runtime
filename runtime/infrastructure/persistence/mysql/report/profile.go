package report

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain string, _ bool) (string, error) {
	formats := map[string]string{"day": "%Y-%m-%d 00:00:00", "week": "%x-%v-1 00:00:00", "month": "%Y-%m-01 00:00:00", "year": "%Y-01-01 00:00:00"}
	if grain == "quarter" {
		return "STR_TO_DATE(CONCAT(YEAR(" + value + "), '-', LPAD(((QUARTER(" + value + ") - 1) * 3) + 1, 2, '0'), '-01'), '%Y-%m-%d')", nil
	}
	return "DATE_FORMAT(" + value + ", '" + formats[grain] + "')", nil
}
