package report

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
)

type reportAudienceExportStore struct{}

func (*reportAudienceExportStore) GetReportRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{}, nil
}
func (*reportAudienceExportStore) ListReportRecordsForPrincipal(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{}, nil
}
func (*reportAudienceExportStore) CreateReportRecord(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{}, nil
}
func (*reportAudienceExportStore) UpdateReportRecord(context.Context, string, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{}, nil
}
func (*reportAudienceExportStore) TransitionReportExportAuditStatus(context.Context, string, string, string, string, string, string) error {
	return nil
}

func TestReportDirectKeyEntrypointsConcealDefinitionWithoutRequiredPermission(t *testing.T) {
	reportDefinition := reportmodel.ReportSchema{
		Key:                 "product_sales",
		RequiredPermissions: []string{"order_line.read"},
		Dataset: reportmodel.ReportDatasetSchema{
			Source: reportmodel.ReportDatasetSource{ObjectKey: "order_line", Alias: "order_line"},
		},
	}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "order_line"}},
		Reports: []reportmodel.ReportSchema{reportDefinition},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "cashier-1", WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Key: "cashier", Permissions: []string{"checkout.read"}, RecordScope: "all_records",
	},
	)
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(_ context.Context, candidate principalmodel.Principal) []reportmodel.ReportSchema {
			return appschemaservice.SnapshotForPrincipal(snapshot, candidate).Reports
		},
	})
	service := NewReportApplicationService(ReportApplicationDependencies{
		Domain:        domain,
		ExportRecords: &reportAudienceExportStore{},
	})

	if _, err := service.QueryObjectSQL(t.Context(), reportDefinition.Key, nil, principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("query error=%v", err)
	}
	if _, err := service.Summary(t.Context(), reportDefinition.Key, principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("summary error=%v", err)
	}
	if _, err := service.PrepareExportRouted(t.Context(), reportDefinition.Key, "order_line", "audit-1", "export-1", reportmodel.ReportExportScopeRequest{Purpose: "test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}, principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("prepare export error=%v", err)
	}
}
