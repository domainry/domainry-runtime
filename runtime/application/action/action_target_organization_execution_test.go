package action

import (
	"context"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type targetOrganizationDeliveryStub struct {
	requests     []identitysdk.StoreOrganizationDeliveryRequest
	listRequests []identitysdk.StoreOrganizationListRequest
	result       identitysdk.StoreOrganizationDeliveryResult
	page         identitysdk.StoreOrganizationPage
	err          error
}

func (stub *targetOrganizationDeliveryStub) DeliverStoreOrganization(_ context.Context, request identitysdk.StoreOrganizationDeliveryRequest) (identitysdk.StoreOrganizationDeliveryResult, error) {
	stub.requests = append(stub.requests, request)
	return stub.result, stub.err
}

func (*targetOrganizationDeliveryStub) ResolveStoreOrganization(context.Context, identitysdk.StoreOrganizationResolveRequest) (identitysdk.StoreOrganization, error) {
	return identitysdk.StoreOrganization{}, nil
}

func (stub *targetOrganizationDeliveryStub) ListStoreOrganizations(_ context.Context, request identitysdk.StoreOrganizationListRequest) (identitysdk.StoreOrganizationPage, error) {
	stub.listRequests = append(stub.listRequests, request)
	return stub.page, stub.err
}

func TestSoleAuthorizedStoreResolutionFailsClosed(t *testing.T) {
	tests := []struct {
		name             string
		page             identitysdk.StoreOrganizationPage
		wantID, wantCode string
	}{
		{name: "one active", page: identitysdk.StoreOrganizationPage{Items: []identitysdk.StoreOrganization{{ID: "store-only", Status: "active"}}}, wantID: "store-only"},
		{name: "none", wantCode: "backend.action.target_organization_ambiguous"},
		{name: "two", page: identitysdk.StoreOrganizationPage{Items: []identitysdk.StoreOrganization{{ID: "store-a", Status: "active"}, {ID: "store-b", Status: "active"}}}, wantCode: "backend.action.target_organization_ambiguous"},
		{name: "continuation", page: identitysdk.StoreOrganizationPage{Items: []identitysdk.StoreOrganization{{ID: "store-a", Status: "active"}}, NextCursor: "more"}, wantCode: "backend.action.target_organization_ambiguous"},
		{name: "disabled", page: identitysdk.StoreOrganizationPage{Items: []identitysdk.StoreOrganization{{ID: "store-a", Status: "disabled"}}}, wantCode: "backend.action.target_organization_ambiguous"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delivery := &targetOrganizationDeliveryStub{page: test.page}
			execution := &businessActionExecution{requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"}}
			id, err := execution.resolveSoleAuthorizedActiveStore(t.Context(), delivery)
			gotCode := ""
			if err != nil {
				gotCode = apperror.CodeOf(err)
			}
			if id != test.wantID || gotCode != test.wantCode {
				t.Fatalf("id=%q error=%v", id, err)
			}
			if len(delivery.listRequests) != 1 || delivery.listRequests[0].PageSize != 2 || delivery.listRequests[0].AccessToken != "trusted-token" {
				t.Fatalf("request=%+v", delivery.listRequests)
			}
		})
	}
}

func TestExplicitOrSoleAuthorizedStoreInvocationAcceptsOptionalMetadataOnlyForObjectAction(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "employee_profile.staffing_snapshot", ObjectKey: "employee_profile", Kind: definitionmodel.ActionKindObjectOperation,
		TargetOrganization: &definitionmodel.ActionTargetOrganizationPolicy{Source: definitionmodel.ActionTargetOrganizationSourceExplicitOrSoleAuthorizedStore, Input: definitionmodel.ActionTargetOrganizationInputInvocation}}
	for _, target := range []string{"", "store-selected"} {
		if err := validateActionTargetOrganizationInvocation(action, actionmodel.ActionInvocation{TargetOrganizationID: target}); err != nil {
			t.Fatalf("target %q error=%v", target, err)
		}
	}
	explicit := action
	explicit.TargetOrganization = &definitionmodel.ActionTargetOrganizationPolicy{Source: definitionmodel.ActionTargetOrganizationSourceExplicit, Input: definitionmodel.ActionTargetOrganizationInputInvocation}
	if err := validateActionTargetOrganizationInvocation(explicit, actionmodel.ActionInvocation{}); apperror.CodeOf(err) != "backend.action.target_organization_required" {
		t.Fatalf("explicit omission error=%v", err)
	}
}

