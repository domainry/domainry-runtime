package schema

import (
	"sort"

	"github.com/domainry/domainry-foundation/schemaownership"
)

const ManagedDatabaseCohortTable = "_domainry_managed_runtime_database_cohort"

var runtimeCoreSchemaOwnership = []schemaownership.Table{
	{
		Name: "_action_assurance_grants", Owner: "runtime/action", Kind: schemaownership.KindEntity, WorkspaceScope: schemaownership.ScopeWorkspace,
		RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"id"},
		EntityIDColumn: "grant_id", EntityIDPrefix: "assure", BusinessKey: []string{"grant_id"},
		BoundedQueryPath: "workspace/grant identity, globally unique token hash, action binding and expiry/consume lookup",
		DeletionPolicy:   "consumed and expired assurance grants are physically purged after the replay-defense window",
	},
	{
		Name: "_automation_runs", Owner: "runtime/automation", Kind: schemaownership.KindEntity, WorkspaceScope: schemaownership.ScopeWorkspace,
		RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"id"},
		EntityIDColumn: "run_id", EntityIDPrefix: "autorun", BusinessKey: []string{"run_id"},
		BoundedQueryPath: "workspace/run identity, kind/idempotency identity and bounded rule, record, status or lease claims",
		DeletionPolicy:   "terminal runs follow operational retention; subject erasure redacts subject-linked trace and result state",
	},
	{
		Name: ManagedDatabaseCohortTable, Owner: "runtime/schema", Kind: schemaownership.KindInfrastructure, WorkspaceScope: schemaownership.ScopeInstallation,
		RetentionClass: schemaownership.RetentionInstallation, PrimaryKey: []string{"id"},
		BoundedQueryPath: "fixed marker_id=1 lookup for the managed physical database installation identity",
		DeletionPolicy:   "the marker lives for the database lifetime and is removed only when the managed database is destroyed",
	},
	{
		Name: "_project_model_state", Owner: "runtime/application", Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeInstallation,
		RetentionClass: schemaownership.RetentionInstallation, PrimaryKey: []string{"id"},
		BoundedQueryPath: "fixed state_key=current lookup for the one installed project model state",
		DeletionPolicy:   "the row is replaced only by recreating the pre-release database; model hash drift fails startup",
	},
	{
		Name: "_publication_outbox", Owner: "runtime/publication", Kind: schemaownership.KindEntity, WorkspaceScope: schemaownership.ScopeWorkspace,
		RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"id"},
		EntityIDColumn: "message_id", EntityIDPrefix: "pubmsg", BusinessKey: []string{"message_id"},
		BoundedQueryPath: "workspace/outbox identity, destination/deduplication identity and bounded status/due/lease worker claims",
		DeletionPolicy:   "terminal publications follow replay retention; subject erasure redacts payloads after active work is fenced",
	},
	{
		Name: "_rate_limit_buckets", Owner: "runtime/rate-limit", Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeExplicitMixed,
		RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"id"},
		BoundedQueryPath: "exact canonical bucket key for atomic window increment and expiry replacement",
		DeletionPolicy:   "expired buckets are overwritten or physically purged after their enforcement window",
	},
	{
		Name: "_record_localized_values", Owner: "runtime/record", Kind: schemaownership.KindDependent, WorkspaceScope: schemaownership.ScopeWorkspace,
		RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"id"},
		BoundedQueryPath: "workspace/object/record/field/locale identity and bounded locale-aware record projection index",
		DeletionPolicy:   "record deletion and subject erasure physically remove localized values with their owning business record",
	},
	{
		Name: "_release_cohorts", Owner: "runtime/release", Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeInstallation,
		RetentionClass: schemaownership.RetentionInstallation, PrimaryKey: []string{"id"},
		BoundedQueryPath: "exact release cohort key and current generation/revision lookup",
		DeletionPolicy:   "superseded coordination state is removed after every live instance leaves its generation",
	},
	{
		Name: "_release_instances", Owner: "runtime/release", Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeInstallation,
		RetentionClass: schemaownership.RetentionTechnicalTTL, PrimaryKey: []string{"id"},
		BoundedQueryPath: "exact instance identity plus bounded generation/cohort and lease-expiry scans",
		DeletionPolicy:   "expired instance leases are physically purged after coordinated-release reconciliation",
	},
	{
		Name: "_workspaces", Owner: "runtime/workspace", Kind: schemaownership.KindEntity, WorkspaceScope: schemaownership.ScopeInstallation,
		RetentionClass: schemaownership.RetentionProduct, PrimaryKey: []string{"id"},
		EntityIDColumn: "workspace_id", EntityIDPrefix: "ws", BusinessKey: []string{"workspace_id"},
		BoundedQueryPath: "exact workspace identity, canonical code and installation/company authority lookups",
		DeletionPolicy:   "workspace retirement fences writes and deletes or archives dependent Workspace data before removing the catalog row",
	},
}

func RuntimeCoreSchemaOwnership() []schemaownership.Table {
	return schemaownership.Clone(runtimeCoreSchemaOwnership)
}

func RuntimeSchemaOwnership() []schemaownership.Table {
	tables := append(RuntimeCoreSchemaOwnership(), WorkflowSchemaOwnership()...)
	sort.Slice(tables, func(left, right int) bool { return tables[left].Name < tables[right].Name })
	return tables
}

func RuntimeOwnedTables() []string {
	return schemaownership.Names(RuntimeSchemaOwnership())
}
