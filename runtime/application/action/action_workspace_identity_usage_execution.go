package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	aggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
)

const workspaceIdentityUsageAuditTimeout = 2 * time.Second

type WorkspaceIdentityUsageAudit struct {
	ActionKey      string
	Outcome        string
	ErrorCode      string
	ScopeSHA256    string
	WorkspaceCount int
	Principal      principalmodel.Principal
}

func (e *businessActionExecution) ResolveWorkspaceIdentityUsage(ctx context.Context, request runtimeext.WorkspaceIdentityUsageResolveRequest) (runtimeext.WorkspaceIdentityUsageResolveResult, error) {
	auditValue := WorkspaceIdentityUsageAudit{}
	auditCtx := ctx
	if e != nil {
		auditValue.ActionKey, auditValue.Principal = strings.TrimSpace(e.action.Key), e.invocation.Principal
	}
	fail := func(kind apperror.ErrorKind, code, outcome string, cause error) (runtimeext.WorkspaceIdentityUsageResolveResult, error) {
		auditValue.Outcome, auditValue.ErrorCode = outcome, code
		if auditErr := e.auditWorkspaceIdentityUsage(auditCtx, auditValue); auditErr != nil {
			return runtimeext.WorkspaceIdentityUsageResolveResult{}, apperror.New(apperror.KindInternal, "backend.action.workspace_identity_usage_audit_failed", auditErr, nil)
		}
		return runtimeext.WorkspaceIdentityUsageResolveResult{}, apperror.New(kind, code, cause, nil)
	}
	if e == nil || e.unitOfWork == nil || e.workspaceUsageGrant == nil {
		return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_grant_denied", "denied", nil)
	}
	phase := e.Phase()
	if phase != runtimeext.ExecutionPhasePrewrite && phase != runtimeext.ExecutionPhaseWriting {
		return fail(apperror.KindConflict, "backend.action.workspace_identity_usage_phase_forbidden", "denied", nil)
	}
	if !ActionAllowed(e.invocation.Principal, e.action) {
		return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_action_denied", "denied", nil)
	}
	if !request.Valid() {
		return fail(apperror.KindBadRequest, "backend.action.workspace_identity_usage_resolve_request_invalid", "denied", nil)
	}
	if e.dependencies.WorkspaceUsageResolver == nil || e.dependencies.BindWorkspaceIdentityUsage == nil || e.dependencies.AuthorizeWorkspaceIdentityUsage == nil {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_unavailable", "error", nil)
	}
	authorization, err := e.authorizeWorkspaceIdentityUsage(ctx)
	if err != nil {
		return fail(apperror.KindOf(err), apperror.CodeOf(err), workspaceIdentityUsageErrorOutcome(err), err)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_transaction_required", "error", err)
	}
	auditCtx = txCtx
	usage, err := e.bindWorkspaceIdentityUsage(txCtx)
	if err != nil {
		return fail(apperror.KindOf(err), apperror.CodeOf(err), workspaceIdentityUsageErrorOutcome(err), err)
	}
	workspace, err := e.dependencies.WorkspaceUsageResolver.ResolveUsageWorkspace(
		txCtx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve exact Workspace billing facts"),
		request.WorkspaceCode, request.ExpectedWorkspaceRevision,
	)
	if err != nil {
		switch {
		case errors.Is(err, aggregatecontract.ErrUsageWorkspaceNotFound):
			return fail(apperror.KindNotFound, "backend.action.workspace_identity_usage_workspace_not_found", "denied", err)
		case errors.Is(err, aggregatecontract.ErrUsageWorkspaceInactive):
			return fail(apperror.KindConflict, "backend.action.workspace_identity_usage_workspace_inactive", "denied", err)
		case errors.Is(err, aggregatecontract.ErrUsageWorkspaceRevisionConflict):
			return fail(apperror.KindConflict, "backend.action.workspace_identity_usage_revision_conflict", "denied", err)
		default:
			return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_catalog_failed", "error", err)
		}
	}
	identityUsage, err := usage.ResolveWorkspaceIdentityUsage(txCtx, identitysdk.WorkspaceIdentityUsageResolveRequest{
		ContractVersion: identitysdk.CurrentWorkspaceIdentityUsageContractVersion,
		ContractHash:    identitysdk.CurrentWorkspaceIdentityUsageContractHash,
		Authorization:   authorization,
		WorkspaceCode:   request.WorkspaceCode,
	})
	if err != nil {
		normalized := normalizeIdentityCapabilityError(err, "identity.workspace_identity_usage.resolve_failed")
		return fail(apperror.KindOf(normalized), apperror.CodeOf(normalized), workspaceIdentityUsageErrorOutcome(normalized), normalized)
	}
	physicalID := strings.TrimSpace(identityUsage.WorkspaceID)
	if physicalID == "" || !validWorkspaceIdentityCounts(identityUsage.Accounts) {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_result_invalid", "error", nil)
	}
	auditValue.WorkspaceCount, auditValue.ScopeSHA256 = 1, workspaceIdentityUsageScopeHash([]string{physicalID})
	if strings.TrimSpace(workspace.ID) != physicalID || strings.TrimSpace(workspace.CanonicalCode) != request.WorkspaceCode {
		return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_workspace_scope_mismatch", "denied", nil)
	}
	result := runtimeext.WorkspaceIdentityUsageResolveResult{
		WorkspaceCode: request.WorkspaceCode, WorkspaceDisplayName: strings.TrimSpace(workspace.DisplayName),
		WorkspaceStatus: strings.TrimSpace(workspace.Status), WorkspaceRevision: workspace.Revision,
		CommercialConfiguration: runtimeext.WorkspaceCommercialConfiguration{
			Plan: strings.TrimSpace(workspace.CommercialPlan), IncludedUserLimit: workspace.IncludedUserLimit, MaxUserLimit: workspace.MaxUserLimit,
			IncludedCustomerLimit: workspace.IncludedCustomerLimit, MaxCustomerLimit: workspace.MaxCustomerLimit,
			IncludedStoreLimit: workspace.IncludedStoreLimit, MaxStores: workspace.MaxStores, ContractDate: workspace.ContractDate,
			BillingDay: workspace.BillingDay, BillingContactName: workspace.BillingContactName, BillingContactPhone: workspace.BillingContactPhone,
			BillingContactEmail: workspace.BillingContactEmail, BillingContactAddress: workspace.BillingContactAddress,
			BillingContactNotes: workspace.BillingContactNotes, Revision: workspace.CommercialRevision,
		},
		Accounts: runtimeext.WorkspaceIdentityAccountCounts{
			ActiveHumanAccounts: identityUsage.Accounts.ActiveHumanAccounts, ActiveHumanAccountsWithActiveRole: identityUsage.Accounts.ActiveHumanAccountsWithActiveRole,
			DisabledHumanAccounts: identityUsage.Accounts.DisabledHumanAccounts, ServiceAccounts: identityUsage.Accounts.ServiceAccounts,
			AutomationAccounts: identityUsage.Accounts.AutomationAccounts,
		},
	}
	if result.WorkspaceDisplayName == "" || result.WorkspaceStatus != "active" || result.WorkspaceRevision != request.ExpectedWorkspaceRevision ||
		result.CommercialConfiguration.Plan == "" || result.CommercialConfiguration.Revision < 1 {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_catalog_failed", "error", nil)
	}
	auditValue.Outcome = "success"
	if err := e.auditWorkspaceIdentityUsage(auditCtx, auditValue); err != nil {
		return runtimeext.WorkspaceIdentityUsageResolveResult{}, apperror.New(apperror.KindInternal, "backend.action.workspace_identity_usage_audit_failed", err, nil)
	}
	return result, nil
}

