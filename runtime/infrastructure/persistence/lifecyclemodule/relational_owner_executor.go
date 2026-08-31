package lifecyclemodule

import (
	"context"
	"fmt"
	"time"

	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-lifecycle-sdk/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/query"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type cleanupSpec struct {
	policyKey           string
	table               string
	idColumn            string
	tenantColumn        string
	timeColumn          string
	statusColumn        string
	ineligibleStatuses  []string
	eligibleStatuses    []string
	retentionGroup      string
	referenceChecks     []cleanupReferenceCheck
	childCollections    []relationalChildCollection
	additionalPredicate func(string) ormbuilder.Predicate
	unixNanoTime        bool
}

type cleanupReferenceCheck struct {
	table, tenantColumn, referenceColumn string
	fixedColumn, fixedValue              string
}

type relationalChildCollection struct {
	table, idColumn, tenantColumn, parentColumn string
}

// RelationalCleanupSpec is declared by the source owner of a table. Lifecycle
// supplies the orchestration engine but does not own or discover business DDL.
type RelationalCleanupSpec struct {
	PolicyKey           string
	Table               string
	IDColumn            string
	TenantColumn        string
	TimeColumn          string
	StatusColumn        string
	IneligibleStatuses  []string
	EligibleStatuses    []string
	RetentionGroup      string
	ReferenceChecks     []RelationalReferenceCheck
	ChildCollections    []RelationalChildCollection
	AdditionalPredicate func(string) ormbuilder.Predicate
	UnixNanoTime        bool
}

type RelationalReferenceCheck struct {
	Table           string
	TenantColumn    string
	ReferenceColumn string
	FixedColumn     string
	FixedValue      string
}

type RelationalChildCollection struct {
	Table        string
	IDColumn     string
	TenantColumn string
	ParentColumn string
}

type OwnerExecutor struct {
	db       modulehost.Database
	renderer modulehost.Dialect
	archives lifecyclecontract.ArchiveStore
	owner    string
	specs    []cleanupSpec
}

// NewRelationalOwnerExecutor creates a cleanup adapter from source-owned table
// declarations. Callers should keep these declarations in the owning module.
func NewRelationalOwnerExecutor(store *database.RuntimeStore, archives lifecyclecontract.ArchiveStore, owner string, specs ...RelationalCleanupSpec) lifecyclecontract.OwnerLifecycleExecutor {
	internal := make([]cleanupSpec, 0, len(specs))
	for _, spec := range specs {
		references := make([]cleanupReferenceCheck, 0, len(spec.ReferenceChecks))
		for _, reference := range spec.ReferenceChecks {
			references = append(references, cleanupReferenceCheck{
				table: reference.Table, tenantColumn: reference.TenantColumn, referenceColumn: reference.ReferenceColumn,
				fixedColumn: reference.FixedColumn, fixedValue: reference.FixedValue,
			})
		}
		children := make([]relationalChildCollection, 0, len(spec.ChildCollections))
		for _, child := range spec.ChildCollections {
			children = append(children, relationalChildCollection{table: child.Table, idColumn: child.IDColumn, tenantColumn: child.TenantColumn, parentColumn: child.ParentColumn})
		}
		internal = append(internal, cleanupSpec{
			policyKey: spec.PolicyKey, table: spec.Table, idColumn: spec.IDColumn, tenantColumn: spec.TenantColumn,
			timeColumn: spec.TimeColumn, statusColumn: spec.StatusColumn, ineligibleStatuses: append([]string(nil), spec.IneligibleStatuses...),
			eligibleStatuses: append([]string(nil), spec.EligibleStatuses...), retentionGroup: spec.RetentionGroup,
			referenceChecks: references, childCollections: children,
			additionalPredicate: spec.AdditionalPredicate, unixNanoTime: spec.UnixNanoTime,
		})
	}
	if store == nil {
		return OwnerExecutor{archives: archives, owner: owner, specs: internal}
	}
	return OwnerExecutor{db: store.DB(), renderer: store.RuntimeRenderer(), archives: archives, owner: owner, specs: internal}
}

func (e OwnerExecutor) database(ctx context.Context) modulehost.DBTX {
	return modulehost.ExecutorFromContext(ctx, e.db)
}

func (e OwnerExecutor) Owner(context.Context) string { return e.owner }

