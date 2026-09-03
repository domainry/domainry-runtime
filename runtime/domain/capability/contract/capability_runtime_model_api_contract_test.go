package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeModelAPIContractStartsWithACompactSelectionIndex(t *testing.T) {
	index := RuntimeModelAPIContractIndex()
	transportBytes := len(RuntimeAPIContractDocument())
	if len(index)*4 >= transportBytes {
		t.Fatalf("model index is not compact enough: index=%d transport=%d", len(index), transportBytes)
	}
	t.Logf("Runtime API disclosure bytes: transport=%d model_index=%d reduction=%.1f%%", transportBytes, len(index), float64(transportBytes-len(index))*100/float64(transportBytes))
	text := string(index)
	for _, forbidden := range []string{`"method"`, `"path"`, `"202"`, `idempotency`, `sse`, `download`, `portal_notification_`, `business_notification_`} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Errorf("model index still discloses transport concern %q", forbidden)
		}
	}
	for _, required := range []string{`"notification_list"`, `"record_export"`, `"workflow_run"`} {
		if !strings.Contains(text, required) {
			t.Errorf("model index does not expose semantic operation %s", required)
		}
	}
}

func TestRuntimeModelAPIRecordExportHidesAutomaticDeliveryMechanics(t *testing.T) {
	payload, err := RuntimeModelAPIContractProjection("record_export")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("record export semantic projection bytes=%d", len(payload))
	var document runtimeModelAPIProjection
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	operation, exists := document.Operations["record_export"]
	if !exists || operation.Domain != "records" || operation.ResultSchema != "file" {
		t.Fatalf("record export semantic operation=%+v exists=%v", operation, exists)
	}
	if len(document.Schemas) != 0 {
		t.Fatalf("file delivery must not pull transport schemas into model context: %v", document.Schemas)
	}
	text := strings.ToLower(string(payload))
	for _, forbidden := range []string{"server_selected", "record_batch_job", "record_export_download", `"202"`, `"method"`, `"path"`, "idempotency", "sse"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("record export projection still discloses %q", forbidden)
		}
	}
}

func TestRuntimeModelAPINormalizesFrontendSurfacesAndClosesSelectedSchemas(t *testing.T) {
	payload, err := RuntimeModelAPIContractProjection("notification_list")
	if err != nil {
		t.Fatal(err)
	}
	var document runtimeModelAPIProjection
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Operations) != 1 || document.Operations["notification_list"].ResultSchema != "notification_page" {
		t.Fatalf("selected operations=%+v", document.Operations)
	}
	for _, schema := range []string{"notification_page", "notification"} {
		if len(document.Schemas[schema]) == 0 {
			t.Errorf("selected notification schema closure is missing %q", schema)
		}
	}
	text := strings.ToLower(string(payload))
	for _, forbidden := range []string{"portal", `"surface"`, "sse", "last-event-id"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("notification projection still discloses frontend/transport concern %q", forbidden)
		}
	}
}

func TestRuntimeModelAPIProjectionRejectsUnselectedTransportOperations(t *testing.T) {
	for _, key := range []string{"record_export_download", "business_notification_stream", "missing"} {
		if _, err := RuntimeModelAPIContractProjection(key); err == nil {
			t.Errorf("transport or unknown operation %q was accepted", key)
		}
	}
}
