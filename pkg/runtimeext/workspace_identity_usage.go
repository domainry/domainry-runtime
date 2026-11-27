package runtimeext

import (
	"context"
	"strings"
)

const WorkspaceIdentityUsageMaximumPageSize = 100

// WorkspaceIdentityUsageCapability is a source-declared, read-only Handler
// grant. Runtime and Identity own the permission, installation scope, active
// Workspace join, and physical Workspace selection; project code controls only
// bounded paging.
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

// WorkspaceIdentityAccountCounts is deliberately policy-neutral.
type WorkspaceIdentityAccountCounts struct {
	ActiveHumanAccounts               int64
	ActiveHumanAccountsWithActiveRole int64
	DisabledHumanAccounts             int64
	ServiceAccounts                   int64
	AutomationAccounts                int64
}

// WorkspaceIdentityUsageItem joins Identity's account counts to Runtime's
// active installation Workspace catalog inside the Action transaction. It
// exposes only the stable canonical code and display name; the physical
// Workspace ID returned by Identity never crosses this boundary.
type WorkspaceIdentityUsageItem struct {
	WorkspaceCode        string
	WorkspaceDisplayName string
	Accounts             WorkspaceIdentityAccountCounts
}

type WorkspaceIdentityUsagePage struct {
	Items      []WorkspaceIdentityUsageItem
	NextCursor string
}

// WorkspaceIdentityUsageResolveResult is a policy-neutral current fact. The
// physical Workspace ID used for the Identity aggregate is deliberately
// absent; the stable canonical reference and top-level revision are the only
// Workspace identity visible to project code.
type WorkspaceIdentityUsageResolveResult struct {
	WorkspaceCode        string
	WorkspaceDisplayName string
	WorkspaceStatus      string
	WorkspaceRevision    int64
	Accounts             WorkspaceIdentityAccountCounts
}

type WorkspaceIdentityUsageExecution interface {
	ListWorkspaceIdentityUsage(context.Context, WorkspaceIdentityUsageRequest) (WorkspaceIdentityUsagePage, error)
	ResolveWorkspaceIdentityUsage(context.Context, WorkspaceIdentityUsageResolveRequest) (WorkspaceIdentityUsageResolveResult, error)
}

func ExecuteWorkspaceIdentityUsage(ctx context.Context, execution ActionExecution, request WorkspaceIdentityUsageRequest) (WorkspaceIdentityUsagePage, error) {
	capability, ok := execution.(WorkspaceIdentityUsageExecution)
	if !ok {
		return WorkspaceIdentityUsagePage{}, &BusinessError{Class: BusinessErrorInternal, Code: "backend.action.workspace_identity_usage_unavailable", Message: "Runtime Workspace identity usage is unavailable"}
	}
	return capability.ListWorkspaceIdentityUsage(ctx, request)
}

func ExecuteWorkspaceIdentityUsageResolve(ctx context.Context, execution ActionExecution, request WorkspaceIdentityUsageResolveRequest) (WorkspaceIdentityUsageResolveResult, error) {
	capability, ok := execution.(WorkspaceIdentityUsageExecution)
	if !ok {
		return WorkspaceIdentityUsageResolveResult{}, &BusinessError{Class: BusinessErrorInternal, Code: "backend.action.workspace_identity_usage_unavailable", Message: "Runtime Workspace identity usage is unavailable"}
	}
	return capability.ResolveWorkspaceIdentityUsage(ctx, request)
}
