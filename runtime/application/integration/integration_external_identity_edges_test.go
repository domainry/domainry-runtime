package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type externalIdentityConfigRepo struct {
	integrationrepository.IntegrationConfigRepository
	identities []integrationmodel.IntegrationExternalIdentity
	listErr    error
	upsertErr  error
}

func (r *externalIdentityConfigRepo) ListExternalIdentities(context.Context, string) ([]integrationmodel.IntegrationExternalIdentity, error) {
	return append([]integrationmodel.IntegrationExternalIdentity(nil), r.identities...), r.listErr
}

func (r *externalIdentityConfigRepo) UpsertExternalIdentity(_ context.Context, _ string, value integrationmodel.IntegrationExternalIdentity) (integrationmodel.IntegrationExternalIdentity, error) {
	if r.upsertErr != nil {
		return integrationmodel.IntegrationExternalIdentity{}, r.upsertErr
	}
	for index := range r.identities {
		if r.identities[index].Key == value.Key {
			r.identities[index] = value
			return value, nil
		}
	}
	r.identities = append(r.identities, value)
	return value, nil
}

func externalIdentityService(repository *externalIdentityConfigRepo) *IntegrationApplicationService {
	return NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		PrincipalResolver: func(_ context.Context, actorID, roleKey, _ string) principalmodel.Principal {
			if actorID == "known" && roleKey == "member" {
				return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: actorID}}, accessfixture.Bundle{Key: roleKey})
			}
			return principalmodel.Principal{}
		},
		UnmappedPrincipalResolver: func(_ context.Context, workspaceID, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: workspaceID, UserID: "readonly"}}, accessfixture.Bundle{Key: "reader"})
		},
	})
}

func externalIdentityAdmin() principalmodel.Principal {
	return integrationManagementPrincipal("workspace.admin", "integration.entrypoint.invoke")
}

func validExternalIdentityRequest() integrationmodel.IntegrationExternalIdentityUpsertRequest {
	return integrationmodel.IntegrationExternalIdentityUpsertRequest{Provider: " provider ", ExternalSubject: " subject ", ActorID: " known ", RoleKey: " member "}
}

func TestExternalIdentityNormalizationAndProjectionHelpers(t *testing.T) {
	for input, want := range map[string]string{"": "user", "user": "user", "organization": "organization", "department": "department", "group": "group", "bot": "bot", "service_account": "service_account", "device": "device"} {
		if got, err := normalizeExternalSubjectType(input); err != nil || got != want {
			t.Fatalf("subject type %q=%q err=%v", input, got, err)
		}
	}
	if _, err := normalizeExternalSubjectType("invalid"); apperror.CodeOf(err) != "backend.integration.external_identity.invalid_subject_type" {
		t.Fatalf("invalid subject type error=%v", err)
	}
	for input, want := range map[string]string{"": "active", "active": "active", "disabled": "disabled"} {
		if got, err := normalizeExternalIdentityStatus(input); err != nil || got != want {
			t.Fatalf("status %q=%q err=%v", input, got, err)
		}
	}
	if _, err := normalizeExternalIdentityStatus("invalid"); err == nil {
		t.Fatal("invalid identity status accepted")
	}
	for input, want := range map[string]string{"": "reject", "reject": "reject", "read_only": "read_only"} {
		if got, err := normalizeExternalIdentityUnmappedPolicy(input); err != nil || got != want {
			t.Fatalf("policy %q=%q err=%v", input, got, err)
		}
	}
	if _, err := normalizeExternalIdentityUnmappedPolicy("invalid"); err == nil {
		t.Fatal("invalid unmapped policy accepted")
	}
	for _, test := range []struct{ provider, kind, subject, want string }{
		{"", "", "subject", "subject"}, {"provider", "", "", "provider"}, {"provider", "group", "subject", "provider:group:subject"}, {"provider", "user", "subject", "provider:subject"},
	} {
		if got := externalPrincipal(test.provider, test.kind, test.subject); got != test.want {
			t.Fatalf("external principal=%q want=%q", got, test.want)
		}
	}
	identity := integrationmodel.IntegrationExternalIdentity{Key: "key", ExternalBotID: "bot", Provider: "provider", ExternalSubject: "subject", ActorID: "known", RoleKey: "member"}
	if externalIdentityAuditShape(identity)["external_bot_id_set"] != true || externalIdentityResolutionAuditShape(identity)["external_subject_type"] != "user" {
		t.Fatal("external identity audit projection lost normalized fields")
	}
	metadata := externalIdentityResolutionMetadata(integrationmodel.IntegrationExternalIdentityResolveRequest{ExternalBotID: "bot"}, "workspace", "provider", "user", "provider:subject", "read_only", false, "unmapped", accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "actor"}}, accessfixture.Bundle{Key: "reader"}))
	if metadata["actor_id"] != "actor" || metadata["external_bot_id_set"] != true {
		t.Fatalf("resolution metadata=%#v", metadata)
	}
	metadata = externalIdentityResolutionMetadata(integrationmodel.IntegrationExternalIdentityResolveRequest{}, "workspace", "provider", "user", "provider:subject", "reject", false, "unmapped", principalmodel.Principal{})
	if _, exists := metadata["actor_id"]; exists {
		t.Fatalf("unexpected actor metadata=%#v", metadata)
	}
	if CanInvokeEntrypoint(principalmodel.Principal{}) || CanInvokeEntrypoint(integrationManagementPrincipal()) || !CanInvokeEntrypoint(externalIdentityAdmin()) || !CanInvokeEntrypoint(integrationManagementPrincipal("integration.entrypoint.invoke")) {
		t.Fatal("entrypoint permission matrix mismatch")
	}
}

