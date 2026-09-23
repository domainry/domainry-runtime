package record

import (
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore, objects ...definitionmodel.ObjectSchema) lifecyclecontract.OwnerLifecycleExecutor {
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "record", recordLifecycleSpecs(objects...)...)
}

func recordLifecycleSpecs(objects ...definitionmodel.ObjectSchema) []lifecyclepersistence.RelationalCleanupSpec {
	specs := []lifecyclepersistence.RelationalCleanupSpec{{
		PolicyKey: "execution.idempotency_receipt.v1", Table: sharedoperation.TableName, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "expires_at", StatusColumn: "status", IneligibleStatuses: []string{"pending", "processing"},
		AdditionalPredicate: func(string) query.Predicate { return query.Equal("owner", "record") },
	}}
	available := make(map[string]bool, len(objects))
	for _, object := range objects {
		available[object.Key] = true
	}
	if available["record_timer"] && available["record_timer_event"] {
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{
			PolicyKey: "record.object.default.v1", Table: "record_timer", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "updated_at", StatusColumn: "status",
			EligibleStatuses: []string{"fired", "cancelled", "superseded", "failed"},
			ChildCollections: []lifecyclepersistence.RelationalChildCollection{{Table: "record_timer_event", IDColumn: "id", TenantColumn: "workspace_id", ParentColumn: "record_timer_id"}},
		})
	}
	for _, object := range objects {
		if !recordpolicy.RecordUsesSoftDelete(object) {
			continue
		}
		specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{
			PolicyKey: "record.object.default.v1", Table: object.Key, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "deleted_at", StatusColumn: "status", EligibleStatuses: []string{"deleted"},
			ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{{Table: "_workflow_process_instances", TenantColumn: "workspace_id", ReferenceColumn: "record_id", FixedColumn: "object_key", FixedValue: object.Key}},
		})
	}
	return specs
}
