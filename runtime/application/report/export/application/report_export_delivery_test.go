package application

import (
	"testing"

	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
)

func TestReportExportInlineDeliveryBoundary(t *testing.T) {
	tests := []struct {
		name       string
		exactTotal int
		want       bool
	}{
		{name: "unknown stays asynchronous", exactTotal: -1, want: false},
		{name: "empty result is inline", exactTotal: 0, want: true},
		{name: "one thousand rows is inline", exactTotal: 1000, want: true},
		{name: "one thousand and one rows stays asynchronous", exactTotal: 1001, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := reportExportCanDeliverInline(reportexport.ExportPayload{ExactTotal: test.exactTotal}); got != test.want {
				t.Fatalf("reportExportCanDeliverInline(%d)=%v want=%v", test.exactTotal, got, test.want)
			}
		})
	}
}
