package sqlite

import (
	sqldriver "database/sql/driver"
	"testing"
)

func TestExactPercentProductAndWholeUnitFloor(t *testing.T) {
	for _, test := range []struct {
		units, rate int64
		want        string
	}{
		{60000, 1000, "6000"}, {49999, 1000, "4999"}, {99999, 1000, "9999"},
		{100000, 2000, "20000"}, {149999, 2000, "29999"}, {150000, 2500, "37500"},
		{150000, 0, "0"}, {-60001, 1000, "-6001"}, {-1, 1, "-1"},
		{9007199254740993, 9999, "9006298534815518"},
	} {
		product, err := sqliteDecimalMultiplyMinor(nil, []sqldriver.Value{test.units, test.rate})
		if err != nil {
			t.Fatal(err)
		}
		got, err := sqliteDecimalFloorUnits(nil, []sqldriver.Value{product, int64(4)})
		if err != nil || got != test.want {
			t.Fatalf("units=%d rate=%d product=%v floor=%v want=%s error=%v", test.units, test.rate, product, got, test.want, err)
		}
	}
	if got, err := sqliteDecimalMultiplyMinor(nil, []sqldriver.Value{nil, int64(1000)}); err != nil || got != nil {
		t.Fatalf("null product=%v error=%v", got, err)
	}
	if got, err := sqliteDecimalFloorUnits(nil, []sqldriver.Value{nil, int64(4)}); err != nil || got != nil {
		t.Fatalf("null floor=%v error=%v", got, err)
	}
	for _, invalid := range []sqldriver.Value{float64(0.1), "1.2", "NaN"} {
		if _, err := sqliteDecimalMultiplyMinor(nil, []sqldriver.Value{int64(60000), invalid}); err == nil {
			t.Fatalf("accepted non-integer minor units %v", invalid)
		}
	}
	for _, invalidScale := range []sqldriver.Value{int64(-1), int64(39), "4", float64(4)} {
		if _, err := sqliteDecimalFloorUnits(nil, []sqldriver.Value{int64(60000000), invalidScale}); err == nil {
			t.Fatalf("accepted invalid scale %v", invalidScale)
		}
	}
}
