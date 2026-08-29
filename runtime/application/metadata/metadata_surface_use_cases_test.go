package metadata

import (
	"context"
	"encoding/json"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestPublishedRuntimeSchemaOmitsAdministrationAndInternalConfiguration(t *testing.T) {
	snapshot := metadatamodel.ApplicationSchemaSnapshot{
		TemplateID: "template", TemplateVersion: "1", SchemaHash: "hash", SnapshotVersion: "snapshot",
		Objects: []definitionmodel.ObjectSchema{{
			Key: "order", Name: "Order",
			Config: map[string]any{"display": "table", "sql_query": "select secret", "internal_registry": "hidden", "nested": map[string]any{"access_token": "hidden", "safe": "visible"}},
		}},
		Views: []definitionmodel.ViewSchema{{
			Key: "order.table", ObjectKey: "order", Type: "table",
			Config: map[string]any{"label": "Orders", "password": "hidden"},
		}},
	}
	service := NewMetadataSchemaApplicationService(metadataSchemaApplicationProviderStub{snapshot: snapshot}, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "member"})
	published, err := service.PublishedRuntimeSchema(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(published)
	for _, forbidden := range []string{"roles", "permission_sets", "guardrails", "sql_query", "select secret", "internal_registry", "access_token", "password"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("published schema leaked %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(string(payload), `"safe":"visible"`) || !strings.Contains(string(payload), `"display":"table"`) {
		t.Fatalf("published schema removed safe configuration: %s", payload)
	}
}

func TestPublishedSurfaceContextIsBoundToBackendPrincipalSurface(t *testing.T) {
	service := NewMetadataSchemaApplicationService(metadataSchemaApplicationProviderStub{snapshot: metadatamodel.ApplicationSchemaSnapshot{SchemaHash: "hash"}}, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}, SurfaceKey: "consumer_portal",

		ActiveBusinessProfile: &profilebindingmodel.Reference{BindingKey: "customer-profile"},
	}, accessfixture.Bundle{Key: "consumer", Permissions: []string{"customer.read"}},
	)
	if _, err := service.PublishedSurfaceContext(t.Context(), "business_workspace", principal); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("cross-Surface context accepted: %v", err)
	}
	context, err := service.PublishedSurfaceContext(t.Context(), "consumer_portal", principal)
	if err != nil || context.ActiveBusinessProfile != "customer-profile" || context.SchemaHash != "hash" {
		t.Fatalf("context=%+v err=%v", context, err)
	}
}

func TestOpsMetadataDiagnosticsRequiresExplicitPermissionAndReturnsOnlyCompatibilityFacts(t *testing.T) {
	repository := &metadataWatcherRepository{revision: "revision-7"}
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{
		SchemaHash: "schema-hash", SnapshotVersion: "snapshot-7", TemplateVersion: "3",
		Objects: []definitionmodel.ObjectSchema{{Key: "order"}}, Actions: []definitionmodel.ActionSchema{{Key: "order.submit"}},
	}}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: repository, Runtime: runtime})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.OpsMetadataDiagnostics(t.Context(), principal); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("workspace.admin unexpectedly granted metadata Ops diagnostics: %v", err)
	}
	principal = accessfixture.Attach(principal, accessfixture.Bundle{Permissions: []string{PermissionMetadataOpsRead}})
	result, err := service.OpsMetadataDiagnostics(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(result)
	if result.RepositoryRevision != "revision-7" || !result.Compatible || strings.Contains(string(payload), "objects") || strings.Contains(string(payload), "sql") || strings.Contains(string(payload), "secret") {
		t.Fatalf("diagnostics=%s", payload)
	}
}

func TestMetadataSurfaceUseCasesRejectMissingScopeAndCoverOptionalContext(t *testing.T) {
	schemaService := NewMetadataSchemaApplicationService(
		metadataSchemaApplicationProviderStub{snapshot: metadatamodel.ApplicationSchemaSnapshot{SchemaHash: "hash"}},
		nil,
	)
	unknown := principalmodel.Principal{}
	if _, err := schemaService.PublishedRuntimeSchema(t.Context(), unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("published schema scope err=%v", err)
	}
	if _, err := schemaService.PublishedSurfaceContext(t.Context(), "business_workspace", unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("surface context scope err=%v", err)
	}

	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "member"})
	result, err := schemaService.PublishedSurfaceContext(t.Context(), "business_workspace", principal)
	if err != nil || result.Surface != "business_workspace" || result.ActiveBusinessProfile != "" {
		t.Fatalf("surface context=%+v err=%v", result, err)
	}

	application := NewApplicationSchemaService(ApplicationSchemaDependencies{})
	if _, err := application.OpsMetadataDiagnostics(t.Context(), unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("ops diagnostics scope err=%v", err)
	}
	if metadataHasExactPermission(unknown, PermissionMetadataOpsRead) {
		t.Fatal("unknown principal received exact metadata permission")
	}
}

func TestOpsMetadataDiagnosticsRejectsEveryUnavailableDependency(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{PermissionMetadataOpsRead}})
	repository := &metadataWatcherRepository{revision: "revision"}
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{SchemaHash: "hash"}}
	tests := []struct {
		name    string
		service *ApplicationSchemaService
	}{
		{name: "nil service"},
		{name: "nil repository", service: NewApplicationSchemaService(ApplicationSchemaDependencies{Runtime: runtime})},
		{name: "nil runtime", service: NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: repository})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.service.OpsMetadataDiagnostics(t.Context(), principal)
			if apperror.CodeOf(err) != "backend.metadata.repository_unavailable" {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestOpsMetadataDiagnosticsForwardsRepositoryRevisionFailure(t *testing.T) {
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{
		Repository: &metadataWatcherRepository{revision: "revision"},
		Runtime:    &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{SchemaHash: "hash"}},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{PermissionMetadataOpsRead}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.OpsMetadataDiagnostics(ctx, principal); err == nil {
		t.Fatal("canceled repository revision unexpectedly succeeded")
	}
}

func TestSanitizePublishedMapCoversSensitiveKeysAndNestedSlices(t *testing.T) {
	result := sanitizePublishedMap(map[string]any{
		"secret_value": "hidden",
		"password":     "hidden",
		"safe": []any{
			map[string]any{"password_hint": "hidden", "label": "visible"},
			"literal",
		},
	})
	if _, ok := result["secret_value"]; ok {
		t.Fatalf("secret survived: %#v", result)
	}
	if _, ok := result["password"]; ok {
		t.Fatalf("password survived: %#v", result)
	}
	values, ok := result["safe"].([]any)
	if !ok || len(values) != 2 {
		t.Fatalf("safe slice=%#v", result["safe"])
	}
	nested, ok := values[0].(map[string]any)
	if !ok || nested["label"] != "visible" {
		t.Fatalf("nested=%#v", values[0])
	}
	if _, ok := nested["password_hint"]; ok {
		t.Fatalf("nested password survived: %#v", nested)
	}
}
