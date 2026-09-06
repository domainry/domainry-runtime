package action

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	aggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
)

type workspaceIdentityUsageStub struct {
	request        identitysdk.WorkspaceIdentityUsageRequest
	resolveRequest identitysdk.WorkspaceIdentityUsageResolveRequest
	resolveCalls   int
	page           identitysdk.WorkspaceIdentityUsagePage
	err            error
}

func (stub *workspaceIdentityUsageStub) ListWorkspaceIdentityUsage(_ context.Context, request identitysdk.WorkspaceIdentityUsageRequest) (identitysdk.WorkspaceIdentityUsagePage, error) {
	stub.request = request
	return stub.page, stub.err
}

func (stub *workspaceIdentityUsageStub) ResolveWorkspaceIdentityUsage(_ context.Context, request identitysdk.WorkspaceIdentityUsageResolveRequest) (identitysdk.WorkspaceIdentityUsage, error) {
	stub.resolveCalls++
	stub.resolveRequest = request
	if stub.err != nil {
		return identitysdk.WorkspaceIdentityUsage{}, stub.err
	}
	if len(stub.page.Items) == 0 {
		return identitysdk.WorkspaceIdentityUsage{}, nil
	}
	return stub.page.Items[0], nil
}

type workspaceActiveResolverStub struct {
	workspaces map[string]aggregatecontract.Workspace
	ids        []string
	err        error
}

func (stub *workspaceActiveResolverStub) ResolveActive(ctx context.Context, _ principalmodel.SystemScope, ids []string) (map[string]aggregatecontract.Workspace, error) {
	if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
		return nil, errors.New("Workspace scope projection did not use Action transaction")
	}
	stub.ids = append([]string(nil), ids...)
	result := make(map[string]aggregatecontract.Workspace, len(stub.workspaces))
	for key, workspace := range stub.workspaces {
		result[key] = workspace
	}
	return result, stub.err
}

func (stub *workspaceActiveResolverStub) ResolveUsageWorkspace(ctx context.Context, _ principalmodel.SystemScope, code string, expectedRevision int64) (aggregatecontract.Workspace, error) {
	if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
		return aggregatecontract.Workspace{}, errors.New("exact usage did not use Action transaction")
	}
	if stub.err != nil {
		return aggregatecontract.Workspace{}, stub.err
	}
	for _, workspace := range stub.workspaces {
		if workspace.CanonicalCode == code {
			if workspace.Revision != expectedRevision {
				return aggregatecontract.Workspace{}, aggregatecontract.ErrUsageWorkspaceRevisionConflict
			}
			return workspace, nil
		}
	}
	return aggregatecontract.Workspace{}, aggregatecontract.ErrUsageWorkspaceNotFound
}

func workspaceIdentityUsageTestExecution(t *testing.T, usage identitysdk.WorkspaceIdentityUsageAggregate, resolver aggregatecontract.ActiveResolver, audits *[]WorkspaceIdentityUsageAudit) *businessActionExecution {
	t.Helper()
	principal := workspaceAggregatePrincipal("billing.invoice.generate")
	principal.UserID = "billing-operator"
	principal.AuthorizationRevision = "authz-1"
	unitOfWork := newActionTestUnitOfWork()
	unitOfWork.claim = actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "usage-execution-1", LeaseOwner: "test", FencingToken: 1}}
	cursorCodec, err := NewWorkspaceIdentityUsageCursorCodec([]byte("test-workspace-usage-cursor-key"), "installation-a", func() time.Time {
		return time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	return &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			WorkspaceActiveResolver:      resolver,
			WorkspaceUsageResolver:       resolver.(aggregatecontract.UsageResolver),
			WorkspaceIdentityUsageCursor: cursorCodec,
			AuthorizeWorkspaceIdentityUsage: func(ctx context.Context, accessToken string) (identitysdk.WorkspaceIdentityUsageAuthorization, error) {
				if ctx.Value(actionUnitOfWorkTransactionContextKey{}) == true || accessToken != "trusted-installation-token" {
					t.Fatal("Identity usage authorization was not completed before the Action transaction")
				}
				return identitysdk.WorkspaceIdentityUsageAuthorization{
					InstallationID: "installation-a", ApplicationKey: "nightpos", SubjectID: "billing-operator",
					AuditWorkspaceID: "workspace-hq", PermissionKey: identitysdk.WorkspaceIdentityUsageAggregatePermission,
					AuthorizationRevision: "authz-1", AuthorizationAuditID: "authority-audit-1",
				}, nil
			},
			BindWorkspaceIdentityUsage: func(ctx context.Context) (identitysdk.WorkspaceIdentityUsageAggregate, error) {
				if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
					t.Fatal("Identity usage was not bound to the Action transaction")
				}
				return usage, nil
			},
			AuditWorkspaceIdentityUsage: func(_ context.Context, value WorkspaceIdentityUsageAudit) error {
				*audits = append(*audits, value)
				return nil
			},
		},
		invocation: actionmodel.ActionInvocation{Principal: principal},
		action: definitionmodel.ActionSchema{Key: "billing.invoice.generate", ObjectKey: "invoice", EffectSet: &definitionmodel.ActionEffectSet{
			Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "invoice"}},
		}},
		unitOfWork:          unitOfWork,
		workspaceUsageGrant: &runtimeext.WorkspaceIdentityUsageCapability{MaxPageSize: 25},
		requestIdentity: identitysdk.RequestIdentity{
			Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "billing-operator", AuthorizationRevision: "authz-1"}, AccessToken: "trusted-installation-token",
		},
	}
}

