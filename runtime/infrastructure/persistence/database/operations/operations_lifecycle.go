package operations

import (
	foundationartifact "github.com/domainry/domainry-foundation/artifact"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

func LifecycleExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore) lifecyclecontract.OwnerLifecycleExecutor {
	specs := operationsLifecycleSpecs()
	specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{PolicyKey: "operations.break_glass.v1", Table: sharedoperation.BreakGlassTableName, IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "expires_at"})
	return lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "operations", specs...)
}

func operationsLifecycleSpecs() []lifecyclepersistence.RelationalCleanupSpec {
	specs := []lifecyclepersistence.RelationalCleanupSpec{}
	for _, retentionClass := range []operationsmodel.OperationsRetentionClass{
		operationsmodel.OperationsRetentionTechnical,
		operationsmodel.OperationsRetentionLegalAudit,
	} {
		for _, scope := range []operationsmodel.OperationsExecutionScope{
			operationsmodel.OperationsExecutionWorkspace,
			operationsmodel.OperationsExecutionSystem,
		} {
			definitions := operationsLifecycleDefinitions(retentionClass, scope)
			if len(definitions) == 0 {
				continue
			}
			tenantColumn := "workspace_id"
			if scope == operationsmodel.OperationsExecutionSystem {
				tenantColumn = ""
			}
			for _, status := range []operationsmodel.OperationsStatus{
				operationsmodel.OperationsStatusSucceeded,
				operationsmodel.OperationsStatusFailed,
			} {
				specs = append(specs, lifecyclepersistence.RelationalCleanupSpec{
					PolicyKey: definitions[0].Retention.PolicyKey, Table: sharedoperation.TableName, IDColumn: "id",
					TenantColumn: tenantColumn, TimeColumn: "finished_at", StatusColumn: "status",
					EligibleStatuses: []string{string(status)}, RetentionGroup: string(status),
					AdditionalPredicate: operationsLifecyclePredicate(definitions, scope),
					ReferenceChecks: []lifecyclepersistence.RelationalReferenceCheck{
						{Table: foundationartifact.BindingTableName, TenantColumn: "workspace_id", ReferenceColumn: "resource_id", FixedColumn: "owner", FixedValue: foundationartifact.OwnerOperations},
						{Table: sharedoperation.TableName, TenantColumn: tenantColumn, ReferenceColumn: "parent_id"},
					},
				})
			}
		}
	}
	return specs
}

func operationsLifecycleDefinitions(retentionClass operationsmodel.OperationsRetentionClass, scope operationsmodel.OperationsExecutionScope) []operationsmodel.OperationsDefinition {
	result := []operationsmodel.OperationsDefinition{}
	for _, definition := range operationsprojection.OperationsDefinitions() {
		if definition.Retention.Class == retentionClass && definition.ExecutionScope == scope {
			result = append(result, definition)
		}
	}
	return result
}

func operationsLifecyclePredicate(definitions []operationsmodel.OperationsDefinition, scope operationsmodel.OperationsExecutionScope) func(string) query.Predicate {
	registered := append([]operationsmodel.OperationsDefinition(nil), definitions...)
	return func(string) query.Predicate {
		identities := make([]query.Predicate, 0, len(registered))
		for _, definition := range registered {
			identities = append(identities, query.And(query.Equal("owner", definition.Owner), query.Equal("kind", definition.Kind)))
		}
		predicate := query.Or(identities...)
		if scope == operationsmodel.OperationsExecutionSystem {
			predicate = query.And(predicate, query.Equal("workspace_id", ""), query.NotEqual("system_purpose", ""))
		}
		return predicate
	}
}
