package record

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestBusinessProfileDeactivationFieldContract(t *testing.T) {
	extension := profilebindingmodel.Binding{
		ObjectKey: "member_profile",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{
			StatusField:        "member_status",
			ActiveStatusValues: []string{"active", "trial"},
		},
	}
	service := &RecordApplicationService{identityProfileExtensions: func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{extension}
	}}
	if field, err := businessProfileDeactivationField(service, " member_profile ", "cancelled"); err != nil || field != "member_status" {
		t.Fatalf("field=%q err=%v", field, err)
	}
	if field, err := businessProfileReactivationField(service, "member_profile", "trial"); err != nil || field != "member_status" {
		t.Fatalf("reactivation field=%q err=%v", field, err)
	}
	if _, err := businessProfileReactivationField(service, "member_profile", "cancelled"); apperror.CodeOf(err) != "backend.identity.profile_reactivation_status_inactive" {
		t.Fatalf("inactive restoration accepted: %v", err)
	}
	extension.BusinessIdentity.StatusField = ""
	extension.Directory.StatusField = "directory_status"
	if field, err := businessProfileDeactivationField(service, "member_profile", "cancelled"); err != nil || field != "directory_status" {
		t.Fatalf("directory field=%q err=%v", field, err)
	}
	if field, err := businessProfileReactivationField(service, "member_profile", "trial"); err != nil || field != "directory_status" {
		t.Fatalf("reactivation directory field=%q err=%v", field, err)
	}
	extension.Directory.StatusField = ""
	if _, err := businessProfileReactivationField(service, "member_profile", "trial"); apperror.CodeOf(err) != "backend.identity.profile_reactivation_status_required" {
		t.Fatalf("missing reactivation status field err=%v", err)
	}
	extension.Directory.StatusField = "directory_status"
	if _, err := businessProfileReactivationField(nil, "member_profile", "trial"); apperror.CodeOf(err) != "backend.identity.profile_binding_unavailable" {
		t.Fatalf("missing reactivation service err=%v", err)
	}
	if _, err := businessProfileReactivationField(service, "other", "trial"); apperror.CodeOf(err) != "backend.identity.profile_binding_definition_not_found" {
		t.Fatalf("missing reactivation definition err=%v", err)
	}
	for _, test := range []struct {
		name    string
		service *RecordApplicationService
		object  string
		status  string
		code    string
	}{
		{"missing service", nil, "member_profile", "cancelled", "backend.identity.profile_binding_unavailable"},
		{"missing definition", service, "other", "cancelled", "backend.identity.profile_binding_definition_not_found"},
		{"missing status", service, "member_profile", "", "backend.identity.profile_deactivation_status_required"},
		{"active status", service, "member_profile", "active", "backend.identity.profile_deactivation_status_active"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := businessProfileDeactivationField(test.service, test.object, test.status); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v code=%q", err, apperror.CodeOf(err))
			}
		})
	}
}

func TestDeactivateBusinessProfileAuthorizesDedicatedDutyFirst(t *testing.T) {
	service := &RecordApplicationService{}
	if _, err := service.DeactivateBusinessProfile(t.Context(), "member_profile", "member-1", "cancelled", "", "key", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal err=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	if _, err := service.DeactivateBusinessProfile(t.Context(), "member_profile", "member-1", "cancelled", "", "key", principal); apperror.CodeOf(err) != "backend.identity.profile_binding_manage_required" {
		t.Fatalf("missing duty err=%v", err)
	}
}

func TestReactivateBusinessProfileAuthorizesDedicatedDutyAndIdempotencyFirst(t *testing.T) {
	service := &RecordApplicationService{identityProfileExtensions: func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{{
			ObjectKey: "member_profile",
			BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{
				StatusField: "status", ActiveStatusValues: []string{"active"},
			},
		}}
	}}
	if _, err := service.ReactivateBusinessProfile(t.Context(), "member_profile", "member-1", "active", "", "key", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal err=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	if _, err := service.ReactivateBusinessProfile(t.Context(), "member_profile", "member-1", "active", "", "key", principal); apperror.CodeOf(err) != "backend.identity.profile_binding_manage_required" {
		t.Fatalf("missing duty err=%v", err)
	}
	accessfixture.Set(&principal, accessfixture.Bundle{Permissions: []string{"identity.profile_binding.manage"}})
	if _, err := service.ReactivateBusinessProfile(t.Context(), "member_profile", "member-1", "inactive", "", "key", principal); apperror.CodeOf(err) != "backend.identity.profile_reactivation_status_inactive" {
		t.Fatalf("inactive status err=%v", err)
	}
	if _, err := service.ReactivateBusinessProfile(t.Context(), "member_profile", "member-1", "active", "", "", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("missing idempotency key err=%v code=%q", err, apperror.CodeOf(err))
	}
}

func TestDeactivateBusinessProfileValidatesIdempotencyAfterDefinition(t *testing.T) {
	service := &RecordApplicationService{identityProfileExtensions: func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{{ObjectKey: "member_profile", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{StatusField: "status", ActiveStatusValues: []string{"active"}}}}
	}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"identity.profile_binding.manage"}})
	if _, err := service.DeactivateBusinessProfile(t.Context(), "member_profile", "member", "inactive", "", "", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("err=%v", err)
	}
}

