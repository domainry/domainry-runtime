package report

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore, objects ...definitionmodel.ObjectSchema) lifecyclecontract.OwnerLifecycleExecutor {
	available := make(map[string]bool, len(objects))
	for _, object := range objects {
		available[object.Key] = true
	}
	specs := []lifecyclepersistence.RelationalCleanupSpec{}
	if available["download_task"] {
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "report.download.v1", Table: "download_task", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"expired", "failed", "cancelled"}})
	}
	if available["report_export_audit"] {
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "report.export.v1", Table: "report_export_audit", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"completed", "failed", "cancelled"}, RetentionGroup: "succeeded", ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{{Table: "download_task", TenantColumn: "workspace_id", ReferenceColumn: "report_export_audit"}}})
	}
	if available["report_query_run"] {
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "report.export.v1", Table: "report_query_run", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status", EligibleStatuses: []string{"completed", "failed", "cancelled"}, RetentionGroup: "succeeded", ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{{Table: "report_export_audit", TenantColumn: "workspace_id", ReferenceColumn: "report_query_run"}}})
	}
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "report", specs...)
}