func TestWorkspaceIdentityUsageExactResolveReturnsCanonicalCASCommercialSnapshotAndCounts(t *testing.T) {
	usage := &workspaceIdentityUsageStub{page: identitysdk.WorkspaceIdentityUsagePage{Items: []identitysdk.WorkspaceIdentityUsage{{
		WorkspaceID: "workspace-physical-a",
		Accounts: identitysdk.WorkspaceIdentityAccountCounts{
			ActiveHumanAccounts: 8, ActiveHumanAccountsWithActiveRole: 6, DisabledHumanAccounts: 2, ServiceAccounts: 1,
		},
	}}}}
	resolver := &workspaceActiveResolverStub{workspaces: map[string]aggregatecontract.Workspace{
		"workspace-physical-a": {
			ID: "workspace-physical-a", CanonicalCode: "night-tokyo", DisplayName: "Night Tokyo", Status: "active", Revision: 7,
			CommercialPlan: "premium", IncludedUserLimit: 5, MaxUserLimit: 30, IncludedCustomerLimit: 100, MaxCustomerLimit: 1000,
			IncludedStoreLimit: 1, MaxStores: 5, ContractDate: "2026-09-01", BillingDay: 25,
			BillingContactName: "Accounts", BillingContactEmail: "accounts@example.test", CommercialRevision: 3,
		},
	}}
	audits := []WorkspaceIdentityUsageAudit{}
	execution := workspaceIdentityUsageTestExecution(t, usage, resolver, &audits)
	result, err := execution.ResolveWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageResolveRequest{
		WorkspaceCode: "night-tokyo", ExpectedWorkspaceRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.resolveRequest.WorkspaceCode != "night-tokyo" || usage.resolveRequest.Authorization.AuthorizationAuditID != "authority-audit-1" || usage.resolveRequest.AccessToken != "" ||
		result.WorkspaceCode != "night-tokyo" || result.WorkspaceDisplayName != "Night Tokyo" || result.WorkspaceStatus != "active" || result.WorkspaceRevision != 7 ||
		result.CommercialConfiguration.Plan != "premium" || result.CommercialConfiguration.IncludedUserLimit != 5 || result.CommercialConfiguration.MaxStores != 5 ||
		result.CommercialConfiguration.BillingContactEmail != "accounts@example.test" || result.CommercialConfiguration.Revision != 3 ||
		result.Accounts.ActiveHumanAccountsWithActiveRole != 6 || len(audits) != 1 || audits[0].Outcome != "success" || audits[0].WorkspaceCount != 1 {
		t.Fatalf("identity_request=%#v result=%#v audits=%#v", usage.resolveRequest, result, audits)
	}
	if strings.Contains(fmt.Sprintf("%#v", result), "workspace-physical-a") {
		t.Fatalf("physical Workspace ID leaked: %#v", result)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestWorkspaceIdentityUsageExactResolveRejectsStaleRevisionBeforeCounting(t *testing.T) {
	usage := &workspaceIdentityUsageStub{page: identitysdk.WorkspaceIdentityUsagePage{Items: []identitysdk.WorkspaceIdentityUsage{{
		WorkspaceID: "workspace-physical-a", Accounts: identitysdk.WorkspaceIdentityAccountCounts{ActiveHumanAccounts: 8},
	}}}}
	resolver := &workspaceActiveResolverStub{workspaces: map[string]aggregatecontract.Workspace{
		"workspace-physical-a": {ID: "workspace-physical-a", CanonicalCode: "night-tokyo", Status: "active", Revision: 8},
	}}
	audits := []WorkspaceIdentityUsageAudit{}
	execution := workspaceIdentityUsageTestExecution(t, usage, resolver, &audits)
	result, err := execution.ResolveWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageResolveRequest{
		WorkspaceCode: "night-tokyo", ExpectedWorkspaceRevision: 7,
	})
	if apperror.CodeOf(err) != "backend.action.workspace_identity_usage_revision_conflict" || result.WorkspaceCode != "" || usage.resolveCalls != 0 ||
		len(audits) != 1 || audits[0].Outcome != "denied" {
		t.Fatalf("result=%#v error=%v resolve_calls=%d audits=%#v", result, err, usage.resolveCalls, audits)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestWorkspaceIdentityUsageMapsPhysicalIDsToCanonicalCodesInSameActionUoW(t *testing.T) {
	usage := &workspaceIdentityUsageStub{page: identitysdk.WorkspaceIdentityUsagePage{
		Items: []identitysdk.WorkspaceIdentityUsage{
			{WorkspaceID: "workspace-physical-a", Accounts: identitysdk.WorkspaceIdentityAccountCounts{ActiveHumanAccounts: 7, ActiveHumanAccountsWithActiveRole: 5, DisabledHumanAccounts: 2, ServiceAccounts: 1}},
			{WorkspaceID: "workspace-physical-b", Accounts: identitysdk.WorkspaceIdentityAccountCounts{ActiveHumanAccounts: 3, ActiveHumanAccountsWithActiveRole: 2, AutomationAccounts: 4}},
		},
		NextCursor: "identity-opaque-next",
	}}
	resolver := &workspaceActiveResolverStub{workspaces: map[string]aggregatecontract.Workspace{
		"workspace-physical-a": {ID: "workspace-physical-a", CanonicalCode: "tokyo", DisplayName: "Tokyo Night", CommercialPlan: "standard", IncludedUserLimit: 5, MaxUserLimit: 25},
		"workspace-physical-b": {ID: "workspace-physical-b", CanonicalCode: "osaka", DisplayName: "Osaka Night", CommercialPlan: "enterprise", IncludedUserLimit: 10, MaxUserLimit: 100},
	}}
	audits := []WorkspaceIdentityUsageAudit{}
	execution := workspaceIdentityUsageTestExecution(t, usage, resolver, &audits)
	page, err := execution.ListWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageRequest{PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if usage.request.AccessToken != "" || usage.request.Authorization.AuthorizationAuditID != "authority-audit-1" || usage.request.ContractVersion != identitysdk.CurrentWorkspaceIdentityUsageContractVersion || usage.request.ContractHash != identitysdk.CurrentWorkspaceIdentityUsageContractHash || usage.request.PageSize != 20 || usage.request.Cursor != "" {
		t.Fatalf("request=%#v", usage.request)
	}
	if len(page.Items) != 2 || page.Items[0].WorkspaceCode != "tokyo" || page.Items[0].WorkspaceDisplayName != "Tokyo Night" ||
		page.Items[0].CommercialTerms.Plan != "standard" || page.Items[0].CommercialTerms.IncludedUserLimit != 5 || page.Items[0].CommercialTerms.MaxUserLimit != 25 ||
		page.Items[0].Accounts.ActiveHumanAccounts != 7 || page.Items[0].Accounts.ActiveHumanAccountsWithActiveRole != 5 ||
		page.Items[1].WorkspaceCode != "osaka" || page.Items[1].WorkspaceDisplayName != "Osaka Night" || page.Items[1].CommercialTerms.Plan != "enterprise" ||
		page.NextCursor == "" || page.NextCursor == "identity-opaque-next" || strings.Contains(page.NextCursor, "workspace-physical") {
		t.Fatalf("page=%#v", page)
	}
	if len(resolver.ids) != 2 || len(audits) != 1 || audits[0].Outcome != "success" || audits[0].WorkspaceCount != 2 || audits[0].ScopeSHA256 == "" {
		t.Fatalf("ids=%#v audits=%#v", resolver.ids, audits)
	}
	if _, err := execution.ListWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageRequest{PageSize: 20, Cursor: page.NextCursor}); err != nil || usage.request.Cursor != "identity-opaque-next" {
		t.Fatalf("sealed cursor did not reopen only inside Runtime: request=%#v err=%v", usage.request, err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestWorkspaceIdentityUsageFailsWholePageForUnknownWorkspaceOrAuditFailure(t *testing.T) {
	usage := &workspaceIdentityUsageStub{page: identitysdk.WorkspaceIdentityUsagePage{Items: []identitysdk.WorkspaceIdentityUsage{{WorkspaceID: "workspace-physical-a"}, {WorkspaceID: "workspace-unknown"}}}}
	resolver := &workspaceActiveResolverStub{workspaces: map[string]aggregatecontract.Workspace{"workspace-physical-a": {ID: "workspace-physical-a", CanonicalCode: "tokyo", DisplayName: "Tokyo Night", CommercialPlan: "standard", IncludedUserLimit: 5, MaxUserLimit: 25}}}
	audits := []WorkspaceIdentityUsageAudit{}
	execution := workspaceIdentityUsageTestExecution(t, usage, resolver, &audits)
	page, err := execution.ListWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageRequest{})
	if apperror.CodeOf(err) != "backend.action.workspace_identity_usage_workspace_scope_mismatch" || len(page.Items) != 0 || len(audits) != 1 || audits[0].Outcome != "denied" {
		t.Fatalf("page=%#v audits=%#v err=%v", page, audits, err)
	}
	execution.unitOfWork.rollBack(t.Context())

	audits = nil
	execution = workspaceIdentityUsageTestExecution(t, &workspaceIdentityUsageStub{page: identitysdk.WorkspaceIdentityUsagePage{Items: []identitysdk.WorkspaceIdentityUsage{{WorkspaceID: "workspace-physical-a"}}}}, &workspaceActiveResolverStub{workspaces: map[string]aggregatecontract.Workspace{"workspace-physical-a": {ID: "workspace-physical-a", CanonicalCode: "tokyo", DisplayName: "Tokyo Night", CommercialPlan: "standard", IncludedUserLimit: 5, MaxUserLimit: 25}}}, &audits)
	execution.dependencies.AuditWorkspaceIdentityUsage = func(context.Context, WorkspaceIdentityUsageAudit) error { return errors.New("audit unavailable") }
	page, err = execution.ListWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageRequest{})
	if apperror.CodeOf(err) != "backend.action.workspace_identity_usage_audit_failed" || len(page.Items) != 0 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestWorkspaceIdentityUsageFailsWholePageForInvalidCommercialProjection(t *testing.T) {
	usage := &workspaceIdentityUsageStub{page: identitysdk.WorkspaceIdentityUsagePage{Items: []identitysdk.WorkspaceIdentityUsage{{WorkspaceID: "workspace-physical-a"}}}}
	resolver := &workspaceActiveResolverStub{workspaces: map[string]aggregatecontract.Workspace{
		"workspace-physical-a": {ID: "workspace-physical-a", CanonicalCode: "tokyo", DisplayName: "Tokyo Night", CommercialPlan: "standard", IncludedUserLimit: 10, MaxUserLimit: 5},
	}}
	audits := []WorkspaceIdentityUsageAudit{}
	execution := workspaceIdentityUsageTestExecution(t, usage, resolver, &audits)
	page, err := execution.ListWorkspaceIdentityUsage(t.Context(), runtimeext.WorkspaceIdentityUsageRequest{})
	if apperror.CodeOf(err) != "backend.action.workspace_identity_usage_catalog_failed" || len(page.Items) != 0 || len(audits) != 1 || audits[0].Outcome != "error" {
		t.Fatalf("page=%#v audits=%#v err=%v", page, audits, err)
	}
	execution.unitOfWork.rollBack(t.Context())
}
