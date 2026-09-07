package action

import (
	"context"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type organizationUnitDeliveryStub struct {
	createRequests  []organizationunit.DeliveryRequest
	resolveRequests []organizationunit.ResolveRequest
	createResult    organizationunit.DeliveryResult
	resolveResult   organizationunit.DeliveredOrganizationUnit
	err             error
}

func (stub *organizationUnitDeliveryStub) CreateOrganizationUnit(_ context.Context, request organizationunit.DeliveryRequest) (organizationunit.DeliveryResult, error) {
	stub.createRequests = append(stub.createRequests, request)
	return stub.createResult, stub.err
}

func (stub *organizationUnitDeliveryStub) ResolveOrganizationUnit(_ context.Context, request organizationunit.ResolveRequest) (organizationunit.DeliveredOrganizationUnit, error) {
	stub.resolveRequests = append(stub.resolveRequests, request)
	return stub.resolveResult, stub.err
}

func TestOrganizationUnitDeliveryCreatesDepartmentWithoutStoreQuotaAndFixesDeliveredTarget(t *testing.T) {
	delivery := &organizationUnitDeliveryStub{}
	unitOfWork := newActionTestUnitOfWork()
	executionID := "execution-department-sales"
	wantID := stableOrganizationUnitID("workspace-a", executionID)
	delivery.createResult = organizationunit.DeliveryResult{DeliveryID: "delivery-department-sales", Organization: organizationunit.DeliveredOrganizationUnit{
		ID: wantID, Code: "SALES", Name: "Sales", NodeType: organizationunit.NodeTypeDepartment,
		Status: "active", ParentOrganizationID: "company-hq", SortOrder: 20, Version: 1,
	}}
	lockerCalls := 0
	bundle := &identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			BindOrganizationUnitDelivery: func(ctx context.Context) (organizationunit.Delivery, error) {
				if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
					t.Fatal("Organization Unit delivery was not bound to the Action transaction")
				}
				return delivery, nil
			},
			WorkspaceCommercialConfiguration: workspaceCommercialConfigurationLockerFunc(func(_ context.Context, workspaceID string) (WorkspaceCommercialConfiguration, error) {
				lockerCalls++
				if workspaceID != "workspace-a" {
					t.Fatalf("workspace=%q", workspaceID)
				}
				// max_stores is intentionally already exhausted. Generic department
				// delivery reads only the trusted company parent and never store quota.
				return WorkspaceCommercialConfiguration{MaxStores: 1, CompanyOrganizationID: "company-hq"}, nil
			}),
		},
		identity: runtimeext.ExecutionIdentity{ExecutionID: executionID}, workspace: runtimeext.Workspace{ID: "workspace-a"},
		invocation:  actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator", AccessBundle: bundle}}},
		action:      definitionmodel.ActionSchema{Key: "department.provision", ObjectKey: "department_profile"},
		unitOfWork:  unitOfWork,
		targetGrant: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceDeliveredOrganizationUnit},
		organizationUnitGrant: &runtimeext.OrganizationUnitDeliveryCapability{
			Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
			NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
			ParentSource: runtimeext.OrganizationUnitParentSourceWorkspaceCompany,
		},
		requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
	}
	request := runtimeext.OrganizationUnitDeliveryRequest{Code: " SALES ", Name: " Sales ", NodeType: runtimeext.OrganizationUnitNodeTypeDepartment, SortOrder: 20}
	result, err := execution.CreateOrganizationUnit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Organization.ID != wantID || result.Organization.NodeType != runtimeext.OrganizationUnitNodeTypeDepartment || result.Version != 1 || lockerCalls != 1 || len(delivery.createRequests) != 1 {
		t.Fatalf("result=%#v lockerCalls=%d requests=%#v", result, lockerCalls, delivery.createRequests)
	}
	bound := delivery.createRequests[0]
	if bound.AccessToken != "trusted-token" || bound.Organization.ParentOrganizationID != "company-hq" || bound.Organization.OrganizationID != wantID || bound.Organization.NodeType != organizationunit.NodeTypeDepartment || bound.Organization.SortOrder != 20 || bound.Organization.ExpectedVersion != 0 {
		t.Fatalf("Identity request=%#v", bound)
	}
	if target, ok := execution.TargetOrganization(); !ok || target.ID != wantID || execution.mutationPrincipal == nil || len(execution.mutationPrincipal.AccessBundle.DataPolicies) != 1 || execution.mutationPrincipal.AccessBundle.DataPolicies[0].Key != "runtime.delivered_organization_unit."+executionID {
		t.Fatalf("target=%#v ok=%v mutationPrincipal=%#v", target, ok, execution.mutationPrincipal)
	}
	if replay, replayErr := execution.CreateOrganizationUnit(t.Context(), request); replayErr != nil || replay != result || len(delivery.createRequests) != 1 {
		t.Fatalf("same-call replay=%#v calls=%d err=%v", replay, len(delivery.createRequests), replayErr)
	}
	changed := request
	changed.Name = "Other"
	if _, changedErr := execution.CreateOrganizationUnit(t.Context(), changed); apperror.CodeOf(changedErr) != "backend.idempotency_key_reused" {
		t.Fatalf("changed request error=%v", changedErr)
	}
	unitOfWork.rollBack(t.Context())
}