func TestUpsertAndDisableExternalIdentityEdges(t *testing.T) {
	repository := &externalIdentityConfigRepo{}
	service := externalIdentityService(repository)
	admin := externalIdentityAdmin()
	invalidWorkspace := admin
	invalidWorkspace.WorkspaceID = ""
	if _, err := service.UpsertIntegrationExternalIdentity(t.Context(), "key", validExternalIdentityRequest(), invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("upsert workspace error=%v", err)
	}
	if _, err := service.DisableIntegrationExternalIdentity(t.Context(), "key", invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("disable workspace error=%v", err)
	}
	if _, err := service.UpsertIntegrationExternalIdentity(t.Context(), "key", validExternalIdentityRequest(), integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("non-admin upsert error=%v", err)
	}
	if _, err := service.DisableIntegrationExternalIdentity(t.Context(), "key", integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("non-admin disable error=%v", err)
	}
	for name, mutate := range map[string]func(*integrationmodel.IntegrationExternalIdentityUpsertRequest){
		"subject type": func(r *integrationmodel.IntegrationExternalIdentityUpsertRequest) { r.ExternalSubjectType = "bad" },
		"provider":     func(r *integrationmodel.IntegrationExternalIdentityUpsertRequest) { r.Provider = "" },
		"subject":      func(r *integrationmodel.IntegrationExternalIdentityUpsertRequest) { r.ExternalSubject = "" },
		"actor":        func(r *integrationmodel.IntegrationExternalIdentityUpsertRequest) { r.ActorID = "" },
		"role":         func(r *integrationmodel.IntegrationExternalIdentityUpsertRequest) { r.RoleKey = "" },
		"unknown":      func(r *integrationmodel.IntegrationExternalIdentityUpsertRequest) { r.ActorID = "unknown" },
		"status":       func(r *integrationmodel.IntegrationExternalIdentityUpsertRequest) { r.Status = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			request := validExternalIdentityRequest()
			mutate(&request)
			if _, err := service.UpsertIntegrationExternalIdentity(t.Context(), "", request, admin); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
	request := validExternalIdentityRequest()
	request.Key, request.Status, request.ExternalBotID = " request-key ", "disabled", " bot "
	saved, err := service.UpsertIntegrationExternalIdentity(t.Context(), "", request, admin)
	if err != nil || saved.Key != "request-key" || saved.Status != "disabled" || saved.CreatedBy != "user" {
		t.Fatalf("saved=%#v err=%v", saved, err)
	}
	request.Key, request.Status = "", ""
	saved, err = service.UpsertIntegrationExternalIdentity(t.Context(), "", request, admin)
	if err != nil || saved.Key == "" || saved.Status != "active" {
		t.Fatalf("generated key identity=%#v err=%v", saved, err)
	}
	if saved, err = service.UpsertIntegrationExternalIdentity(t.Context(), saved.Key, request, admin); err != nil || saved.Key == "" {
		t.Fatalf("existing identity update=%#v err=%v", saved, err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.UpsertIntegrationExternalIdentity(t.Context(), "key", request, admin); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("list failure=%v", err)
	}
	repository.listErr = nil
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.UpsertIntegrationExternalIdentity(t.Context(), "key", request, admin); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("upsert failure=%v", err)
	}
	repository.upsertErr = nil
	if _, err := service.DisableIntegrationExternalIdentity(t.Context(), "missing", admin); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing disable error=%v", err)
	}
	repository.identities = []integrationmodel.IntegrationExternalIdentity{{Key: "key", WorkspaceID: "workspace", Provider: "provider", ExternalSubject: "subject", ActorID: "known", RoleKey: "member", Status: "active"}}
	disabled, err := service.DisableIntegrationExternalIdentity(t.Context(), " key ", admin)
	if err != nil || disabled.Status != "disabled" || disabled.DisabledAt == "" {
		t.Fatalf("disabled=%#v err=%v", disabled, err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.DisableIntegrationExternalIdentity(t.Context(), "key", admin); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("disable list failure=%v", err)
	}
	repository.listErr = nil
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.DisableIntegrationExternalIdentity(t.Context(), "key", admin); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("disable upsert failure=%v", err)
	}
}

func TestResolveExternalIdentityMappedAndUnmappedEdges(t *testing.T) {
	repository := &externalIdentityConfigRepo{}
	service := externalIdentityService(repository)
	caller := integrationManagementPrincipal("integration.entrypoint.invoke")
	caller.RequestID = "request"
	invalidWorkspace := caller
	invalidWorkspace.WorkspaceID = ""
	if _, _, err := service.ResolveIntegrationExternalIdentity(t.Context(), integrationmodel.IntegrationExternalIdentityResolveRequest{}, invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("resolve workspace error=%v", err)
	}
	if _, _, err := service.ResolveIntegrationExternalIdentity(t.Context(), integrationmodel.IntegrationExternalIdentityResolveRequest{}, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if _, _, err := service.ResolveIntegrationExternalIdentity(t.Context(), integrationmodel.IntegrationExternalIdentityResolveRequest{Provider: "provider", ExternalSubject: "subject", ExternalSubjectType: "bad"}, caller); err == nil {
		t.Fatal("invalid subject type accepted")
	}
	for _, request := range []integrationmodel.IntegrationExternalIdentityResolveRequest{{}, {Provider: "provider"}} {
		if _, _, err := service.ResolveIntegrationExternalIdentity(t.Context(), request, caller); apperror.CodeOf(err) != "backend.integration.external_identity.missing_subject" {
			t.Fatalf("missing subject error=%v", err)
		}
	}
	request := integrationmodel.IntegrationExternalIdentityResolveRequest{Provider: "provider", ExternalSubject: "subject"}
	if result, principal, err := service.ResolveIntegrationExternalIdentity(t.Context(), request, caller); apperror.CodeOf(err) != "backend.integration.external_identity.unmapped" || result.Mapped || principal.Known {
		t.Fatalf("unmapped reject result=%#v principal=%#v err=%v", result, principal, err)
	}
	request.OnUnmapped = "bad"
	if _, _, err := service.ResolveIntegrationExternalIdentity(t.Context(), request, caller); err == nil {
		t.Fatal("invalid unmapped policy accepted")
	}
	request.OnUnmapped = "read_only"
	result, principal, err := service.ResolveIntegrationExternalIdentity(t.Context(), request, caller)
	if err != nil || result.Status != "unmapped_read_only" || principal.UserID != "readonly" || principal.RequestID != "request" {
		t.Fatalf("read-only result=%#v principal=%#v err=%v", result, principal, err)
	}
	repository.identities = []integrationmodel.IntegrationExternalIdentity{{Key: "mapping", WorkspaceID: "workspace", Provider: "provider", ExternalSubject: "subject", Status: "disabled"}}
	result, _, err = service.ResolveIntegrationExternalIdentity(t.Context(), request, caller)
	if err != nil || result.Mapped {
		t.Fatalf("disabled mapping result=%#v err=%v", result, err)
	}
	repository.identities[0] = integrationmodel.IntegrationExternalIdentity{Key: "mapping", WorkspaceID: "workspace", Provider: "provider", ExternalSubject: "subject", ActorID: "known", RoleKey: "member", Status: "active"}
	request = integrationmodel.IntegrationExternalIdentityResolveRequest{Provider: "provider", ExternalSubject: "subject", ExternalName: " Name ", ExternalOrganization: " Org ", ExternalDepartment: " Dept ", ExternalGroup: " Group ", ExternalBotID: " Bot "}
	result, principal, err = service.ResolveIntegrationExternalIdentity(t.Context(), request, caller)
	if err != nil || !result.Mapped || principal.UserID != "known" || result.ExternalName != "Name" || result.ExternalBotID != "Bot" {
		t.Fatalf("mapped result=%#v principal=%#v err=%v", result, principal, err)
	}
	request = integrationmodel.IntegrationExternalIdentityResolveRequest{Provider: "provider", ExternalSubject: "subject"}
	if result, _, err = service.ResolveIntegrationExternalIdentity(t.Context(), request, caller); err != nil || !result.Mapped {
		t.Fatalf("mapped empty optional result=%#v err=%v", result, err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, _, err := service.ResolveIntegrationExternalIdentity(t.Context(), request, caller); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("resolve list failure=%v", err)
	}
	repository.listErr = nil
	repository.upsertErr = errIntegrationManagementTest
	if _, _, err := service.ResolveIntegrationExternalIdentity(t.Context(), request, caller); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("resolve upsert failure=%v", err)
	}
}

func TestExternalIdentityWebhookWritebackEdges(t *testing.T) {
	repository := &externalIdentityConfigRepo{}
	service := externalIdentityService(repository)

	connection := integrationmodel.IntegrationConnection{WorkspaceID: "workspace", ProviderKey: "provider", Config: map[string]any{}}
	if _, err := service.WritebackWebhookExternalIdentity(t.Context(), integrationmodel.IntegrationConnection{}, nil); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("webhook workspace error=%v", err)
	}
	if value, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, nil); err != nil || value != nil {
		t.Fatalf("nil webhook identity=%#v err=%v", value, err)
	}
	if value, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, &integrationcontract.WebhookExternalIdentity{Subject: " "}); err != nil || value != nil {
		t.Fatalf("empty webhook identity=%#v err=%v", value, err)
	}
	if value, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, &integrationcontract.WebhookExternalIdentity{Subject: "subject"}); err != nil || value != nil {
		t.Fatalf("unmapped webhook identity=%#v err=%v", value, err)
	}
	connection.Config["external_identity_mappings"] = map[string]any{"subject": map[string]any{"actor_id": "known", "role_key": "admin"}}
	if _, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, &integrationcontract.WebhookExternalIdentity{Subject: "subject"}); err == nil {
		t.Fatal("unsafe webhook role accepted")
	}
	for name, mapping := range map[string]map[string]any{
		"actor":   {"role_key": "member"},
		"role":    {"actor_id": "known"},
		"unknown": {"actor_id": "unknown", "role_key": "member"},
	} {
		t.Run(name, func(t *testing.T) {
			connection.Config["external_identity_mappings"] = map[string]any{"subject": mapping}
			if _, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, &integrationcontract.WebhookExternalIdentity{Subject: "subject"}); err == nil {
				t.Fatal("invalid webhook mapping accepted")
			}
		})
	}
	connection.Config["external_identity_mappings"] = map[string]any{"subject": map[string]any{"actor_id": "known", "role_key": "member"}}
	repository.identities = append([]integrationmodel.IntegrationExternalIdentity{
		{Key: "wrong-type", Provider: "provider", ExternalSubjectType: "group", ExternalSubject: "subject"},
		{Key: "wrong-subject", Provider: "provider", ExternalSubject: "other"},
	}, repository.identities...)
	repository.listErr = errIntegrationManagementTest
	if _, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, &integrationcontract.WebhookExternalIdentity{Subject: "subject"}); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("webhook list error=%v", err)
	}
	repository.listErr = nil
	value, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, &integrationcontract.WebhookExternalIdentity{Subject: "subject", Name: "Name", Group: "Group"})
	if err != nil || value == nil || value.ActorID != "known" || value.ExternalSubjectType != "user" {
		t.Fatalf("webhook identity=%#v err=%v", value, err)
	}
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.WritebackWebhookExternalIdentity(t.Context(), connection, &integrationcontract.WebhookExternalIdentity{Subject: "subject"}); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("webhook upsert error=%v", err)
	}
}
