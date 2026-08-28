package preference

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataPreferenceSourceStub struct {
	metadatarepository.MetadataRepository
	current  metadatamodel.MetadataDefinition
	found    bool
	getErr   error
	versions []metadatamodel.MetadataDefinitionVersion
	listErr  error
	getScope principalmodel.SystemScope
	key      string
}

func (s *metadataPreferenceSourceStub) GetDefinition(_ context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (metadatamodel.MetadataDefinition, bool, error) {
	s.getScope, s.key = scope, resourceType+":"+resourceKey
	return s.current, s.found, s.getErr
}

func (s *metadataPreferenceSourceStub) ListDefinitionVersions(_ context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	s.getScope, s.key = scope, resourceType+":"+resourceKey
	return append([]metadatamodel.MetadataDefinitionVersion(nil), s.versions...), s.listErr
}

func TestMetadataWorkspacePreferenceRepositoryMapsValidatedVersionsToExplicitWorkspace(t *testing.T) {
	t.Parallel()

	source := &metadataPreferenceSourceStub{
		found: true, current: metadatamodel.MetadataDefinition{ResourceKey: "policy.limit"},
		versions: []metadatamodel.MetadataDefinitionVersion{
			{SchemaVersion: "2", SchemaHash: "hash-2", Payload: preferenceMetadataPayload(`20`, "2026-07-01")},
			{SchemaVersion: "1", SchemaHash: "hash-1", Payload: preferenceMetadataPayload(`10`, "2026-01-01")},
		},
	}
	repository := NewMetadataWorkspacePreferenceRepository(source)
	scope, _ := principalmodel.NewWorkspaceQueryScope("workspace-a")
	versions, err := repository.ListWorkspacePreferenceVersions(t.Context(), scope, " policy.limit ")
	if err != nil {
		t.Fatalf("list preference versions: %v", err)
	}
	if !source.getScope.Valid() || source.getScope.Kind != principalmodel.SystemScopeInstallation || source.key != "preference:policy.limit" {
		t.Fatalf("metadata query scope=%+v key=%q", source.getScope, source.key)
	}
	if len(versions) != 2 || versions[0].WorkspaceID != "workspace-a" || versions[0].Version != "2" || versions[0].Definition.ValueType != "integer" {
		t.Fatalf("versions=%+v", versions)
	}
}

func TestMetadataWorkspacePreferenceRepositoryRejectsDisabledMalformedAndFailureEdges(t *testing.T) {
	t.Parallel()

	scope, _ := principalmodel.NewWorkspaceQueryScope("workspace-a")
	systemScope, _ := principalmodel.NewSystemQueryScope(principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test"))
	tests := []struct {
		name   string
		repo   *MetadataWorkspacePreferenceRepository
		scope  principalmodel.QueryScope
		key    string
		length int
	}{
		{name: "zero scope", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{}), scope: principalmodel.QueryScope{}, key: "policy", length: -1},
		{name: "invalid scope", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{}), scope: systemScope, key: "policy", length: -1},
		{name: "nil repository", repo: nil, scope: scope, key: "policy", length: -1},
		{name: "source required", repo: NewMetadataWorkspacePreferenceRepository(nil), scope: scope, key: "policy", length: -1},
		{name: "key required", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{}), scope: scope, length: -1},
		{name: "get failure", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{getErr: errors.New("get")}), scope: scope, key: "policy", length: -1},
		{name: "missing", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{}), scope: scope, key: "policy", length: 0},
		{name: "disabled", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{found: true, current: metadatamodel.MetadataDefinition{DisabledAt: "now"}}), scope: scope, key: "policy", length: 0},
		{name: "list failure", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{found: true, listErr: errors.New("list")}), scope: scope, key: "policy", length: -1},
		{name: "malformed stored version", repo: NewMetadataWorkspacePreferenceRepository(&metadataPreferenceSourceStub{found: true, versions: []metadatamodel.MetadataDefinitionVersion{{SchemaVersion: "1", Payload: json.RawMessage(`{"key":"policy"}`)}}}), scope: scope, key: "policy", length: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			versions, err := test.repo.ListWorkspacePreferenceVersions(t.Context(), test.scope, test.key)
			if test.length < 0 && err == nil {
				t.Fatalf("expected failure, got versions=%+v", versions)
			}
			if test.length >= 0 && (err != nil || len(versions) != test.length) {
				t.Fatalf("expected length %d, got versions=%+v err=%v", test.length, versions, err)
			}
		})
	}
}

func preferenceMetadataPayload(value, effectiveFrom string) json.RawMessage {
	return json.RawMessage(`{"key":"policy.limit","name":"Limit","value_type":"integer","value":` + value + `,"effective_from":"` + effectiveFrom + `"}`)
}