func TestOrganizationUnitNestedCreateKeepsParentTargetAndReturnsOpaqueChild(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "department_profile.add_team", ObjectKey: "department_profile"}
	principal := accessfixture.Attach(
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}},
		accessfixture.Bundle{Permissions: []string{action.Key}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{action.Key}, identitysdk.DataScopeAll)},
	)
	executionID := "execution-team-platform"
	wantChildID := stableOrganizationUnitID("workspace-a", executionID)
	delivery := &organizationUnitDeliveryStub{createResult: organizationunit.DeliveryResult{
		DeliveryID: "delivery-team-platform",
		Organization: organizationunit.DeliveredOrganizationUnit{
			ID: wantChildID, Code: "PLATFORM", Name: "Platform", NodeType: organizationunit.NodeTypeTeam,
			Status: "active", ParentOrganizationID: "department-engineering", SortOrder: 10, Version: 1,
		},
	}}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{BindOrganizationUnitDelivery: func(context.Context) (organizationunit.Delivery, error) { return delivery, nil }},
		identity:     runtimeext.ExecutionIdentity{ExecutionID: executionID}, workspace: runtimeext.Workspace{ID: "workspace-a"},
		invocation: actionmodel.ActionInvocation{Principal: principal, TargetOrganizationID: "department-engineering"}, action: action,
		unitOfWork: newActionTestUnitOfWork(),
		targetGrant: &runtimeext.ActionTargetOrganizationCapability{
			Source: runtimeext.TargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation,
		},
		organizationUnitGrant: &runtimeext.OrganizationUnitDeliveryCapability{
			Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
			NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeTeam},
			ParentSource: runtimeext.OrganizationUnitParentSourceTargetOrganization,
		},
		requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
	}
	if err := execution.initializeTargetOrganization(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := execution.CreateOrganizationUnit(t.Context(), runtimeext.OrganizationUnitDeliveryRequest{
		Code: "PLATFORM", Name: "Platform", NodeType: runtimeext.OrganizationUnitNodeTypeTeam, SortOrder: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Organization.ID != wantChildID || len(delivery.createRequests) != 1 || delivery.createRequests[0].Organization.ParentOrganizationID != "department-engineering" {
		t.Fatalf("result=%#v requests=%#v", result, delivery.createRequests)
	}
	if target, ok := execution.TargetOrganization(); !ok || target.ID != "department-engineering" || execution.mutationPrincipal != nil {
		t.Fatalf("nested create retargeted Action: target=%#v ok=%v mutationPrincipal=%#v", target, ok, execution.mutationPrincipal)
	}

	mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
		WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction, ActionKey: action.Key,
		CorrelationID: "correlation", ApplicationSchemaRevision: "schema",
	})
	if err != nil {
		t.Fatal(err)
	}
	planForOwner := func(ownerID string) transactionmodel.MutationPlan {
		plan, planErr := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
			Operation: "create", Object: definitionmodel.ObjectSchema{Key: "department_profile"},
			Record: recordmodel.Record{ID: "record-" + ownerID, OwnerOrgID: ownerID},
		}, nil)
		if planErr != nil {
			t.Fatal(planErr)
		}
		return plan
	}
	if err := execution.validateMutationTarget([]transactionmodel.MutationPlan{planForOwner("department-engineering")}); err != nil {
		t.Fatalf("parent-owned mutation rejected: %v", err)
	}
	if err := execution.validateMutationTarget([]transactionmodel.MutationPlan{planForOwner(wantChildID)}); apperror.CodeOf(err) != "backend.action.mixed_target_organization" {
		t.Fatalf("child-owned mutation escaped fixed parent target: %v", err)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestOrganizationUnitDeliveryRejectsTamperedCreateReceipt(t *testing.T) {
	executionID := "execution-department-sales"
	wantID := stableOrganizationUnitID("workspace-a", executionID)
	base := organizationunit.DeliveryResult{DeliveryID: "delivery-department-sales", Organization: organizationunit.DeliveredOrganizationUnit{
		ID: wantID, Code: "SALES", Name: "Sales", NodeType: organizationunit.NodeTypeDepartment,
		Status: "active", ParentOrganizationID: "company-hq", SortOrder: 20, Version: 1,
	}}
	for _, test := range []struct {
		name   string
		mutate func(*organizationunit.DeliveryResult)
	}{
		{name: "sort order mismatch", mutate: func(result *organizationunit.DeliveryResult) { result.Organization.SortOrder = 21 }},
		{name: "version mismatch", mutate: func(result *organizationunit.DeliveryResult) { result.Organization.Version = 2 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := base
			test.mutate(&result)
			delivery := &organizationUnitDeliveryStub{createResult: result}
			execution := &businessActionExecution{
				dependencies: BusinessHandlerExecutionDependencies{
					BindOrganizationUnitDelivery: func(context.Context) (organizationunit.Delivery, error) { return delivery, nil },
					WorkspaceCommercialConfiguration: workspaceCommercialConfigurationLockerFunc(func(context.Context, string) (WorkspaceCommercialConfiguration, error) {
						return WorkspaceCommercialConfiguration{CompanyOrganizationID: "company-hq"}, nil
					}),
				},
				identity: runtimeext.ExecutionIdentity{ExecutionID: executionID}, workspace: runtimeext.Workspace{ID: "workspace-a"},
				unitOfWork:  newActionTestUnitOfWork(),
				targetGrant: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceDeliveredOrganizationUnit},
				organizationUnitGrant: &runtimeext.OrganizationUnitDeliveryCapability{
					Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
					NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
					ParentSource: runtimeext.OrganizationUnitParentSourceWorkspaceCompany,
				},
				requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
			}
			_, err := execution.CreateOrganizationUnit(t.Context(), runtimeext.OrganizationUnitDeliveryRequest{
				Code: "SALES", Name: "Sales", NodeType: runtimeext.OrganizationUnitNodeTypeDepartment, SortOrder: 20,
			})
			if apperror.CodeOf(err) != "backend.action.organization_unit_delivery_identity_mismatch" || execution.targetResolved || execution.organizationUnitResult.Organization.ID != "" {
				t.Fatalf("error=%v targetResolved=%v result=%#v", err, execution.targetResolved, execution.organizationUnitResult)
			}
			execution.unitOfWork.rollBack(t.Context())
		})
	}
}

