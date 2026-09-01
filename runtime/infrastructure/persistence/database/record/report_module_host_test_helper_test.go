package record_test

import (
	"testing"

	reportsdkcontract "github.com/domainry/domainry-report-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

func readAuthorizedReportSources(t *testing.T, report reportmodel.ReportSchema, principal principalmodel.Principal, access reportcontract.ReportRecordAccess, records reportcontract.ReportRecordReader) reportmodel.ReportDatasetReadResult {
	t.Helper()
	plan, err := reportsdkcontract.BuildReportDatasetPlan(report)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := reportadapter.ReportAuthorityFromRuntimePrincipal(principal)
	if err != nil || authority.Subject == nil {
		t.Fatalf("Report authority=%#v err=%v", authority, err)
	}
	host := reportadapter.NewReportModuleQueryHost(reportadapter.ReportModuleQueryHostDependencies{Access: access, Records: records})
	result, err := host.ReadReportDataset(t.Context(), reportmodel.ReportDatasetReadRequest{Report: report, Plan: plan, Subject: *authority.Subject})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
