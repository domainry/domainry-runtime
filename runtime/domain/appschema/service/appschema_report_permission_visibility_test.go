package service

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestSnapshotReportPermissionAndObjectVisibilityIntersection(t *testing.T) {
	orderLine := definitionmodel.ObjectSchema{Key: "order_line"}
	report := func(key string, permissions []string) reportmodel.ReportSchema {
		return reportmodel.ReportSchema{
			Key:                 key,
			ObjectSQLV1:         &reportmodel.ReportObjectSQLSchema{SQL: "SELECT COUNT(*) AS record_count FROM order_line source LIMIT 1"},
			RequiredPermissions: permissions,
		}
	}
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{orderLine},
		Reports: []reportmodel.ReportSchema{
			report("order_line_permission", []string{"order_line.read"}),
			report("finance_permission", []string{"finance.read"}),
			report("object_visibility", nil),
			{Key: "invalid", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{SQL: "not sql"}, RequiredPermissions: []string{"order_line.read"}},
			{Key: "global"},
		},
	}
	principal := func(permissions ...string) principalmodel.Principal {
		return accessfixture.Attach(
			principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}},
			accessfixture.Bundle{Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)},
		)
	}

	assertReportKeys(t, reportKeys(SnapshotForPrincipal(snapshot, principal("order_line.read")).Reports), "order_line_permission", "object_visibility", "global")
	assertReportKeys(t, reportKeys(SnapshotForPrincipal(snapshot, principal()).Reports), "global")
	assertReportKeys(t, reportKeys(SnapshotForPrincipal(snapshot, principal("runtime.appschema.validate_application_definition")).Reports), "global")
	assertReportKeys(t, reportKeys(SnapshotForPrincipal(snapshot, principalmodel.Principal{}).Reports))
}

func reportKeys(reports []reportmodel.ReportSchema) []string {
	keys := make([]string, 0, len(reports))
	for _, report := range reports {
		keys = append(keys, report.Key)
	}
	return keys
}

func assertReportKeys(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("report keys=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("report keys=%v want=%v", got, want)
		}
	}
}