func TestOrganizationUnitDeliveryRejectsStoreAndCallerAuthorityFields(t *testing.T) {
	capability := runtimeext.OrganizationUnitDeliveryCapability{
		Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
		NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
		ParentSource: runtimeext.OrganizationUnitParentSourceWorkspaceCompany,
	}
	execution := &businessActionExecution{organizationUnitGrant: &capability}
	if _, err := execution.CreateOrganizationUnit(t.Context(), runtimeext.OrganizationUnitDeliveryRequest{Code: "STORE", Name: "Store", NodeType: runtimeext.OrganizationUnitNodeType("store")}); apperror.CodeOf(err) != "backend.action.organization_unit_delivery_grant_denied" {
		t.Fatalf("store delivery error=%v", err)
	}
	for _, value := range []any{runtimeext.OrganizationUnitDeliveryRequest{}, runtimeext.OrganizationUnitResolveRequest{}} {
		typeOf := reflect.TypeOf(value)
		for _, forbidden := range []string{"WorkspaceID", "AccessToken", "Bearer", "OrganizationID", "ParentOrganizationID", "OwnerOrgID"} {
			if _, found := typeOf.FieldByName(forbidden); found {
				t.Fatalf("%s exposes caller authority %s", typeOf.Name(), forbidden)
			}
		}
	}
}

