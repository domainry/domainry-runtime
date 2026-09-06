package action

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestIdentityInitialCredentialIsAbsentBeforeCommitAndFromReplayState(t *testing.T) {
	newExecution := func() *businessActionExecution {
		return &businessActionExecution{
			identityGrant: &runtimeext.IdentityHandlerDeliveryCapability{InitialCredentialOutputField: "initial_credential"},
			initialCredential: &identitysdk.HandlerInitialCredential{
				InitialPassword: "one-time-secret", MustChangePassword: true, NoStore: true,
			},
		}
	}
	preCommit := actionmodel.ActionInvocationResult{Status: "success", Record: &actionmodel.ActionResult{Output: map[string]any{"employee_id": "employee-1"}}}
	receiptJSON, err := json.Marshal(preCommit)
	if err != nil {
		t.Fatal(err)
	}
	audit := buildActionSuccessAudit(t.Context(), definitionmodel.ActionSchema{Key: "employee.invite"}, actionmodel.ActionInvocation{}, preCommit)
	auditJSON, err := json.Marshal(audit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(receiptJSON), "one-time-secret") || strings.Contains(string(auditJSON), "one-time-secret") {
		t.Fatalf("pre-commit receipt/audit leaked credential: receipt=%s audit=%s", receiptJSON, auditJSON)
	}

	// Rollback and commit-unknown paths never invoke PostCommit; the response
	// assembled from their pre-commit state therefore cannot contain a secret.
	for _, outcome := range []string{"rollback", "commit_unknown"} {
		execution := newExecution()
		result := preCommit
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil || strings.Contains(string(encoded), "one-time-secret") || result.NoStore {
			t.Fatalf("%s result=%#v json=%s err=%v", outcome, result, encoded, marshalErr)
		}
		if execution.initialCredential == nil {
			t.Fatalf("%s unexpectedly materialized or cleared volatile credential", outcome)
		}
	}

	execution := newExecution()
	committed := preCommit
	execution.postCommit(&committed)
	credential, ok := committed.Record.Output["initial_credential"].(map[string]any)
	if !ok || credential["initial_password"] != "one-time-secret" || credential["must_change_password"] != true || !committed.NoStore || !committed.Record.NoStore || execution.initialCredential != nil {
		t.Fatalf("committed=%#v volatile=%#v", committed, execution.initialCredential)
	}

	// A replay is reconstructed from the committed receipt captured before the
	// volatile post-commit injection and has no callback to regenerate it.
	var replay actionmodel.ActionInvocationResult
	if err := json.Unmarshal(receiptJSON, &replay); err != nil {
		t.Fatal(err)
	}
	if _, found := replay.Record.Output["initial_credential"]; found || replay.NoStore || strings.Contains(string(receiptJSON), "one-time-secret") {
		t.Fatalf("replay leaked credential: %#v", replay)
	}
}

type identityHandlerDeliveryStub struct {
	request         identitysdk.HandlerDeliveryRequest
	result          identitysdk.HandlerDeliveryResult
	err             error
	calls           int
	resolveRequest  identitysdk.HandlerBoundIdentityRequest
	resolveRequests []identitysdk.HandlerBoundIdentityRequest
	resolveResult   identitysdk.HandlerBoundIdentity
	resolveErr      error
	resolveCalls    int
}

