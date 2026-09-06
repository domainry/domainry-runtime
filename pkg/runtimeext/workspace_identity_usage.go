package runtimeext

import (
	"context"
	"strings"
)

const WorkspaceIdentityUsageMaximumPageSize = 100

// WorkspaceIdentityUsageCapability is a source-declared, read-only Handler
// grant. Runtime and Identity own the permission, installation scope, active
// Workspace/commercial join, and physical Workspace selection; project code
// controls only bounded paging.
type WorkspaceIdentityUsageCapability struct {
	MaxPageSize int
}

func (capability WorkspaceIdentityUsageCapability) Valid() bool {
	return capability.MaxPageSize > 0 && capability.MaxPageSize <= WorkspaceIdentityUsageMaximumPageSize
}

type WorkspaceIdentityUsageRequest struct {
	PageSize int
	Cursor   string
}

type WorkspaceIdentityUsageResolveRequest struct {
	WorkspaceCode             string
	ExpectedWorkspaceRevision int64
}

func (request WorkspaceIdentityUsageResolveRequest) Valid() bool {
	code := strings.TrimSpace(request.WorkspaceCode)
	return code != "" && code == request.WorkspaceCode && len(code) <= 191 && request.ExpectedWorkspaceRevision > 0
}

func (request WorkspaceIdentityUsageRequest) Valid() bool {
	return request.PageSize >= 0 && request.PageSize <= WorkspaceIdentityUsageMaximumPageSize && len(strings.TrimSpace(request.Cursor)) <= 4096
}

// WorkspaceIdentityAccountCounts is deliberately policy-neutral. Billing
// policy remains source-owned by the calling Action.
type WorkspaceIdentityAccountCounts struct {
	ActiveHumanAccounts               int64
	ActiveHumanAccountsWithActiveRole int64
	DisabledHumanAccounts             int64
	ServiceAccounts                   int64
	AutomationAccounts                int64
}

// WorkspaceCommercialTerms is the bounded installation-owned commercial
// projection needed to turn Identity account counts into an invoice fact.
// Runtime deliberately withholds billing contacts and all other provisioning
// state from project code.
type WorkspaceCommercialTerms struct {
	Plan              string
	IncludedUserLimit int64
	MaxUserLimit      int64
}

// WorkspaceCommercialConfiguration is the typed, installation-owned snapshot
// available only inside an authorized Handler invocation. Generated handlers
// cannot mutate it or use it to choose a physical Workspace scope.
type WorkspaceCommercialConfiguration struct {
	Plan                  string
	IncludedUserLimit     int64
	MaxUserLimit          int64
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
	Revision              int64
}

// WorkspaceIdentityUsageItem joins Identity's account counts to Runtime's
// active installation Workspace catalog inside the Action transaction. It
// exposes only the stable canonical code, display name, and minimum commercial
// terms; the physical Workspace ID returned by Identity never crosses this
// boundary.
type WorkspaceIdentityUsageItem struct {
	WorkspaceCode        string
	WorkspaceDisplayName string
	CommercialTerms      WorkspaceCommercialTerms
	Accounts             WorkspaceIdentityAccountCounts
}

type WorkspaceIdentityUsagePage struct {
	Items      []WorkspaceIdentityUsageItem
	NextCursor string
}

// WorkspaceIdentityUsageResolveResult is an invoice-safe current fact. The
// physical Workspace ID used for the Identity aggregate is deliberately
// absent; the stable canonical reference and top-level revision are the only
// Workspace identity visible to project code.
type WorkspaceIdentityUsageResolveResult struct {
	WorkspaceCode           string
	WorkspaceDisplayName    string
	WorkspaceStatus         string
	WorkspaceRevision       int64
	CommercialConfiguration WorkspaceCommercialConfiguration
	Accounts                WorkspaceIdentityAccountCounts
}

type WorkspaceIdentityUsageExecution interface {
	ListWorkspaceIdentityUsage(context.Context, WorkspaceIdentityUsageRequest) (WorkspaceIdentityUsagePage, error)
	ResolveWorkspaceIdentityUsage(context.Context, WorkspaceIdentityUsageResolveRequest) (WorkspaceIdentityUsageResolveResult, error)
}

func ExecuteWorkspaceIdentityUsage(ctx context.Context, execution ActionExecution, request WorkspaceIdentityUsageRequest) (WorkspaceIdentityUsagePage, error) {
	capability, ok := execution.(WorkspaceIdentityUsageExecution)
	if !ok {
		return WorkspaceIdentityUsagePage{}, &BusinessError{Code: "backend.action.workspace_identity_usage_unavailable", Message: "Runtime Workspace identity usage is unavailable"}
	}
	return capability.ListWorkspaceIdentityUsage(ctx, request)
}

func ExecuteWorkspaceIdentityUsageResolve(ctx context.Context, execution ActionExecution, request WorkspaceIdentityUsageResolveRequest) (WorkspaceIdentityUsageResolveResult, error) {
	capability, ok := execution.(WorkspaceIdentityUsageExecution)
	if !ok {
		return WorkspaceIdentityUsageResolveResult{}, &BusinessError{Code: "backend.action.workspace_identity_usage_unavailable", Message: "Runtime Workspace identity usage is unavailable"}
	}
	return capability.ResolveWorkspaceIdentityUsage(ctx, request)
}