func cloneWorkspaceIdentityUsageCapability(value *runtimeext.WorkspaceIdentityUsageCapability) *runtimeext.WorkspaceIdentityUsageCapability {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func (e *businessActionExecution) ListWorkspaceIdentityUsage(ctx context.Context, request runtimeext.WorkspaceIdentityUsageRequest) (runtimeext.WorkspaceIdentityUsagePage, error) {
	auditValue := WorkspaceIdentityUsageAudit{}
	auditCtx := ctx
	if e != nil {
		auditValue.ActionKey, auditValue.Principal = strings.TrimSpace(e.action.Key), e.invocation.Principal
	}
	fail := func(kind apperror.ErrorKind, code, outcome string, cause error) (runtimeext.WorkspaceIdentityUsagePage, error) {
		auditValue.Outcome, auditValue.ErrorCode = outcome, code
		if auditErr := e.auditWorkspaceIdentityUsage(auditCtx, auditValue); auditErr != nil {
			return runtimeext.WorkspaceIdentityUsagePage{}, apperror.New(apperror.KindInternal, "backend.action.workspace_identity_usage_audit_failed", auditErr, nil)
		}
		return runtimeext.WorkspaceIdentityUsagePage{}, apperror.New(kind, code, cause, nil)
	}
	if e == nil || e.unitOfWork == nil || e.workspaceUsageGrant == nil {
		return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_grant_denied", "denied", nil)
	}
	phase := e.Phase()
	if phase != runtimeext.ExecutionPhasePrewrite && phase != runtimeext.ExecutionPhaseWriting {
		return fail(apperror.KindConflict, "backend.action.workspace_identity_usage_phase_forbidden", "denied", nil)
	}
	if !ActionAllowed(e.invocation.Principal, e.action) {
		return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_action_denied", "denied", nil)
	}
	if !request.Valid() {
		return fail(apperror.KindBadRequest, "backend.action.workspace_identity_usage_request_invalid", "denied", nil)
	}
	pageSize := request.PageSize
	if pageSize == 0 {
		pageSize = e.workspaceUsageGrant.MaxPageSize
		if pageSize > identitysdk.WorkspaceIdentityUsageDefaultPageSize {
			pageSize = identitysdk.WorkspaceIdentityUsageDefaultPageSize
		}
	}
	if pageSize > e.workspaceUsageGrant.MaxPageSize || pageSize > identitysdk.WorkspaceIdentityUsageMaxPageSize {
		return fail(apperror.KindBadRequest, "backend.action.workspace_identity_usage_page_size_invalid", "denied", nil)
	}
	if e.dependencies.WorkspaceActiveResolver == nil || e.dependencies.BindWorkspaceIdentityUsage == nil || e.dependencies.AuthorizeWorkspaceIdentityUsage == nil {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_unavailable", "error", nil)
	}
	cursorBinding := WorkspaceIdentityUsageCursorBinding{
		ActionKey: strings.TrimSpace(e.action.Key), WorkspaceID: strings.TrimSpace(e.invocation.Principal.WorkspaceID),
		SubjectID: strings.TrimSpace(e.invocation.Principal.UserID), AuthorizationRevision: strings.TrimSpace(e.invocation.Principal.AuthorizationRevision),
	}
	if !cursorBinding.valid() {
		return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_authorization_revision_required", "denied", nil)
	}
	identityCursor := ""
	if strings.TrimSpace(request.Cursor) != "" {
		if e.dependencies.WorkspaceIdentityUsageCursor == nil {
			return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_cursor_unavailable", "error", nil)
		}
		var err error
		identityCursor, err = e.dependencies.WorkspaceIdentityUsageCursor.Open(request.Cursor, cursorBinding)
		if err != nil {
			return fail(apperror.KindBadRequest, "backend.action.workspace_identity_usage_cursor_invalid", "denied", err)
		}
	}
	authorization, err := e.authorizeWorkspaceIdentityUsage(ctx)
	if err != nil {
		return fail(apperror.KindOf(err), apperror.CodeOf(err), workspaceIdentityUsageErrorOutcome(err), err)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_transaction_required", "error", err)
	}
	auditCtx = txCtx
	usage, err := e.bindWorkspaceIdentityUsage(txCtx)
	if err != nil {
		return fail(apperror.KindOf(err), apperror.CodeOf(err), workspaceIdentityUsageErrorOutcome(err), err)
	}
	page, err := usage.ListWorkspaceIdentityUsage(txCtx, identitysdk.WorkspaceIdentityUsageRequest{
		ContractVersion: identitysdk.CurrentWorkspaceIdentityUsageContractVersion,
		ContractHash:    identitysdk.CurrentWorkspaceIdentityUsageContractHash,
		Authorization:   authorization,
		PageSize:        pageSize,
		Cursor:          identityCursor,
	})
	if err != nil {
		normalized := normalizeIdentityCapabilityError(err, "identity.workspace_identity_usage.aggregate_failed")
		return fail(apperror.KindOf(normalized), apperror.CodeOf(normalized), workspaceIdentityUsageErrorOutcome(normalized), normalized)
	}
	if len(page.Items) > pageSize {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_result_limit_exceeded", "error", nil)
	}
	physicalIDs := make([]string, len(page.Items))
	seen := make(map[string]bool, len(page.Items))
	previousID := ""
	for index, item := range page.Items {
		id := strings.TrimSpace(item.WorkspaceID)
		if id == "" || seen[id] || previousID != "" && id <= previousID || !validWorkspaceIdentityCounts(item.Accounts) {
			return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_result_invalid", "error", nil)
		}
		seen[id], physicalIDs[index], previousID = true, id, id
	}
	auditValue.WorkspaceCount, auditValue.ScopeSHA256 = len(physicalIDs), workspaceIdentityUsageScopeHash(physicalIDs)
	nextCursor := ""
	if strings.TrimSpace(page.NextCursor) != "" {
		if e.dependencies.WorkspaceIdentityUsageCursor == nil {
			return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_cursor_unavailable", "error", nil)
		}
		nextCursor, err = e.dependencies.WorkspaceIdentityUsageCursor.Seal(page.NextCursor, cursorBinding)
		if err != nil {
			return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_cursor_failed", "error", err)
		}
	}
	if len(physicalIDs) == 0 {
		auditValue.Outcome = "success"
		if err := e.auditWorkspaceIdentityUsage(auditCtx, auditValue); err != nil {
			return runtimeext.WorkspaceIdentityUsagePage{}, apperror.New(apperror.KindInternal, "backend.action.workspace_identity_usage_audit_failed", err, nil)
		}
		return runtimeext.WorkspaceIdentityUsagePage{NextCursor: nextCursor}, nil
	}
	workspaces, err := e.dependencies.WorkspaceActiveResolver.ResolveActive(txCtx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "map Identity usage to canonical Workspace references"), physicalIDs)
	if err != nil {
		return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_catalog_failed", "error", err)
	}
	if len(workspaces) != len(physicalIDs) {
		return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_workspace_scope_mismatch", "denied", nil)
	}
	result := runtimeext.WorkspaceIdentityUsagePage{Items: make([]runtimeext.WorkspaceIdentityUsageItem, len(page.Items)), NextCursor: nextCursor}
	canonicalCodes := make(map[string]bool, len(page.Items))
	for index, item := range page.Items {
		workspace, ok := workspaces[physicalIDs[index]]
		code, displayName, plan := strings.TrimSpace(workspace.CanonicalCode), strings.TrimSpace(workspace.DisplayName), strings.TrimSpace(workspace.CommercialPlan)
		if !ok || strings.TrimSpace(workspace.ID) != physicalIDs[index] || code == "" || canonicalCodes[code] {
			return fail(apperror.KindForbidden, "backend.action.workspace_identity_usage_workspace_scope_mismatch", "denied", nil)
		}
		if displayName == "" || plan == "" || workspace.IncludedUserLimit < 0 || workspace.MaxUserLimit < workspace.IncludedUserLimit {
			return fail(apperror.KindInternal, "backend.action.workspace_identity_usage_catalog_failed", "error", nil)
		}
		canonicalCodes[code] = true
		result.Items[index] = runtimeext.WorkspaceIdentityUsageItem{
			WorkspaceCode:        code,
			WorkspaceDisplayName: displayName,
			CommercialTerms: runtimeext.WorkspaceCommercialTerms{
				Plan: plan, IncludedUserLimit: workspace.IncludedUserLimit, MaxUserLimit: workspace.MaxUserLimit,
			},
			Accounts: runtimeext.WorkspaceIdentityAccountCounts{
				ActiveHumanAccounts: item.Accounts.ActiveHumanAccounts, ActiveHumanAccountsWithActiveRole: item.Accounts.ActiveHumanAccountsWithActiveRole,
				DisabledHumanAccounts: item.Accounts.DisabledHumanAccounts,
				ServiceAccounts:       item.Accounts.ServiceAccounts, AutomationAccounts: item.Accounts.AutomationAccounts,
			},
		}
	}
	auditValue.Outcome = "success"
	if err := e.auditWorkspaceIdentityUsage(auditCtx, auditValue); err != nil {
		return runtimeext.WorkspaceIdentityUsagePage{}, apperror.New(apperror.KindInternal, "backend.action.workspace_identity_usage_audit_failed", err, nil)
	}
	return result, nil
}

