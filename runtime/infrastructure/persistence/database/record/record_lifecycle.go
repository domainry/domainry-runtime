package record

import (
	lifecyclecontract "github.com/domainry/domainry-lifecycle/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, objects ...definitionmodel.ObjectSchema) lifecyclecontract.OwnerLifecycleExecutor {
	specs := []lifecyclepersistence.RelationalCleanupSpec{{
		PolicyKey: "execution.idempotency_receipt.v1", Table: "record_mutation_executions", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "expires_at", StatusColumn: "status", IneligibleStatuses: []string{"pending", "processing"},
	}}
	for _, object := range objects {
		if !recordpolicy.RecordUsesSoftDelete(object) {
			continue
		}
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{
			PolicyKey: "record.object.default.v1", Table: object.Key, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "deleted_at", StatusColumn: "status", EligibleStatuses: []string{"deleted"},
			ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{{Table: "workflow_process_instances", TenantColumn: "workspace_id", ReferenceColumn: "record_id", FixedColumn: "object_key", FixedValue: object.Key}},
		})
	}
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, "record", specs...)
}