func TestOrganizationUnitDeliveryResolvesOnlyFixedTargetAndGrantedNodeType(t *testing.T) {
	delivery := &organizationUnitDeliveryStub{resolveResult: organizationunit.DeliveredOrganizationUnit{
		ID: "department-sales", NodeType: organizationunit.NodeTypeDepartment, Status: "active",
		ParentOrganizationID: "company-hq", Version: 1,
	}}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{BindOrganizationUnitDelivery: func(ctx context.Context) (organizationunit.Delivery, error) {
			if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
				t.Fatal("resolve was not bound to the Action transaction")
			}
			return delivery, nil
		}},
		unitOfWork: newActionTestUnitOfWork(), organizationUnitTargetID: "department-sales",
		targetGrant: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit},
		organizationUnitGrant: &runtimeext.OrganizationUnitDeliveryCapability{
			Operations: []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryResolve},
			NodeTypes:  []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
		},
		requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
	}
	request := runtimeext.OrganizationUnitResolveRequest{NodeType: runtimeext.OrganizationUnitNodeTypeDepartment}
	result, err := execution.ResolveOrganizationUnit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Organization.ID != "department-sales" || result.Version != 1 || len(delivery.resolveRequests) != 1 {
		t.Fatalf("result=%#v requests=%#v", result, delivery.resolveRequests)
	}
	bound := delivery.resolveRequests[0]
	if bound.OrganizationID != "department-sales" || bound.NodeType != organizationunit.NodeTypeDepartment || bound.AccessToken != "trusted-token" {
		t.Fatalf("Identity resolve=%#v", bound)
	}
	if target, ok := execution.TargetOrganization(); !ok || target.ID != "department-sales" {
		t.Fatalf("target=%#v ok=%v", target, ok)
	}
	if _, wrongErr := execution.ResolveOrganizationUnit(t.Context(), runtimeext.OrganizationUnitResolveRequest{NodeType: runtimeext.OrganizationUnitNodeTypeTeam}); apperror.CodeOf(wrongErr) != "backend.action.organization_unit_delivery_grant_denied" {
		t.Fatalf("wrong type error=%v", wrongErr)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestOrganizationUnitDeliveryFailsClosedOnIdentityScopeAndResponseMismatch(t *testing.T) {
	grant := &runtimeext.OrganizationUnitDeliveryCapability{
		Operations: []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryResolve},
		NodeTypes:  []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
	}
	for _, test := range []struct {
		name     string
		delivery *organizationUnitDeliveryStub
		wantCode string
	}{
		{name: "cross workspace or permission denied", delivery: &organizationUnitDeliveryStub{err: &identitysdk.Error{Code: "identity.organization_unit_delivery_application_scope_mismatch"}}, wantCode: "identity.organization_unit_delivery_application_scope_mismatch"},
		{name: "response id mismatch", delivery: &organizationUnitDeliveryStub{resolveResult: organizationunit.DeliveredOrganizationUnit{ID: "other", NodeType: organizationunit.NodeTypeDepartment, Status: "active", ParentOrganizationID: "company", Version: 1}}, wantCode: "backend.action.organization_unit_delivery_identity_mismatch"},
		{name: "response version mismatch", delivery: &organizationUnitDeliveryStub{resolveResult: organizationunit.DeliveredOrganizationUnit{ID: "department-sales", NodeType: organizationunit.NodeTypeDepartment, Status: "active", ParentOrganizationID: "company", Version: 2}}, wantCode: "backend.action.organization_unit_delivery_identity_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			execution := &businessActionExecution{
				dependencies: BusinessHandlerExecutionDependencies{BindOrganizationUnitDelivery: func(context.Context) (organizationunit.Delivery, error) { return test.delivery, nil }},
				unitOfWork:   newActionTestUnitOfWork(), organizationUnitTargetID: "department-sales", organizationUnitGrant: grant,
				requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
			}
			_, err := execution.ResolveOrganizationUnit(t.Context(), runtimeext.OrganizationUnitResolveRequest{NodeType: runtimeext.OrganizationUnitNodeTypeDepartment})
			if apperror.CodeOf(err) != test.wantCode || execution.targetResolved {
				t.Fatalf("error=%v targetResolved=%v", err, execution.targetResolved)
			}
			execution.unitOfWork.rollBack(t.Context())
		})
	}
}