func (e *businessActionExecution) bindWorkspaceIdentityUsage(ctx context.Context) (identitysdk.WorkspaceIdentityUsageAggregate, error) {
	if err := e.validateWorkspaceIdentityUsageRequestIdentity(); err != nil {
		return nil, err
	}
	if e.dependencies.BindWorkspaceIdentityUsage == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.workspace_usage_transaction_required", nil, nil)
	}
	capability, err := e.dependencies.BindWorkspaceIdentityUsage(ctx)
	if err != nil {
		return nil, normalizeIdentityCapabilityError(err, "identity.workspace_usage_transaction_required")
	}
	if capability == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.workspace_usage_transaction_required", nil, nil)
	}
	return capability, nil
}

func (e *businessActionExecution) authorizeWorkspaceIdentityUsage(ctx context.Context) (identitysdk.WorkspaceIdentityUsageAuthorization, error) {
	if err := e.validateWorkspaceIdentityUsageRequestIdentity(); err != nil {
		return identitysdk.WorkspaceIdentityUsageAuthorization{}, err
	}
	if e.dependencies.AuthorizeWorkspaceIdentityUsage == nil {
		return identitysdk.WorkspaceIdentityUsageAuthorization{}, apperror.New(apperror.KindInternal, "identity.workspace_usage_authority_required", nil, nil)
	}
	authorization, err := e.dependencies.AuthorizeWorkspaceIdentityUsage(ctx, strings.TrimSpace(e.requestIdentity.AccessToken))
	if err != nil {
		return identitysdk.WorkspaceIdentityUsageAuthorization{}, normalizeIdentityCapabilityError(err, "identity.workspace_usage_authority_required")
	}
	return authorization, nil
}