func identityDeliveryMutationContext(t *testing.T) transactionmodel.MutationContext {
	t.Helper()
	value, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
		WorkspaceID: "workspace-a", Source: transactionmodel.MutationSourceAction, ActionKey: "employee.create",
		CorrelationID: "correlation-1", ApplicationSchemaRevision: "schema-1", EffectAuthority: map[string][]string{"employee_profile": {"*"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func (stub *identityHandlerDeliveryStub) DeliverIdentity(_ context.Context, request identitysdk.HandlerDeliveryRequest) (identitysdk.HandlerDeliveryResult, error) {
	stub.calls++
	stub.request = request
	return stub.result, stub.err
}

func (stub *identityHandlerDeliveryStub) ResolveBoundIdentity(_ context.Context, request identitysdk.HandlerBoundIdentityRequest) (identitysdk.HandlerBoundIdentity, error) {
	stub.resolveCalls++
	stub.resolveRequest = request
	stub.resolveRequests = append(stub.resolveRequests, request)
	result := stub.resolveResult
	if strings.TrimSpace(result.UserID) == "" {
		result.UserID = request.UserID
	}
	return result, stub.resolveErr
}

func TestIdentityBoundProfileResolutionInjectsStaticBindingAndReturnsCurrentCAS(t *testing.T) {
	delivery := &identityHandlerDeliveryStub{resolveResult: identitysdk.HandlerBoundIdentity{
		UserID: "identity-user-1", DisplayName: "Employee One", Active: true, Version: 4,
		ProfileBinding: &identitysdk.HandlerProfileBinding{
			BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "employee-profile-1",
			IdentityUserID: "identity-user-1", Status: "active", Version: 7,
		},
	}}
	execution := identityDeliveryTestExecution(t, delivery)
	execution.identityGrant.Operations = append(execution.identityGrant.Operations, runtimeext.IdentityHandlerResolve)

	resolved, err := execution.ResolveBoundIdentityProfile(t.Context(), "identity-user-1", runtimeext.IdentityHandlerProfileBindingSelector{
		BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "employee-profile-1",
	})
	if err != nil || resolved.ProfileBinding == nil || resolved.ProfileBinding.Version != 7 {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if delivery.resolveCalls != 1 || delivery.resolveRequest.AccessToken != "trusted-token" || delivery.resolveRequest.ProfileBinding == nil ||
		delivery.resolveRequest.ProfileBinding.BindingKey != "employee" || delivery.resolveRequest.ProfileBinding.ObjectKey != "employee_profile" ||
		delivery.resolveRequest.ProfileBinding.ProfileID != "employee-profile-1" {
		t.Fatalf("resolve request=%+v calls=%d", delivery.resolveRequest, delivery.resolveCalls)
	}
	execution.unitOfWork.rollBack(t.Context())

	denied := identityDeliveryTestExecution(t, delivery)
	denied.identityGrant.Operations = append(denied.identityGrant.Operations, runtimeext.IdentityHandlerResolve)
	if _, err := denied.ResolveBoundIdentityProfile(t.Context(), "identity-user-1", runtimeext.IdentityHandlerProfileBindingSelector{
		BindingKey: "other", ObjectKey: "other_profile", ProfileID: "profile-1",
	}); apperror.CodeOf(err) != "identity.handler_delivery_profile_binding_denied" || delivery.resolveCalls != 1 {
		t.Fatalf("ungranted selector reached Identity: calls=%d err=%v", delivery.resolveCalls, err)
	}

	mismatch := identityDeliveryTestExecution(t, delivery)
	mismatch.identityGrant.Operations = append(mismatch.identityGrant.Operations, runtimeext.IdentityHandlerResolve)
	delivery.resolveResult.ProfileBinding.ProfileID = "different-profile"
	if _, err := mismatch.ResolveBoundIdentityProfile(t.Context(), "identity-user-1", runtimeext.IdentityHandlerProfileBindingSelector{
		BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "employee-profile-1",
	}); apperror.CodeOf(err) != "identity.handler_delivery_profile_resolution_mismatch" {
		t.Fatalf("mismatched Identity projection err=%v", err)
	}
	mismatch.unitOfWork.rollBack(t.Context())
}

func TestIdentityBoundIdentityBatchUsesOneActionTransactionAndPreservesOrder(t *testing.T) {
	delivery := &identityHandlerDeliveryStub{}
	execution := identityDeliveryTestExecution(t, delivery)
	execution.identityGrant.Operations = append(execution.identityGrant.Operations, runtimeext.IdentityHandlerResolve)
	delivery.resolveResult = identitysdk.HandlerBoundIdentity{DisplayName: "resolved", Active: true, Version: 1}

	results, err := execution.ResolveBoundIdentities(t.Context(), []string{" user-2 ", "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].UserID != "user-2" || results[1].UserID != "user-1" || delivery.resolveCalls != 2 || execution.boundIdentityCalls != 2 || len(delivery.resolveRequests) != 2 || delivery.resolveRequests[0].UserID != "user-2" || delivery.resolveRequests[1].UserID != "user-1" {
		t.Fatalf("results=%+v delivery calls=%d execution calls=%d last request=%+v", results, delivery.resolveCalls, execution.boundIdentityCalls, delivery.resolveRequest)
	}
	if results[0].DisplayName != "resolved" || results[1].DisplayName != "resolved" {
		t.Fatalf("result order/projection=%+v", results)
	}
	execution.unitOfWork.rollBack(t.Context())

	invalid := identityDeliveryTestExecution(t, delivery)
	invalid.identityGrant.Operations = append(invalid.identityGrant.Operations, runtimeext.IdentityHandlerResolve)
	if _, err := invalid.ResolveBoundIdentities(t.Context(), []string{"user-1", " user-1 "}); apperror.CodeOf(err) != "identity.handler_delivery_resolve_batch_invalid" {
		t.Fatalf("duplicate batch error=%v", err)
	}
	if delivery.resolveCalls != 2 {
		t.Fatalf("invalid batch reached Identity: calls=%d", delivery.resolveCalls)
	}
}

func identityDeliveryTestExecution(t *testing.T, delivery identitysdk.HandlerDelivery) *businessActionExecution {
	t.Helper()
	principal := workspaceAggregatePrincipal("employee.create")
	principal.UserID = "actor-1"
	unitOfWork := newActionTestUnitOfWork()
	unitOfWork.claim = actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "execution-identity-1", LeaseOwner: "test", FencingToken: 1}}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			BindIdentityHandlerDelivery: func(ctx context.Context) (identitysdk.HandlerDelivery, error) {
				if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
					t.Fatal("Identity delivery was not bound to the Action transaction")
				}
				return delivery, nil
			},
			ResolveProfileBindingField: func(bindingKey, objectKey string) (string, bool) {
				return "identity_user_id", bindingKey == "employee" && objectKey == "employee_profile"
			},
		},
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "execution-identity-1"},
		workspace: runtimeext.Workspace{ID: "workspace-a"}, invocation: actionmodel.ActionInvocation{Principal: principal, IdempotencyKey: "request-1", Source: actionmodel.ActionSourceHTTP},
		action:      definitionmodel.ActionSchema{Key: "employee.create", ObjectKey: "employee_profile", EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "employee_profile"}}, Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "employee_profile", Fields: []string{"name", "status"}}}}},
		unitOfWork:  unitOfWork,
		targetGrant: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit}, targetOrganization: runtimeext.TargetOrganization{ID: "store-north"}, targetResolved: true,
		identityGrant:   &runtimeext.IdentityHandlerDeliveryCapability{Operations: []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerCreate, runtimeext.IdentityHandlerUpdate, runtimeext.IdentityHandlerDisable}, ProfileBindings: []runtimeext.IdentityProfileBindingCapability{{BindingKey: "employee", ObjectKey: "employee_profile"}}, InitialCredentialOutputField: "initial_credential"},
		requestIdentity: identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "actor-1"}, AccessToken: "trusted-token"},
	}
	return execution
}

