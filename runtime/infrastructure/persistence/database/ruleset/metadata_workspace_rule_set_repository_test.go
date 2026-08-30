package ruleset

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataRuleSetSourceStub struct {
	appschemarepository.ApplicationSchemaRepository
	current  appschemamodel.ApplicationDefinition
	found    bool
	versions []appschemamodel.ApplicationDefinitionVersion
	scope    principalmodel.SystemScope
	key      string
	getErr   error
	listErr  error
}

func (s *metadataRuleSetSourceStub) GetDefinition(_ context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (appschemamodel.ApplicationDefinition, bool, error) {
	s.scope, s.key = scope, resourceType+":"+resourceKey
	if s.getErr != nil {
		return appschemamodel.ApplicationDefinition{}, false, s.getErr
	}
	return s.current, s.found, nil
}

func (s *metadataRuleSetSourceStub) ListDefinitionVersions(_ context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) ([]appschemamodel.ApplicationDefinitionVersion, error) {
	s.scope, s.key = scope, resourceType+":"+resourceKey
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]appschemamodel.ApplicationDefinitionVersion(nil), s.versions...), nil
}

func TestMetadataWorkspaceRuleSetRepositoryMapsValidatedVersionsToExplicitWorkspace(t *testing.T) {
	source := &metadataRuleSetSourceStub{found: true, current: appschemamodel.ApplicationDefinition{ResourceKey: "policy.limit"}, versions: []appschemamodel.ApplicationDefinitionVersion{{SchemaVersion: "2", SchemaHash: "hash-2", Payload: ruleSetMetadataPayload("2026-07-01")}, {SchemaVersion: "1", SchemaHash: "hash-1", Payload: ruleSetMetadataPayload("2026-01-01")}}}
	repository := NewApplicationSchemaWorkspaceRuleSetRepository(source)
	scope, _ := principalmodel.NewWorkspaceQueryScope("workspace-a")
	versions, err := repository.ListWorkspaceRuleSetVersions(t.Context(), scope, " policy.limit ")
	if err != nil {
		t.Fatal(err)
	}
	if !source.scope.Valid() || source.scope.Kind != principalmodel.SystemScopeInstallation || source.key != "rule_set:policy.limit" || len(versions) != 2 || versions[0].WorkspaceID != "workspace-a" || versions[0].Version != "2" {
		t.Fatalf("source=%+v key=%q versions=%+v", source.scope, source.key, versions)
	}
}

func TestMetadataWorkspaceRuleSetRepositoryRejectsInvalidInputsAndSourceFailures(t *testing.T) {
	workspaceScope, _ := principalmodel.NewWorkspaceQueryScope("workspace-a")
	systemScope, _ := principalmodel.NewSystemQueryScope(principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test"))
	validSource := func() *metadataRuleSetSourceStub {
		return &metadataRuleSetSourceStub{
			found: true,
			current: appschemamodel.ApplicationDefinition{
				ResourceKey: "policy.limit",
			},
			versions: []appschemamodel.ApplicationDefinitionVersion{{
				SchemaVersion: "1", SchemaHash: "hash-1", Payload: ruleSetMetadataPayload("2026-01-01"),
			}},
		}
	}
	assertError := func(t *testing.T, repository *ApplicationSchemaWorkspaceRuleSetRepository, scope principalmodel.QueryScope, key string) {
		t.Helper()
		if _, err := repository.ListWorkspaceRuleSetVersions(t.Context(), scope, key); err == nil {
			t.Fatal("expected repository error")
		}
	}

	assertError(t, NewApplicationSchemaWorkspaceRuleSetRepository(validSource()), principalmodel.QueryScope{}, "policy.limit")
	assertError(t, NewApplicationSchemaWorkspaceRuleSetRepository(validSource()), systemScope, "policy.limit")
	assertError(t, nil, workspaceScope, "policy.limit")
	assertError(t, NewApplicationSchemaWorkspaceRuleSetRepository(nil), workspaceScope, "policy.limit")
	assertError(t, NewApplicationSchemaWorkspaceRuleSetRepository(validSource()), workspaceScope, " ")

	source := validSource()
	source.getErr = errors.New("get failed")
	assertError(t, NewApplicationSchemaWorkspaceRuleSetRepository(source), workspaceScope, "policy.limit")

	source = validSource()
	source.listErr = errors.New("list failed")
	assertError(t, NewApplicationSchemaWorkspaceRuleSetRepository(source), workspaceScope, "policy.limit")

	source = validSource()
	source.versions[0].Payload = json.RawMessage(`{`)
	assertError(t, NewApplicationSchemaWorkspaceRuleSetRepository(source), workspaceScope, "policy.limit")
}

func TestMetadataWorkspaceRuleSetRepositoryReturnsEmptyForMissingOrDisabledDefinition(t *testing.T) {
	scope, _ := principalmodel.NewWorkspaceQueryScope("workspace-a")
	for _, source := range []*metadataRuleSetSourceStub{
		{found: false},
		{found: true, current: appschemamodel.ApplicationDefinition{ResourceKey: "policy.limit", DisabledAt: "2026-07-27T00:00:00Z"}},
	} {
		versions, err := NewApplicationSchemaWorkspaceRuleSetRepository(source).ListWorkspaceRuleSetVersions(t.Context(), scope, "policy.limit")
		if err != nil || len(versions) != 0 {
			t.Fatalf("versions=%#v err=%v", versions, err)
		}
	}
}

func ruleSetMetadataPayload(from string) json.RawMessage {
	return json.RawMessage(`{"key":"policy.limit","name":"Limit","match_policy":"first_match","input_types":{"count":"integer"},"output_types":{"allowed":"boolean"},"effective_from":"` + from + `","rules":[{"key":"allow","priority":1,"when":{"kind":"literal","value_type":"boolean","value":true},"outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":true}}}],"default_outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":false}}}`)
}