type workspaceCommercialConfigurationLockerFunc func(context.Context, string) (WorkspaceCommercialConfiguration, error)

func (lock workspaceCommercialConfigurationLockerFunc) LockWorkspaceCommercialConfiguration(ctx context.Context, workspaceID string) (WorkspaceCommercialConfiguration, error) {
	return lock(ctx, workspaceID)
}

func TestProvisionStoreOrganizationUsesRuntimeStableIDAndFixesAllWritesToNewTarget(t *testing.T) {
	delivery := &targetOrganizationDeliveryStub{page: identitysdk.StoreOrganizationPage{Items: []identitysdk.StoreOrganization{{ID: "first-store", ParentOrganizationID: "company-hq", Status: "active"}}}}
	unitOfWork := newActionTestUnitOfWork()
	unitOfWork.claim = actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "execution-store-1", LeaseOwner: "test", FencingToken: 1}}
	bundle := &identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "hq-operator", AccessBundle: bundle}}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{BindStoreOrganizationDelivery: func(ctx context.Context) (identitysdk.StoreOrganizationDelivery, error) {
			if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
				t.Fatal("Store Organization delivery was not bound to the Action transaction")
			}
			return delivery, nil
		}, WorkspaceCommercialConfiguration: workspaceCommercialConfigurationLockerFunc(func(context.Context, string) (WorkspaceCommercialConfiguration, error) {
			return WorkspaceCommercialConfiguration{MaxStores: 2, Revision: 1, CompanyOrganizationID: "company-hq"}, nil
		})},
		identity: runtimeext.ExecutionIdentity{ExecutionID: "execution-store-1"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
		invocation: actionmodel.ActionInvocation{Principal: principal}, action: definitionmodel.ActionSchema{Key: "store.create", EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "store_profile"}}}},
		unitOfWork: unitOfWork, targetGrant: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceProvisionedStore},
		requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
	}
	wantID := stableStoreOrganizationID("workspace-a", "execution-store-1")
	delivery.result = identitysdk.StoreOrganizationDeliveryResult{Organization: identitysdk.StoreOrganization{ID: wantID}}
	request := runtimeext.StoreOrganizationProvisionRequest{Code: "TOKYO", Name: "Tokyo", SortOrder: 2}
	result, err := execution.ProvisionStoreOrganization(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.ID != wantID || len(delivery.requests) != 1 || delivery.requests[0].Organization.OrganizationID != wantID || delivery.requests[0].Organization.ParentOrganizationID != "company-hq" {
		t.Fatalf("result=%#v requests=%#v", result, delivery.requests)
	}
	// No Runtime parent-Organization authorization was available or evaluated;
	// Identity owns authorization of the create against company-hq. Runtime adds
	// only the exact newly created target for subsequent business writes.
	if execution.mutationPrincipal == nil || len(execution.mutationPrincipal.AccessBundle.DataPolicies) != 1 {
		t.Fatalf("mutation principal=%#v", execution.mutationPrincipal)
	}
	policy := execution.mutationPrincipal.AccessBundle.DataPolicies[0]
	if policy.Predicate.Fact != "owner_org_id" || policy.Predicate.Operator != identitysdk.OperatorEqual || policy.Predicate.Value != wantID || policy.Predicate.Value == "company-hq" {
		t.Fatalf("provisioned target policy=%#v", policy)
	}

	execution.dependencies.PlanCreateMutation = func(ctx context.Context, objectKey string, fields map[string]any, _ string, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		invocation, ok := recordmutation.MutationInvocationFromContext(ctx)
		if !ok || invocation.TargetOrganizationID != wantID {
			t.Fatalf("mutation target authority=%#v", invocation)
		}
		record := recordmodel.Record{ID: "store-profile-1", OwnerOrgID: wantID, Data: fields}
		mutationContext, contextErr := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction, ActionKey: "store.create", CorrelationID: "correlation", ApplicationSchemaRevision: "schema"})
		if contextErr != nil {
			return transactionmodel.MutationPlan{}, recordmodel.Record{}, contextErr
		}
		plan, planErr := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: objectKey}, Record: record}, nil)
		return plan, record, planErr
	}
	if _, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{Operation: runtimeext.MutationCreate, ObjectKey: "store_profile", Fields: map[string]any{"name": "Tokyo"}}); err != nil {
		t.Fatal(err)
	}
	if replayed, err := execution.ProvisionStoreOrganization(t.Context(), request); err != nil || replayed.Target.ID != wantID || len(delivery.requests) != 1 {
		t.Fatalf("same-call replay=%#v requests=%d err=%v", replayed, len(delivery.requests), err)
	}
	changed := request
	changed.Name = "Other"
	if _, err := execution.ProvisionStoreOrganization(t.Context(), changed); apperror.CodeOf(err) != "backend.idempotency_key_reused" {
		t.Fatalf("changed provision request error=%v", err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestStoreOrganizationRenameRequiresExactGrantAndUsesFixedRecordOwner(t *testing.T) {
	delivery := &targetOrganizationDeliveryStub{}
	delivery.result = identitysdk.StoreOrganizationDeliveryResult{Organization: identitysdk.StoreOrganization{ID: "store-north", Name: "North Flagship", Status: "active", Version: 4}}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{BindStoreOrganizationDelivery: func(context.Context) (identitysdk.StoreOrganizationDelivery, error) { return delivery, nil }},
		identity:     runtimeext.ExecutionIdentity{ExecutionID: "execution-rename-1"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
		unitOfWork: newActionTestUnitOfWork(), targetGrant: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceRecordOwner},
		targetResolved: true, targetOrganization: runtimeext.TargetOrganization{ID: "store-north"}, requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
		storeMutationGrant: &runtimeext.ActionStoreOrganizationMutationCapability{Operations: []runtimeext.StoreOrganizationMutationOperation{runtimeext.StoreOrganizationMutationRename}},
	}
	request := runtimeext.StoreOrganizationRenameRequest{Name: " North Flagship ", ExpectedVersion: 3}
	result, err := execution.RenameStoreOrganization(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.ID != "store-north" || result.Version != 4 || len(delivery.requests) != 1 {
		t.Fatalf("result=%+v requests=%+v", result, delivery.requests)
	}
	mutation := delivery.requests[0].Organization
	if mutation.Operation != identitysdk.StoreOrganizationRename || mutation.OrganizationID != "store-north" || mutation.Name != "North Flagship" || mutation.ExpectedVersion != 3 || mutation.ParentOrganizationID != "" || mutation.Code != "" {
		t.Fatalf("Identity mutation=%+v", mutation)
	}
	if replay, err := execution.RenameStoreOrganization(t.Context(), request); err != nil || replay.Target.ID != "store-north" || len(delivery.requests) != 1 {
		t.Fatalf("same-call replay=%+v requests=%d err=%v", replay, len(delivery.requests), err)
	}
	if _, err := execution.DisableStoreOrganization(t.Context(), runtimeext.StoreOrganizationDisableRequest{ExpectedVersion: 4}); apperror.CodeOf(err) != "backend.action.store_organization_mutation_grant_denied" {
		t.Fatalf("wrong-operation grant error=%v", err)
	}
	execution.storeMutationGrant = nil
	if _, err := execution.RenameStoreOrganization(t.Context(), request); apperror.CodeOf(err) != "backend.action.store_organization_mutation_grant_denied" {
		t.Fatalf("missing grant error=%v", err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestProvisionStoreOrganizationEnforcesWorkspaceQuotaAndCompanyScope(t *testing.T) {
	tests := []struct {
		name      string
		items     []identitysdk.StoreOrganization
		maxStores int
		wantCode  string
		wantWrite bool
	}{
		{name: "limit reached", maxStores: 1, items: []identitysdk.StoreOrganization{{ID: "first-store", ParentOrganizationID: "company-hq", Status: "active"}}, wantCode: "backend.action.store_quota_exceeded"},
		{name: "disabled store excluded", maxStores: 1, items: []identitysdk.StoreOrganization{{ID: "old-store", ParentOrganizationID: "company-hq", Status: "disabled"}}, wantWrite: true},
		{name: "foreign company fails closed", maxStores: 2, items: []identitysdk.StoreOrganization{{ID: "foreign-store", ParentOrganizationID: "other-company", Status: "active"}}, wantCode: "backend.action.store_company_scope_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delivery := &targetOrganizationDeliveryStub{page: identitysdk.StoreOrganizationPage{Items: test.items}}
			executionID := "execution-" + strings.ReplaceAll(test.name, " ", "-")
			wantID := stableStoreOrganizationID("workspace-a", executionID)
			delivery.result = identitysdk.StoreOrganizationDeliveryResult{Organization: identitysdk.StoreOrganization{ID: wantID}}
			execution := &businessActionExecution{
				dependencies: BusinessHandlerExecutionDependencies{
					BindStoreOrganizationDelivery: func(context.Context) (identitysdk.StoreOrganizationDelivery, error) { return delivery, nil },
					WorkspaceCommercialConfiguration: workspaceCommercialConfigurationLockerFunc(func(_ context.Context, workspaceID string) (WorkspaceCommercialConfiguration, error) {
						if workspaceID != "workspace-a" {
							t.Fatalf("workspace=%q", workspaceID)
						}
						return WorkspaceCommercialConfiguration{MaxStores: test.maxStores, Revision: 1, CompanyOrganizationID: "company-hq"}, nil
					}),
				},
				identity: runtimeext.ExecutionIdentity{ExecutionID: executionID}, workspace: runtimeext.Workspace{ID: "workspace-a"}, unitOfWork: newActionTestUnitOfWork(),
				targetGrant: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceProvisionedStore}, requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
			}
			_, err := execution.ProvisionStoreOrganization(t.Context(), runtimeext.StoreOrganizationProvisionRequest{Code: "next", Name: "Next", SortOrder: 2})
			if test.wantCode != "" {
				if apperror.CodeOf(err) != test.wantCode || len(delivery.requests) != 0 {
					t.Fatalf("error=%v writes=%d", err, len(delivery.requests))
				}
			} else if err != nil || !test.wantWrite || len(delivery.requests) != 1 || delivery.requests[0].Organization.ParentOrganizationID != "company-hq" {
				t.Fatalf("error=%v writes=%+v", err, delivery.requests)
			}
			execution.unitOfWork.rollBack(t.Context())
		})
	}
}

func TestTargetOrganizationRejectsMixedMutationPlan(t *testing.T) {
	execution := &businessActionExecution{
		targetGrant:    &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit},
		targetResolved: true, targetOrganization: runtimeext.TargetOrganization{ID: "store-north"},
	}
	mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction, ActionKey: "order.create", CorrelationID: "correlation", ApplicationSchemaRevision: "schema"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: "order-1", OwnerOrgID: "store-south"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.validateMutationTarget([]transactionmodel.MutationPlan{plan}); apperror.CodeOf(err) != "backend.action.mixed_target_organization" {
		t.Fatalf("mixed target error=%v", err)
	}
}
