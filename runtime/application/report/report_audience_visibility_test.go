package report

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadataservice "github.com/domainry/domainry-runtime/runtime/domain/metadata/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

func TestReportDirectKeyEntrypointsConcealDefinitionWithoutRequiredPermission(t *testing.T) {
	reportDefinition := reportmodel.ReportSchema{
		Key:                 "product_sales",
		RequiredPermissions: []string{"order_line.read"},
		Dataset: reportmodel.ReportDatasetSchema{
			Source: reportmodel.ReportDatasetSource{ObjectKey: "order_line", Alias: "order_line"},
		},
	}
	snapshot := metadatamodel.MetadataSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "order_line"}},
		Reports: []reportmodel.ReportSchema{reportDefinition},
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "cashier-1", WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Key: "cashier", Permissions: []string{"checkout.read"}, RecordScope: "all_records",
	},
	)
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(_ context.Context, candidate principalmodel.Principal) []reportmodel.ReportSchema {
			return metadataservice.SnapshotForPrincipal(snapshot, candidate).Reports
		},
	})
	records := &reportRecordExporterStub{}
	service := NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: records,
		ExportRecords: &reportExportStoreStub{}, ExportArtifacts: &reportExportArtifactStoreStub{},
	})

	if _, err := service.QueryObjectSQL(t.Context(), reportDefinition.Key, nil, principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("query error=%v", err)
	}
	if _, err := service.Summary(t.Context(), reportDefinition.Key, principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("summary error=%v", err)
	}
	if _, _, err := service.ExportObject(t.Context(), reportDefinition.Key, "order_line", principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("export error=%v", err)
	}
	if _, err := service.PrepareExport(t.Context(), reportDefinition.Key, "order_line", "audit-1", "export-1", principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("prepare export error=%v", err)
	}
	if records.calls != 0 {
		t.Fatalf("concealed report reached export record port: calls=%d", records.calls)
	}
}
