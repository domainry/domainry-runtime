package validation

import (
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestReportExportRecordMappingRejectsEmptyUnknownStatusAndInvalidFields(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{Key: "audit", Fields: []definitionmodel.FieldSchema{
			{Key: "report_key", Type: "text"},
			{Key: "requester", Type: "user"},
			{Key: "status", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"approved", "downloaded"}}},
			{Key: "row_count", Type: "integer"}, {Key: "scope_hash", Type: "text"},
		}},
		{Key: "download", Fields: []definitionmodel.FieldSchema{
			{Key: "audit_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "other_audit"}},
			{Key: "filename", Type: "number"},
			{Key: "hash", Type: "text"},
			{Key: "expires_at", Type: "datetime"},
		}},
	}}
	state := newValidationState(manifest, nil)
	state.validateReportExportRecordMapping("controls[0].record_mapping", reportmodel.ReportExportControlSchema{
		AuditObject: "audit", DownloadObject: "download",
		RecordMapping: reportmodel.ReportExportRecordMappingSchema{
			AuditReportKeyField: "report_key", AuditRequesterField: "requester", AuditStatusField: "status",
			AuditPreparedStatuses: []string{"", "unknown"}, AuditPreparedStatus: "unknown", AuditDownloadedStatus: "unknown", AuditDeniedStatus: "unknown", AuditExpiredStatus: "unknown",
			AuditRowCountField: "row_count", AuditScopeHashField: "scope_hash",
			DownloadAuditField: "audit_id", DownloadFilenameField: "filename", DownloadContentHashField: "hash", DownloadExpiresAtField: "expires_at",
		},
	})
	got := state.errs.Error()
	for _, want := range []string{"audit_prepared_statuses[0]", "unknown status", "must have type text", "must target \"audit\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("errors=%q missing=%q", got, want)
		}
	}
}

func TestReportExportRecordMappingAllowsOpenTextStatusVocabulary(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{Key: "audit", Fields: []definitionmodel.FieldSchema{{Key: "report_key", Type: "text"}, {Key: "requester", Type: "user"}, {Key: "status", Type: "text"}, {Key: "row_count", Type: "integer"}, {Key: "scope_hash", Type: "text"}}},
		{Key: "download", Fields: []definitionmodel.FieldSchema{{Key: "audit_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "audit"}}, {Key: "filename", Type: "text"}, {Key: "hash", Type: "text"}, {Key: "expires_at", Type: "datetime"}}},
	}}
	state := newValidationState(manifest, nil)
	state.validateReportExportRecordMapping("controls[0].record_mapping", reportmodel.ReportExportControlSchema{
		AuditObject: "audit", DownloadObject: "download",
		RecordMapping: reportmodel.ReportExportRecordMappingSchema{
			AuditReportKeyField: "report_key", AuditRequesterField: "requester", AuditStatusField: "status",
			AuditPreparedStatuses: []string{"requested", "project_approved"}, AuditPreparedStatus: "project_approved", AuditDownloadedStatus: "downloaded", AuditDeniedStatus: "denied", AuditExpiredStatus: "expired",
			AuditRowCountField: "row_count", AuditScopeHashField: "scope_hash",
			DownloadAuditField: "audit_id", DownloadFilenameField: "filename", DownloadContentHashField: "hash", DownloadExpiresAtField: "expires_at",
		},
	})
	if len(state.errs) != 0 {
		t.Fatalf("open text status mapping errors=%v", state.errs)
	}
}