func (e *businessActionExecution) validateWorkspaceIdentityUsageRequestIdentity() error {
	if e == nil || strings.TrimSpace(e.requestIdentity.AccessToken) == "" || !e.requestIdentity.Principal.Known ||
		strings.TrimSpace(e.requestIdentity.Principal.WorkspaceID) != strings.TrimSpace(e.invocation.Principal.WorkspaceID) ||
		strings.TrimSpace(e.requestIdentity.Principal.UserID) != strings.TrimSpace(e.invocation.Principal.UserID) ||
		strings.TrimSpace(e.requestIdentity.Principal.AuthorizationRevision) == "" || strings.TrimSpace(e.requestIdentity.Principal.AuthorizationRevision) != strings.TrimSpace(e.invocation.Principal.AuthorizationRevision) {
		return apperror.New(apperror.KindForbidden, "identity.workspace_usage_request_identity_required", nil, nil)
	}
	return nil
}

func (e *businessActionExecution) auditWorkspaceIdentityUsage(ctx context.Context, value WorkspaceIdentityUsageAudit) error {
	if e == nil || e.dependencies.AuditWorkspaceIdentityUsage == nil {
		return errors.New("Workspace identity usage audit is unavailable")
	}
	auditContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), workspaceIdentityUsageAuditTimeout)
	defer cancel()
	return e.dependencies.AuditWorkspaceIdentityUsage(auditContext, value)
}

func validWorkspaceIdentityCounts(value identitysdk.WorkspaceIdentityAccountCounts) bool {
	return value.ActiveHumanAccounts >= 0 && value.ActiveHumanAccountsWithActiveRole >= 0 && value.ActiveHumanAccountsWithActiveRole <= value.ActiveHumanAccounts &&
		value.DisabledHumanAccounts >= 0 && value.ServiceAccounts >= 0 && value.AutomationAccounts >= 0
}

func workspaceIdentityUsageScopeHash(ids []string) string {
	values := append([]string(nil), ids...)
	sort.Strings(values)
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:])
}

func workspaceIdentityUsageErrorOutcome(err error) string {
	switch apperror.KindOf(err) {
	case apperror.KindBadRequest, apperror.KindForbidden, apperror.KindNotFound, apperror.KindConflict:
		return "denied"
	default:
		return "error"
	}
}