func TestBusinessProfileLifecycleDefinitionErrorsSkipsAndExpectedRevision(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "identity.profile_binding.manage", "*"}})
	if _, err := (&RecordApplicationService{}).DeactivateBusinessProfile(t.Context(), "profile", "id", "inactive", "", "key", principal); apperror.CodeOf(err) != "backend.identity.profile_binding_unavailable" {
		t.Fatalf("definition err=%v", err)
	}
	binding := profilebindingmodel.BusinessIdentityBinding{Key: "member", StatusField: "status", ActiveStatusValues: []string{"active"}}
	repository := &updateEdgeRepository{found: true, record: recordmodel.Record{ID: "profile-1", UpdatedAt: "revision-1", Data: map[string]any{"status": "active"}}}
	dependencies := recordUpdateEdgeDependencies(repository)
	dependencies.ObjectForAction = func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
		return definitionmodel.ObjectSchema{Key: "profile", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}, nil
	}
	applicationSource := func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{{ObjectKey: "profile", BusinessIdentity: binding}}
	}
	service := &RecordApplicationService{update: NewRecordUpdateApplicationService(dependencies), identityProfileExtensions: applicationSource}
	if _, err := service.DeactivateBusinessProfile(t.Context(), "profile", "profile-1", "inactive", " revision-1 ", "deactivate-key", principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("deactivate err=%v", err)
	}
	if _, err := service.ReactivateBusinessProfile(t.Context(), "profile", "profile-1", "active", " revision-1 ", "reactivate-key", principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("reactivate err=%v", err)
	}
	if _, err := service.DeactivateBusinessProfile(t.Context(), "profile", "profile-1", "inactive", "", "deactivate-key-empty-revision", principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("deactivate empty revision err=%v", err)
	}
	if _, err := service.ReactivateBusinessProfile(t.Context(), "profile", "profile-1", "active", "", "reactivate-key-empty-revision", principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("reactivate empty revision err=%v", err)
	}
}

func TestBusinessProfileLifecycleRemainingConditionBoundaries(t *testing.T) {
	service := &RecordApplicationService{identityProfileExtensions: func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{{ObjectKey: "profile", BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{StatusField: "status", ActiveStatusValues: []string{"active"}}}}
	}}
	if _, err := businessProfileDeactivationField(service, "profile", ""); apperror.CodeOf(err) != "backend.identity.profile_deactivation_status_required" {
		t.Fatalf("empty inactive status err=%v", err)
	}
	missingStatusField := &RecordApplicationService{identityProfileExtensions: func() []profilebindingmodel.Binding {
		return []profilebindingmodel.Binding{{ObjectKey: "profile"}}
	}}
	if _, err := businessProfileDeactivationField(missingStatusField, "profile", "inactive"); apperror.CodeOf(err) != "backend.identity.profile_deactivation_status_required" {
		t.Fatalf("missing status field err=%v", err)
	}
	if _, err := businessProfileReactivationField(&RecordApplicationService{}, "profile", "active"); apperror.CodeOf(err) != "backend.identity.profile_binding_unavailable" {
		t.Fatalf("missing extension source err=%v", err)
	}
	if _, err := businessProfileReactivationField(service, "profile", ""); apperror.CodeOf(err) != "backend.identity.profile_reactivation_status_required" {
		t.Fatalf("empty active status err=%v", err)
	}
}