func TestOrganizationUnitDeliveryPreservesIdentityCreateConflictAndAuthorityErrors(t *testing.T) {
	for _, code := range []string{
		"backend.identity.organization_unit_code_exists",
		"backend.identity.organization_unit_type_invalid",
		"backend.identity.organization_unit_parent_invalid",
		"backend.identity.organization_unit_scope_denied",
		"backend.identity.organization_unit_version_conflict",
		"backend.identity.organization_unit_external_change",
		"identity.organization_unit_delivery_application_scope_mismatch",
		"auth.authorization_stale",
	} {
		t.Run(code, func(t *testing.T) {
			delivery := &organizationUnitDeliveryStub{err: &identitysdk.Error{Code: code}}
			execution := &businessActionExecution{
				dependencies: BusinessHandlerExecutionDependencies{
					BindOrganizationUnitDelivery: func(context.Context) (organizationunit.Delivery, error) { return delivery, nil },
					WorkspaceCommercialConfiguration: workspaceCommercialConfigurationLockerFunc(func(context.Context, string) (WorkspaceCommercialConfiguration, error) {
						return WorkspaceCommercialConfiguration{CompanyOrganizationID: "company-hq"}, nil
					}),
				},
				identity: runtimeext.ExecutionIdentity{ExecutionID: "execution-error"}, workspace: runtimeext.Workspace{ID: "workspace-a"},
				unitOfWork: newActionTestUnitOfWork(),
				organizationUnitGrant: &runtimeext.OrganizationUnitDeliveryCapability{
					Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
					NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
					ParentSource: runtimeext.OrganizationUnitParentSourceWorkspaceCompany,
				},
				requestIdentity: identitysdk.RequestIdentity{AccessToken: "trusted-token"},
			}
			_, err := execution.CreateOrganizationUnit(t.Context(), runtimeext.OrganizationUnitDeliveryRequest{
				Code: "SALES", Name: "Sales", NodeType: runtimeext.OrganizationUnitNodeTypeDepartment,
			})
			if apperror.CodeOf(err) != code || execution.targetResolved || execution.organizationUnitResult.Organization.ID != "" {
				t.Fatalf("error=%v targetResolved=%v result=%#v", err, execution.targetResolved, execution.organizationUnitResult)
			}
			execution.unitOfWork.rollBack(t.Context())
		})
	}
}

func TestOrganizationUnitDeliveryCapabilityMustCompleteBeforeActionCommit(t *testing.T) {
	execution := &businessActionExecution{organizationUnitGrant: &runtimeext.OrganizationUnitDeliveryCapability{}}
	if err := execution.validateCapabilityCompletion(nil); apperror.CodeOf(err) != "backend.action.organization_unit_delivery_incomplete" {
		t.Fatalf("incomplete capability error=%v", err)
	}
	execution.organizationUnitResult.Organization.ID = "department-sales"
	if err := execution.validateCapabilityCompletion(nil); err != nil {
		t.Fatalf("completed capability error=%v", err)
	}
}
