package appschema

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
)

type localizedTextRepositoryStub struct {
	appschemarepository.ApplicationSchemaRepository
	values []appschemamodel.LocalizedText
}

func (r localizedTextRepositoryStub) ListLocalizedTexts(_ context.Context, _ string, query appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error) {
	result := []appschemamodel.LocalizedText{}
	for _, value := range r.values {
		if query.Locale == "" || value.Locale == query.Locale {
			result = append(result, value)
		}
	}
	return result, nil
}

type localizedSchemaProviderStub struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s localizedSchemaProviderStub) SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot
}

type localizedLifecycleRuntimeStub struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s localizedLifecycleRuntimeStub) Schema() appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot
}
func (localizedLifecycleRuntimeStub) ApplyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []agentsdk.SkillSchema, []agentsdk.AgentSchema, []profilebindingmodel.Binding) {
}

func TestSchemaLocalizationAndCoveragePreserveStableValues(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Name: "客户系统", SchemaHash: "schema",
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "客户", Fields: []definitionmodel.FieldSchema{{
			Key: "status", Name: "状态", Type: "status", DefaultValue: "active", Validation: definitionmodel.FieldValidation{Options: []string{"active"}},
			Options: []any{map[string]any{"value": "active", "label": "活跃"}},
		}}}},
		Actions:       []definitionmodel.ActionSchema{{Key: "customer.activate", ObjectKey: "customer", Label: "启用客户"}},
		GuardedWrites: []appschemamodel.ApplicationSchemaGuardedWriteContract{{ObjectKey: "customer", ActionKey: "customer.activate", Label: "启用客户"}},
		Dictionaries:  []appschemamodel.DictionarySchema{{Key: "customer_status", Name: "客户状态", Items: []appschemamodel.DictionaryItemSchema{{Key: "active", Value: "active", Label: "活跃"}}}},
	}
	values := []appschemamodel.LocalizedText{
		{Locale: "en-US", EntityType: "app", EntityKey: "app", Property: "name", Text: "Customer System"},
		{Locale: "en-US", EntityType: "object", EntityKey: "customer", Property: "name", Text: "Customer"},
		{Locale: "en-US", EntityType: "field", EntityKey: "customer.status", Property: "name", Text: "Status"},
		{Locale: "en-US", EntityType: "field_option", EntityKey: "customer.status.active", Property: "label", Text: "Active"},
		{Locale: "en-US", EntityType: "action", EntityKey: "customer.activate", Property: "label", Text: "Activate Customer"},
		{Locale: "en-US", EntityType: "dictionary", EntityKey: "customer_status", Property: "name", Text: "Customer Status"},
		{Locale: "en-US", EntityType: "dictionary_item", EntityKey: "customer_status.active", Property: "label", Text: "Active"},
	}
	repository := localizedTextRepositoryStub{values: values}
	localized := appschemaservice.NewApplicationSchemaDomainService(localizedSchemaProviderStub{snapshot: snapshot}, repository).ForPrincipalLocale(t.Context(), principalmodel.Principal{}, "en-US")
	if localized.Name != "Customer System" || localized.Objects[0].Name != "Customer" || localized.Objects[0].Fields[0].Name != "Status" {
		t.Fatalf("localized schema = %#v", localized)
	}
	field := localized.Objects[0].Fields[0]
	option := field.Options.([]any)[0].(map[string]any)
	if field.DefaultValue != "active" || field.Validation.Options[0] != "active" || option["value"] != "active" || option["label"] != "Active" {
		t.Fatalf("localized field changed stable values: %#v", field)
	}
	if localized.Actions[0].Label != "Activate Customer" || localized.GuardedWrites[0].Label != "Activate Customer" || localized.Dictionaries[0].Items[0].Value != "active" || localized.Dictionaries[0].Items[0].Label != "Active" {
		t.Fatalf("localized projections = %#v", localized)
	}

	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: repository, Runtime: localizedLifecycleRuntimeStub{snapshot: snapshot}})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	coverage, err := service.LocalizedTextCoverage(t.Context(), "fr-FR", "en-US", admin)
	if err != nil || coverage.MissingCount == 0 {
		t.Fatalf("coverage=%#v err=%v", coverage, err)
	}
	for _, item := range coverage.Items {
		if item.EntityType == "app" && item.EntityKey == "app" && item.Property == "name" {
			if !item.Missing || item.ResolvedSource != "fallback_locale" || item.ResolvedText != "Customer System" {
				t.Fatalf("app fallback coverage = %#v", item)
			}
			return
		}
	}
	t.Fatal("app coverage item missing")
}
