package report

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

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

func reportApplicationFixture(audit auditcontract.AuditAppender) *ReportApplicationService {
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}}}
		},
	})
	return NewReportApplicationService(ReportApplicationDependencies{Domain: domain, Audit: audit})
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
