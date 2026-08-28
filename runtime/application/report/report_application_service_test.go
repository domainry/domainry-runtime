package report

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type reportRecordExporterStub struct {
	err      error
	calls    int
	content  []byte
	filename string
}

func (s *reportRecordExporterStub) ExportReportRecords(context.Context, string, principalmodel.Principal) ([]byte, string, error) {
	s.calls++
	content, filename := s.content, s.filename
	if content == nil {
		content = []byte("id\ncustomer-1\n")
	}
	if filename == "" {
		filename = "customer.csv"
	}
	return content, filename, s.err
}

type reportAuditAppenderStub struct {
	err      error
	calls    int
	request  auditcontract.AuditAppendRequest
	requests []auditcontract.AuditAppendRequest
}

func (s *reportAuditAppenderStub) AppendAudit(_ context.Context, request auditcontract.AuditAppendRequest) error {
	s.calls++
	s.request = request
	s.requests = append(s.requests, request)
	return s.err
}

func reportApplicationFixture(records ReportRecordExporter, audit auditcontract.AuditAppender) *ReportApplicationService {
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}}}
		},
	})
	return NewReportApplicationService(ReportApplicationDependencies{Domain: domain, Records: records, Audit: audit})
}

func reportPrincipal() principalmodel.Principal {
	resources := []string{"customer", "order", "tag_assignment", "tag_definition", "sale", "ledger", "empty"}
	fields := []string{"id", "name", "status", "amount", "occurred_at", "sold_at", "order_no", "customer_name", "tag_definition_id", "stable_key", "target_id", "active", "created_at", "updated_at"}
	role := accessfixture.Bundle{Key: "report-operator", RecordScope: "all_records"}
	for _, resource := range resources {
		role.Permissions = append(role.Permissions, resource+".read", resource+".export")
		for _, field := range fields {
			role.FieldPolicies = append(role.FieldPolicies, accessfixture.FieldPolicyFixture{ObjectKey: resource, FieldKey: field, Read: true, Export: true})
		}
	}
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1", WorkspaceID: "workspace-a"}}, role)
}

func TestReportApplicationServiceExportsThenPersistsMandatoryAudit(t *testing.T) {
	records := &reportRecordExporterStub{}
	audit := &reportAuditAppenderStub{}
	service := reportApplicationFixture(records, audit)

	content, filename, err := service.ExportObject(t.Context(), "revenue", "customer", reportPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	if string(content) == "" || filename != "revenue-customer.csv" || records.calls != 1 || audit.calls != 1 {
		t.Fatalf("content=%q filename=%q record_calls=%d audit_calls=%d", content, filename, records.calls, audit.calls)
	}
	if audit.request.Event != "report_object_exported" || audit.request.ObjectKey != "customer" {
		t.Fatalf("audit request=%#v", audit.request)
	}
}

func TestReportApplicationServiceDoesNotAuditFailedExport(t *testing.T) {
	failure := errors.New("export failed")
	records := &reportRecordExporterStub{err: failure}
	audit := &reportAuditAppenderStub{}
	_, _, err := reportApplicationFixture(records, audit).ExportObject(t.Context(), "revenue", "customer", reportPrincipal())
	if !errors.Is(err, failure) || audit.calls != 0 {
		t.Fatalf("error=%v audit_calls=%d", err, audit.calls)
	}
}

func TestReportApplicationServiceFailsWhenMandatoryAuditFails(t *testing.T) {
	audit := &reportAuditAppenderStub{err: errors.New("audit unavailable")}
	_, _, err := reportApplicationFixture(&reportRecordExporterStub{}, audit).ExportObject(t.Context(), "revenue", "customer", reportPrincipal())
	if err == nil || audit.calls != 1 {
		t.Fatalf("error=%v audit_calls=%d", err, audit.calls)
	}
}

func TestReportApplicationAuthorizesWorkspaceBeforePorts(t *testing.T) {
	records := &reportRecordExporterStub{}
	audit := &reportAuditAppenderStub{}
	service := reportApplicationFixture(records, audit)
	if _, _, err := service.ExportObject(t.Context(), "revenue", "customer", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); err == nil || apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("missing workspace not rejected: %v", err)
	}
	if records.calls != 0 || audit.calls != 0 {
		t.Fatalf("report ports called before workspace authorization: records=%d audit=%d", records.calls, audit.calls)
	}
}