func TestIdentityDeliveryAtomicallyInjectsRequiredRelationIntoStagedProfileCreate(t *testing.T) {
	delivery := &identityHandlerDeliveryStub{}
	execution := identityDeliveryTestExecution(t, delivery)
	var plannedFields map[string]any
	var plannedRecordID string
	execution.dependencies.PlanCreateMutation = func(ctx context.Context, objectKey string, fields map[string]any, recordID string, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
			t.Fatal("profile CREATE was planned outside the Action transaction")
		}
		plannedFields = recordvalidation.RecordCloneData(fields)
		plannedRecordID = recordID
		invocation, ok := recordmutation.MutationInvocationFromContext(ctx)
		if !ok || len(invocation.ProfileBindingAuthorities) != 1 || invocation.ProfileBindingAuthorities[0].ProfileID != recordID || invocation.TargetOrganizationID != "store-north" {
			t.Fatalf("trusted profile create authority=%#v", invocation)
		}
		record := recordmodel.Record{ID: recordID, OwnerOrgID: "store-north", Data: recordvalidation.RecordCloneData(fields), UpdatedAt: "2026-09-06T00:00:00Z"}
		plan, err := transactionmodel.NewMutationPlan(identityDeliveryMutationContext(t), transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: objectKey}, Record: record}, nil)
		return plan, record, err
	}
	profileID := stableIdentityProfileID("workspace-a", "execution-identity-1", "employee_profile")
	userID := stableIdentityUserID("workspace-a", "execution-identity-1")
	delivery.result = identitysdk.HandlerDeliveryResult{
		DeliveryID: "delivery-1", User: identitysdk.User{ID: userID, Name: "Employee", OrgID: "store-north"},
		ProfileBinding: &identitysdk.HandlerProfileBinding{BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: profileID, IdentityUserID: userID, Status: "active", Version: 1},
	}
	result, err := execution.DeliverIdentity(t.Context(), runtimeext.IdentityHandlerDeliveryRequest{
		User:           runtimeext.IdentityHandlerUserMutation{Operation: runtimeext.IdentityHandlerCreate, User: runtimeext.IdentityUser{Name: "Employee"}, LoginMode: runtimeext.IdentityHandlerLoginNone},
		ProfileBinding: &runtimeext.IdentityHandlerProfileBindingMutation{BindingKey: "employee", ObjectKey: "employee_profile", CreateProfileFields: map[string]any{"name": "Employee", "status": "active"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plannedRecordID != profileID || plannedFields["identity_user_id"] != userID || len(execution.plans) != 1 || len(execution.created) != 1 {
		t.Fatalf("profileID=%q fields=%#v plans=%d created=%#v", plannedRecordID, plannedFields, len(execution.plans), execution.created)
	}
	if delivery.request.ProfileBinding.ProfileID != profileID || delivery.request.ProfileBinding.EmbeddedProfileRecord["identity_user_id"] != nil || delivery.request.ProfileBinding.EmbeddedProfileRecord["status"] != "active" {
		t.Fatalf("Identity trusted pre-binding projection=%#v", delivery.request.ProfileBinding)
	}
	if result.ProfileBinding == nil || result.ProfileBinding.ProfileID != profileID || !execution.identityDeliveryOK {
		t.Fatalf("result=%#v deliveryOK=%v", result, execution.identityDeliveryOK)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestIdentityDeliveryRejectsProjectOwnedIdentityFieldsAndProjectRelationField(t *testing.T) {
	delivery := &identityHandlerDeliveryStub{}
	execution := identityDeliveryTestExecution(t, delivery)
	request := runtimeext.IdentityHandlerDeliveryRequest{User: runtimeext.IdentityHandlerUserMutation{Operation: runtimeext.IdentityHandlerCreate, User: runtimeext.IdentityUser{ID: "identity-user-1", OrgID: "store-sibling"}, LoginMode: runtimeext.IdentityHandlerLoginNone}}
	if _, err := execution.DeliverIdentity(t.Context(), request); apperror.CodeOf(err) != "identity.handler_delivery_runtime_owned_identity_fields" || delivery.calls != 0 {
		t.Fatalf("project-owned Identity fields calls=%d err=%v", delivery.calls, err)
	}

	execution = identityDeliveryTestExecution(t, delivery)
	execution.dependencies.PlanCreateMutation = func(context.Context, string, map[string]any, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		t.Fatal("project-supplied relation reached create planner")
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, nil
	}
	request = runtimeext.IdentityHandlerDeliveryRequest{
		User:           runtimeext.IdentityHandlerUserMutation{Operation: runtimeext.IdentityHandlerCreate, User: runtimeext.IdentityUser{}, LoginMode: runtimeext.IdentityHandlerLoginNone},
		ProfileBinding: &runtimeext.IdentityHandlerProfileBindingMutation{BindingKey: "employee", ObjectKey: "employee_profile", CreateProfileFields: map[string]any{"identity_user_id": "forged"}},
	}
	if _, err := execution.DeliverIdentity(t.Context(), request); apperror.CodeOf(err) != "identity.handler_delivery_profile_relation_field_forbidden" || delivery.calls != 0 {
		t.Fatalf("project relation calls=%d err=%v", delivery.calls, err)
	}
}

func TestIdentityDeliveryExistingProfileRelationIsVerifiedWithoutRedundantWrite(t *testing.T) {
	delivery := &identityHandlerDeliveryStub{result: identitysdk.HandlerDeliveryResult{
		DeliveryID: "delivery-update", User: identitysdk.User{ID: "identity-user-1", OrgID: "store-north", Version: 4},
		ProfileBinding: &identitysdk.HandlerProfileBinding{BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "employee-profile-1", IdentityUserID: "identity-user-1", Status: "active", Version: 2},
	}}
	execution := identityDeliveryTestExecution(t, delivery)
	execution.dependencies.GetRecordForUpdate = func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
		return recordmodel.Record{ID: "employee-profile-1", OwnerOrgID: "store-north", UpdatedAt: "2026-09-06T00:00:00Z", Data: map[string]any{"identity_user_id": "identity-user-1"}}, nil
	}
	execution.dependencies.PlanConditionalUpdate = func(context.Context, string, string, transactionmodel.ConditionalUpdateInput, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		t.Fatal("an already-consistent profile relation must not be rewritten")
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, nil
	}
	_, err := execution.DeliverIdentity(t.Context(), runtimeext.IdentityHandlerDeliveryRequest{
		User:           runtimeext.IdentityHandlerUserMutation{Operation: runtimeext.IdentityHandlerUpdate, User: runtimeext.IdentityUser{}, ExpectedVersion: 3},
		ProfileBinding: &runtimeext.IdentityHandlerProfileBindingMutation{BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "employee-profile-1", ExpectedVersion: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.plans) != 0 || len(execution.updated) != 0 {
		t.Fatalf("Identity-only update staged Runtime profile writes: plans=%d updated=%#v", len(execution.plans), execution.updated)
	}
	execution.unitOfWork.rollBack(t.Context())
}

func TestIdentityDeliveryAndBusinessProfileCASUseOneCanonicalTargetMutation(t *testing.T) {
	for _, operation := range []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerUpdate, runtimeext.IdentityHandlerDisable} {
		t.Run(string(operation), func(t *testing.T) {
			store := &actionUnitOfWorkStoreProbe{}
			identityCommitted := false
			store.onCommit = func() { identityCommitted = true }
			delivery := successfulExistingProfileDelivery(operation)
			execution := identityDeliveryTestExecution(t, delivery)
			execution.unitOfWork = identityDeliveryTestUnitOfWork(store)
			configureIdentityBusinessProfileAction(execution)
			execution.dependencies.GetRecordForUpdate = lockedIdentityProfile

			businessField, businessValue := "rank", any("senior")
			if operation == runtimeext.IdentityHandlerDisable {
				businessField, businessValue = "employment_status", "disabled"
			}
			plannerCalls := 0
			execution.dependencies.PlanConditionalUpdate = func(ctx context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				plannerCalls++
				if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true || objectKey != "employee_profile" || recordID != "employee-profile-1" || input.Patch[businessField] != businessValue {
					t.Fatalf("business planner context/object/input: object=%q record=%q input=%#v", objectKey, recordID, input)
				}
				if _, rewritesRelation := input.Patch["identity_user_id"]; rewritesRelation {
					t.Fatalf("business mutation unexpectedly rewrote the verified relation: %#v", input.Patch)
				}
				record := recordmodel.Record{ID: recordID, OwnerOrgID: "store-north", UpdatedAt: "2026-09-06T01:00:00Z", Data: map[string]any{
					"identity_user_id": "identity-user-1", businessField: businessValue,
				}}
				object := definitionmodel.ObjectSchema{Key: objectKey, Fields: []definitionmodel.FieldSchema{{Key: "rank"}, {Key: "employment_status"}}}
				commit := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, RecordID: recordID, Record: record, Predicates: append([]transactionmodel.MutationPredicate(nil), input.Predicates...)}
				plan, err := transactionmodel.NewMutationPlan(identityDeliveryMutationContext(t), commit, map[string]any{"identity_user_id": "identity-user-1"})
				return plan, record, err
			}

			if _, err := execution.DeliverIdentity(t.Context(), existingProfileDeliveryRequest(operation)); err != nil {
				t.Fatal(err)
			}
			if _, err := execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{
				Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "employee_profile", RecordID: "employee-profile-1",
				Fields: map[string]any{businessField: businessValue}, ExpectedUpdatedAt: "2026-09-06T00:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			commits, err := execution.canonicalCommits()
			if err != nil {
				t.Fatal(err)
			}
			commits, err = enforceActionOptimisticConcurrency(execution.action, execution.invocation, commits)
			if err != nil {
				t.Fatal(err)
			}
			if len(commits) != 1 || plannerCalls != 1 || commits[0].RecordID != "employee-profile-1" || commits[0].Optimistic.ExpectedUpdatedAt != "2026-09-06T00:00:00Z" {
				t.Fatalf("canonical commits=%#v plannerCalls=%d", commits, plannerCalls)
			}
			if err := execution.unitOfWork.commit(t.Context(), map[string]any{"status": "success"}, commits, nil); err != nil {
				t.Fatal(err)
			}
			if !identityCommitted || len(store.commits) != 1 || len(store.commits[0]) != 1 || store.rollbackCalls != 0 {
				t.Fatalf("identityCommitted=%v commits=%#v rollbacks=%d", identityCommitted, store.commits, store.rollbackCalls)
			}
		})
	}
}

func TestIdentityDeliveryAndBusinessProfileCASFailuresHaveZeroCommittedEffects(t *testing.T) {
	for _, operation := range []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerUpdate, runtimeext.IdentityHandlerDisable} {
		for _, failure := range []string{"identity", "stale_profile"} {
			t.Run(string(operation)+"/"+failure, func(t *testing.T) {
				store := &actionUnitOfWorkStoreProbe{}
				identityCommitted := false
				store.onCommit = func() { identityCommitted = true }
				delivery := successfulExistingProfileDelivery(operation)
				if failure == "identity" {
					delivery.err = &identitysdk.Error{Code: "identity.handler_delivery.version_conflict"}
				}
				execution := identityDeliveryTestExecution(t, delivery)
				execution.unitOfWork = identityDeliveryTestUnitOfWork(store)
				configureIdentityBusinessProfileAction(execution)
				execution.dependencies.GetRecordForUpdate = lockedIdentityProfile
				plannerCalls := 0
				execution.dependencies.PlanConditionalUpdate = func(context.Context, string, string, transactionmodel.ConditionalUpdateInput, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
					plannerCalls++
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, apperror.New(apperror.KindConflict, "backend.record.version_conflict", nil, nil)
				}

				_, err := execution.DeliverIdentity(t.Context(), existingProfileDeliveryRequest(operation))
				if failure == "stale_profile" && err == nil {
					_, err = execution.ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{
						Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "employee_profile", RecordID: "employee-profile-1",
						Fields: map[string]any{"rank": "senior"}, ExpectedUpdatedAt: "2026-09-06T00:00:00Z",
					})
				}
				if err == nil {
					t.Fatal("expected the combined operation to fail")
				}
				execution.unitOfWork.rollBack(t.Context())
				wantPlannerCalls := 0
				if failure == "stale_profile" {
					wantPlannerCalls = 1
				}
				if identityCommitted || len(store.commits) != 0 || store.rollbackCalls != 1 || plannerCalls != wantPlannerCalls || len(execution.plans) != 0 {
					t.Fatalf("identityCommitted=%v commits=%#v rollbacks=%d plannerCalls=%d plans=%d err=%v", identityCommitted, store.commits, store.rollbackCalls, plannerCalls, len(execution.plans), err)
				}
			})
		}
	}
}

func identityDeliveryTestUnitOfWork(store *actionUnitOfWorkStoreProbe) *actionUnitOfWork {
	return &actionUnitOfWork{
		manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)), phases: newActionExecutionPhaseMachine(),
		claim: actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{ID: "execution-identity-1", LeaseOwner: "test", FencingToken: 1}},
	}
}

