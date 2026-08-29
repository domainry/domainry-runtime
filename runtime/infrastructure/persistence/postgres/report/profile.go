package report

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) DateBucket(value, grain string, date bool) (string, error) {
	castType := "TIMESTAMPTZ"
	if date {
		castType = "DATE"
	}
	return "DATE_TRUNC('" + grain + "', CAST(" + value + " AS " + castType + "))", nil
}
