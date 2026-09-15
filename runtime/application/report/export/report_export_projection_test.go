package export

import (
	"testing"
	"time"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestDataExchangePreflightCSV(t *testing.T) {
	row := reportmodel.ReportResultRow{Dimensions: map[string]string{"status": "paid", "empty": ""}, Measures: map[string]string{"orders": "2"}}
	content, err := EncodePreflightCSV([]reportmodel.ReportResultRow{row}, []string{"status", "orders", "empty"}, map[string]bool{"status": true, "empty": true})
	if err != nil || string(content) != "status,orders,empty\n******,2,\n" {
		t.Fatalf("csv=%q err=%v", content, err)
	}
}

func TestDataExchangeTTLUsesBusinessAuthoredDuration(t *testing.T) {
	control := reportmodel.ReportExportControlSchema{DownloadTTLSeconds: int64((15 * 24 * time.Hour) / time.Second)}
	ttl, err := dataExchangeTTL(control)
	if err != nil || ttl != 15*24*time.Hour {
		t.Fatalf("ttl=%s err=%v", ttl, err)
	}
	control.DownloadTTLSeconds = 59
	if _, err = dataExchangeTTL(control); err == nil {
		t.Fatal("invalid business TTL was accepted")
	}
}
