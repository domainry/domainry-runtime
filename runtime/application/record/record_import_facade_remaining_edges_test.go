package record

import (
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func governedProfileFacade() *RecordApplicationService {
	return &RecordApplicationService{identityProfileExtensions: func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{{
			ObjectKey: "member_profile", IdentityRelationField: "identity_user",
			BindingLifecycle: profilebindingmodel.Lifecycle{AllowUnbound: true},
		}, {
			ObjectKey: "blank_relation", IdentityRelationField: " ",
			BindingLifecycle: profilebindingmodel.Lifecycle{AllowUnbound: true},
		}}
	}}
}

func TestRecordMutationFacadesRejectGovernedProfileBindingChanges(t *testing.T) {
	service := governedProfileFacade()
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	assertDenied := func(err error) {
		t.Helper()
		if apperror.CodeOf(err) != "backend.identity.profile_binding_command_required" {
			t.Fatalf("err=%v", err)
		}
	}
	_, _, err := service.PlanCreateMutation(t.Context(), "member_profile", map[string]any{"identity_user": "user-1"}, "", principal)
	assertDenied(err)
	_, _, err = service.PlanUpdateMutation(t.Context(), "member_profile", "profile-1", map[string]any{"identity_user": "user-2"}, principal)
	assertDenied(err)
	_, err = service.CreateRecord(t.Context(), "member_profile", map[string]any{"identity_user": "user-1"}, principal)
	assertDenied(err)
	_, err = service.CreateRecordIdempotent(t.Context(), "member_profile", map[string]any{"identity_user": "user-1"}, "key", principal)
	assertDenied(err)
	_, _, err = service.CreateRecordIdempotentResult(t.Context(), "member_profile", map[string]any{"identity_user": "user-1"}, "key", principal)
	assertDenied(err)
	_, err = service.UpdateRecord(t.Context(), "member_profile", "profile-1", map[string]any{"identity_user": "user-2"}, principal)
	assertDenied(err)
	_, err = service.UpdateRecordIdempotent(t.Context(), "member_profile", "profile-1", map[string]any{"identity_user": "user-2"}, "key", principal)
	assertDenied(err)
	_, err = service.ConditionalUpdateRecord(t.Context(), "member_profile", "profile-1", transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"identity_user": "user-2"}}, principal)
	assertDenied(err)

	if err := service.validateProfileBindingMutation("blank_relation", map[string]any{"identity_user": "user-1"}, false); err != nil {
		t.Fatal(err)
	}
	if err := service.validateProfileBindingMutation("member_profile", map[string]any{"identity_user": " "}, true); err != nil {
		t.Fatal(err)
	}
	if err := service.validateProfileBindingMutation("member_profile", nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestRecordMutationFacadeConditionalAuthorizationEdge(t *testing.T) {
	service := recordFacadeAuthorizationService()
	if _, err := service.ConditionalUpdateRecord(t.Context(), "customer", "customer-1", transactionmodel.ConditionalUpdateInput{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("conditional authorization err=%v", err)
	}
	if _, err := service.UpdateRecordIdempotent(t.Context(), "customer", "customer-1", nil, "key", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("idempotent update authorization err=%v", err)
	}
	if err := service.validateProfileBindingMutation("customer", nil, false); err != nil {
		t.Fatal(err)
	}
	plannerFacade := &RecordApplicationService{
		create: NewRecordCreateApplicationService(RecordCreateDependencies{}),
		update: NewRecordUpdateApplicationService(RecordUpdateDependencies{}),
	}
	if _, _, err := plannerFacade.PlanCreateMutation(t.Context(), "customer", nil, "", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("create plan authorization err=%v", err)
	}
	if _, _, err := plannerFacade.PlanUpdateMutation(t.Context(), "customer", "customer-1", nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("update plan authorization err=%v", err)
	}
}

func TestPlanUpdateMutationCoversDependencyAndScopeEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	record := recordmodel.Record{ID: "customer-1", UpdatedAt: "revision-1", Data: map[string]any{"name": "Before"}}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	newService := func(repository *updateRepositoryProbe, objectForAction func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error), canAccess func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool, canWrite bool) *RecordUpdateApplicationService {
		return NewRecordUpdateApplicationService(RecordUpdateDependencies{
			Repository: repository, ObjectForAction: objectForAction, CanAccess: canAccess,
			CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return canWrite },
		})
	}
	objectForAction := func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return object, nil
	}

	if _, _, err := newService(&updateRepositoryProbe{}, objectForAction, nil, true).PlanUpdateMutation(t.Context(), object.Key, record.ID, nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	failure := errors.New("dependency failure")
	if _, _, err := newService(&updateRepositoryProbe{}, func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return definitionmodel.ObjectSchema{}, failure
	}, nil, true).PlanUpdateMutation(t.Context(), object.Key, record.ID, nil, principal); !errors.Is(err, failure) {
		t.Fatalf("object err=%v", err)
	}
	timerObject := object
	timerObject.Key = "record_timer"
	timerObject.Config = map[string]any{"record_timer_runtime": true}
	if _, _, err := newService(&updateRepositoryProbe{}, func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return timerObject, nil
	}, nil, true).PlanUpdateMutation(t.Context(), timerObject.Key, record.ID, nil, principal); apperror.CodeOf(err) != "backend.record_timer.runtime_api_required" {
		t.Fatalf("record timer err=%v", err)
	}
	if _, _, err := newService(&updateRepositoryProbe{err: failure}, objectForAction, nil, true).PlanUpdateMutation(t.Context(), object.Key, record.ID, nil, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("repository err=%v", err)
	}
	if _, _, err := newService(&updateRepositoryProbe{}, objectForAction, nil, true).PlanUpdateMutation(t.Context(), object.Key, record.ID, nil, principal); apperror.CodeOf(err) != "backend.record.not_found" {
		t.Fatalf("not-found err=%v", err)
	}
	plannedContext := recordservice.RecordWithPlannedRelations(t.Context(), map[string]map[string]recordmodel.Record{
		object.Key: {record.ID: record},
	})
	planned, staged, err := newService(&updateRepositoryProbe{}, objectForAction, nil, true).PlanUpdateMutation(plannedContext, object.Key, record.ID, map[string]any{"name": "After staged create"}, principal)
	if err != nil || staged.Data["name"] != "After staged create" || planned.CanonicalCommit().Operation != "update" {
		t.Fatalf("staged plan=%#v updated=%#v err=%v", planned, staged, err)
	}
	repository := &updateRepositoryProbe{found: true, record: record}
	if _, _, err := newService(repository, objectForAction, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false }, true).PlanUpdateMutation(t.Context(), object.Key, record.ID, nil, principal); apperror.CodeOf(err) != "backend.record.outside_scope" {
		t.Fatalf("scope err=%v", err)
	}
	if _, _, err := newService(repository, objectForAction, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true }, true).PlanUpdateMutation(t.Context(), object.Key, record.ID, map[string]any{"name": "After"}, principal); err != nil {
		t.Fatalf("allowed scope err=%v", err)
	}
	if _, _, err := newService(repository, objectForAction, nil, false).PlanUpdateMutation(t.Context(), object.Key, record.ID, map[string]any{"name": "After"}, principal); apperror.CodeOf(err) != "backend.record.owner_write_denied" {
		t.Fatalf("planning err=%v", err)
	}
	plan, updated, err := newService(repository, objectForAction, nil, true).PlanUpdateMutation(t.Context(), object.Key, record.ID, map[string]any{"name": "After"}, principal)
	if err != nil || updated.Data["name"] != "After" || len(plan.WriteSet()) != 1 {
		t.Fatalf("plan=%#v updated=%#v err=%v", plan, updated, err)
	}
	facade := &RecordApplicationService{update: newService(repository, objectForAction, nil, true)}
	if _, err := facade.UpdateRecordIdempotent(t.Context(), object.Key, record.ID, map[string]any{"name": "After"}, "", principal); err != nil {
		t.Fatalf("idempotent update err=%v", err)
	}
	if _, err := facade.ConditionalUpdateRecord(t.Context(), object.Key, record.ID, transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"name": "After"}}, principal); err != nil {
		t.Fatalf("conditional update err=%v", err)
	}
}
