package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type metadataLocaleRepository struct {
	appschemarepository.ApplicationSchemaRepository
	values []appschemamodel.LocalizedText
	err    error
}

func (r metadataLocaleRepository) ListLocalizedTexts(context.Context, string, appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error) {
	return r.values, r.err
}

func TestApplicationSchemaLocaleProjectsAllOwnedShapes(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Name: "App", SchemaHash: "hash",
		Objects: []definitionmodel.ObjectSchema{{Validations: []definitionmodel.ValidationSchema{{}}}, {Key: "order", Name: "Order", Description: "Description", Fields: []definitionmodel.FieldSchema{{
			Key: "status", Name: "Status", Options: []map[string]any{{"value": "ready", "label": "Ready"}},
		}}, Validations: []definitionmodel.ValidationSchema{{Type: "required", FieldKey: "status", Message: "Required"}, {Key: "named", Message: "Named"}}}},
		Views:         []definitionmodel.ViewSchema{{Key: "orders", Name: "Orders"}},
		Actions:       []definitionmodel.ActionSchema{{Key: "order.approve", Label: "Approve", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "note", Name: "Note"}}}},
		GuardedWrites: []appschemamodel.ApplicationSchemaGuardedWriteContract{{ActionKey: "order.approve", Label: "Approve"}},
		Workflows:     []definitionmodel.WorkflowSchema{{Key: "notify", Name: "Notify"}},
		Dictionaries:  []appschemamodel.DictionarySchema{{Key: "status", Name: "Status", Description: "Status values", Items: []appschemamodel.DictionaryItemSchema{{Value: "ready", Label: "Ready", Description: "Ready description"}}}},
		Reports:       []reportmodel.ReportSchema{{Key: "orders", Name: "Orders report"}},
		EntryPoints:   []definitionmodel.EntryPointSchema{{Key: "home", Name: "Home", Description: "Home description"}},
		Skills:        []agentmodel.SkillSchema{{Key: "search", Name: "Search", Description: "Search description"}},
		Agents:        []agentmodel.AgentSchema{{Key: "assistant", Name: "Assistant", Description: "Assistant description"}},
	}
	values := []appschemamodel.LocalizedText{{EntityType: "app", EntityKey: "app", Property: "name", Text: "Anwendung"}}
	service := NewApplicationSchemaDomainService(schemaServiceProviderStub{snapshot: snapshot}, metadataLocaleRepository{values: values})
	localized := service.ForPrincipalLocale(t.Context(), principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}, " de ")
	if localized.Name != "Anwendung" || !strings.HasSuffix(localized.SchemaHash, ":de") || len(localized.Objects) != 2 || len(localized.Agents) != 1 {
		t.Fatalf("localized snapshot=%#v", localized)
	}
	if got := service.ForPrincipalLocale(t.Context(), principalmodel.Principal{}, ""); got.SchemaHash != "hash" {
		t.Fatalf("empty locale changed snapshot=%#v", got)
	}
}

func TestApplicationSchemaLocaleRepositoryFailuresAndEmptyValuesFallback(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{SchemaHash: "hash"}
	for _, repository := range []metadataLocaleRepository{{err: errors.New("store failed")}, {values: nil}} {
		service := NewApplicationSchemaDomainService(schemaServiceProviderStub{snapshot: snapshot}, repository)
		if got := service.ForPrincipalLocale(t.Context(), principalmodel.Principal{}, "de"); got.SchemaHash != "hash" {
			t.Fatalf("fallback snapshot=%#v", got)
		}
	}
	if option := localizeSchemaValueOption(map[string]any{"value": ""}, nil, "field_option", "order.status"); option["value"] != "" {
		t.Fatalf("empty option=%#v", option)
	}
}
