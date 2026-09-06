package contract

import (
	"context"
	"errors"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

var (
	ErrUsageWorkspaceNotFound         = errors.New("Workspace usage scope not found")
	ErrUsageWorkspaceInactive         = errors.New("Workspace usage scope is not active")
	ErrUsageWorkspaceRevisionConflict = errors.New("Workspace usage revision conflict")
)

type Workspace struct {
	ID            string
	CanonicalCode string
	// DisplayName and commercial fields are populated by ActiveResolver for
	// the invoice-safe Identity usage join. Catalog.ListActive callers use only
	// ID and CanonicalCode for cross-Workspace record aggregation.
	DisplayName       string
	Status            string
	Revision          int64
	CommercialPlan    string
	IncludedUserLimit int64
	MaxUserLimit      int64

	IncludedCustomerLimit int64
	MaxCustomerLimit      int64
	IncludedStoreLimit    int64
	MaxStores             int64
	ContractDate          string
	BillingDay            int64
	BillingContactName    string
	BillingContactPhone   string
	BillingContactEmail   string
	BillingContactAddress string
	BillingContactNotes   string
	CommercialRevision    int64
}

type Catalog interface {
	ListActive(context.Context, principalmodel.SystemScope, int) ([]Workspace, error)
}

type ActiveResolver interface {
	ResolveActive(context.Context, principalmodel.SystemScope, []string) (map[string]Workspace, error)
}

// UsageResolver locks and resolves exactly one active installation Workspace
// from its public canonical reference inside the caller-owned Action
// transaction. The physical Workspace ID remains internal to Runtime and the
// embedded Identity aggregate.
type UsageResolver interface {
	ResolveUsageWorkspace(context.Context, principalmodel.SystemScope, string, int64) (Workspace, error)
}

type Query struct {
	Workspaces    []Workspace
	Object        definitionmodel.ObjectSchema
	Dimensions    []runtimeext.CrossWorkspaceAggregateDimension
	Measures      []runtimeext.CrossWorkspaceAggregateMeasure
	RecordQuery   recordmodel.RecordListQuery
	MaxSourceRows int
	MaxResultRows int
}

type Result struct {
	Rows           []map[string]string
	SourceRowCount int64
}

type Repository interface {
	Aggregate(context.Context, Query) (Result, error)
}

type LimitKind string

const (
	LimitWorkspaces LimitKind = "workspaces"
	LimitSourceRows LimitKind = "source_rows"
	LimitResultRows LimitKind = "result_rows"
)

type LimitExceededError struct {
	Kind           LimitKind
	Observed       int64
	SourceRowCount int64
}

func (e *LimitExceededError) Error() string {
	return "cross-Workspace aggregate limit exceeded: " + string(e.Kind)
}
