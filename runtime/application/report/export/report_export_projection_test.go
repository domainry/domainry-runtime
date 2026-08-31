package export

import (
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestDataExchangePreflightCSV(t *testing.T) {
	row := reportmodel.ReportResultRow{Dimensions: map[string]string{"status": "paid", "empty": ""}, Measures: map[string]string{"orders": "2"}}
	content, err := EncodePreflightCSV([]reportmodel.ReportResultRow{row}, []string{"status", "orders", "empty"}, map[string]bool{"status": true, "empty": true})
	if err != nil || string(content) != "status,orders,empty\n******,2,\n" {
		t.Fatalf("csv=%q err=%v", content, err)
	}
}
