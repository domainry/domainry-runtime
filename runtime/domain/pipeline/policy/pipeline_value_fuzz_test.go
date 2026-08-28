package policy

import (
	"strconv"
	"testing"
)

func FuzzPipelineIntValueDecimalRoundTrip(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(-1))
	f.Add(int64(9223372036854775807))
	f.Fuzz(func(t *testing.T, value int64) {
		fromInteger, integerOK := PipelineIntValue(value)
		fromString, stringOK := PipelineIntValue(strconv.FormatInt(value, 10))
		if !integerOK || !stringOK || fromInteger != fromString {
			t.Fatalf("integer/string round-trip differs: %d -> %d/%v, %d/%v", value, fromInteger, integerOK, fromString, stringOK)
		}
	})
}