func (e OwnerExecutor) Preview(ctx context.Context, workspaceID string, policy lifecyclemodel.PolicyVersion, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	specs := e.policySpecs(policy.Policy.Key)
	if len(specs) == 0 {
		return lifecyclecontract.CleanupPreview{}, fmt.Errorf("no cleanup specification for policy %s", policy.Policy.Key)
	}
	preview := lifecyclecontract.CleanupPreview{}
	for _, spec := range specs {
		if spec.tenantColumn == "" && workspaceID != principalmodel.InstallationWorkspaceID {
			return lifecyclecontract.CleanupPreview{}, fmt.Errorf("policy %s is installation scoped", policy.Policy.Key)
		}
		cutoff := now.Add(-lifecycleSpecRetention(policy.Policy, spec))
		count, oldest, err := e.previewSpec(ctx, workspaceID, spec, policy.Policy.Key, cutoff)
		if err != nil {
			return lifecyclecontract.CleanupPreview{}, err
		}
		preview.Rows += count
		if !oldest.IsZero() && (preview.OldestEligible.IsZero() || oldest.Before(preview.OldestEligible)) {
			preview.OldestEligible = oldest
		}
	}
	preview.Bytes = preview.Rows * 1024
	return preview, nil
}

func (e OwnerExecutor) ProcessBatch(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, batchSize int) (lifecyclemodel.CleanupBatchResult, error) {
	specs := e.policySpecs(policy.Policy.Key)
	if len(specs) == 0 {
		return lifecyclemodel.CleanupBatchResult{}, fmt.Errorf("no cleanup specification for policy %s", policy.Policy.Key)
	}
	result := lifecyclemodel.CleanupBatchResult{Done: true}
	remaining := batchSize
	for _, spec := range specs {
		if spec.tenantColumn == "" && job.WorkspaceID != principalmodel.InstallationWorkspaceID {
			return result, fmt.Errorf("policy %s is installation scoped", policy.Policy.Key)
		}
		if remaining <= 0 {
			result.Done = false
			break
		}
		cutoff := job.UpdatedAt.Add(-lifecycleSpecRetention(policy.Policy, spec))
		batch, err := e.processSpec(ctx, job, policy, spec, holds, cutoff, remaining)
		if err != nil {
			return result, err
		}
		result.Scanned += batch.Scanned
		result.Archived += batch.Archived
		result.Purged += batch.Purged
		result.Skipped += batch.Skipped
		result.Failed += batch.Failed
		if batch.Checkpoint != "" {
			result.Checkpoint = batch.Checkpoint
		}
		if !batch.OldestEligible.IsZero() && (result.OldestEligible.IsZero() || batch.OldestEligible.Before(result.OldestEligible)) {
			result.OldestEligible = batch.OldestEligible
		}
		remaining -= int(batch.Scanned)
		if !batch.Done {
			result.Done = false
		}
	}
	return result, nil
}

func (e OwnerExecutor) policySpecs(policyKey string) []cleanupSpec {
	result := []cleanupSpec{}
	for _, spec := range e.specs {
		if spec.policyKey == policyKey {
			result = append(result, spec)
		}
	}
	return result
}

func cleanupPredicate(spec cleanupSpec, cutoff time.Time, outerAlias string) ormbuilder.Predicate {
	predicates := []ormbuilder.Predicate{}
	cutoffValue := any(lifecycleTime(cutoff))
	if spec.unixNanoTime {
		cutoffValue = cutoff.UTC().UnixNano()
	} else {
		predicates = append(predicates, ormbuilder.NotEqual(spec.timeColumn, ""))
	}
	predicates = append(predicates, ormbuilder.LessThanOrEqual(spec.timeColumn, cutoffValue))
	if spec.statusColumn != "" && len(spec.ineligibleStatuses) > 0 {
		values := make([]any, 0, len(spec.ineligibleStatuses))
		for _, status := range spec.ineligibleStatuses {
			values = append(values, status)
		}
		predicates = append(predicates, ormbuilder.NotIn(spec.statusColumn, values...))
	}
	if spec.statusColumn != "" && len(spec.eligibleStatuses) > 0 {
		values := make([]any, 0, len(spec.eligibleStatuses))
		for _, status := range spec.eligibleStatuses {
			values = append(values, status)
		}
		predicates = append(predicates, ormbuilder.In(spec.statusColumn, values...))
	}
	if spec.additionalPredicate != nil {
		predicates = append(predicates, spec.additionalPredicate(outerAlias))
	}
	return ormbuilder.And(predicates...)
}

func lifecycleTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func lifecycleSpecRetention(policy lifecyclemodel.RetentionPolicy, spec cleanupSpec) time.Duration {
	if retention := policy.StatusRetention[spec.retentionGroup]; spec.retentionGroup != "" && retention > 0 {
		return retention
	}
	return policy.DefaultRetention
}