func configureIdentityBusinessProfileAction(execution *businessActionExecution) {
	execution.action = definitionmodel.ActionSchema{
		Key: "employee.update", ObjectKey: "employee_profile", OptimisticConcurrency: true, ConcurrencyField: "updated_at",
		EffectSet: &definitionmodel.ActionEffectSet{
			Read:  []definitionmodel.ActionObjectEffect{{ObjectKey: "employee_profile"}},
			Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "employee_profile", Fields: []string{"rank", "employment_status"}}},
		},
	}
	execution.invocation.RecordID = "employee-profile-1"
	execution.invocation.Input = map[string]any{"expected_updated_at": "2026-09-06T00:00:00Z"}
}

func successfulExistingProfileDelivery(operation runtimeext.IdentityHandlerOperation) *identityHandlerDeliveryStub {
	status := "active"
	if operation == runtimeext.IdentityHandlerDisable {
		status = "disabled"
	}
	return &identityHandlerDeliveryStub{result: identitysdk.HandlerDeliveryResult{
		DeliveryID: "delivery-" + string(operation), User: identitysdk.User{ID: "identity-user-1", OrgID: "store-north", Version: 4},
		ProfileBinding: &identitysdk.HandlerProfileBinding{BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "employee-profile-1", IdentityUserID: "identity-user-1", Status: status, Version: 2},
	}}
}

func existingProfileDeliveryRequest(operation runtimeext.IdentityHandlerOperation) runtimeext.IdentityHandlerDeliveryRequest {
	return runtimeext.IdentityHandlerDeliveryRequest{
		User: runtimeext.IdentityHandlerUserMutation{Operation: operation, ExpectedVersion: 3},
		ProfileBinding: &runtimeext.IdentityHandlerProfileBindingMutation{
			BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "employee-profile-1", ExpectedVersion: 1,
		},
	}
}

func lockedIdentityProfile(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
	return recordmodel.Record{
		ID: "employee-profile-1", OwnerOrgID: "store-north", UpdatedAt: "2026-09-06T00:00:00Z",
		Data: map[string]any{"identity_user_id": "identity-user-1", "rank": "junior", "employment_status": "active"},
	}, nil
}
