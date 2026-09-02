package service

import (
	"fmt"
	"sort"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	actionprojection "github.com/domainry/domainry-runtime/runtime/domain/action/projection"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
)

// AuthorizationRegistryInput contains source-owned Action contributions. The
// builder is the only merge policy used by Runtime startup and HTTP fallback
// assembly; it never reads persisted Identity authorization state.
type AuthorizationRegistryInput struct {
	Snapshot           appschemamodel.ApplicationSchemaSnapshot
	ApplicationKey     string
	ContributedActions []actioncontract.ActionDefinition
	EndpointContracts  map[string]endpointmodel.RuntimeEndpointContractV1
	NonHTTPBindings    map[string][]actioncontract.NonHTTPBinding
}

// BuildAuthorizationRegistry validates every contribution as one batch and
// returns an immutable registry. A module Action replaces a generated Runtime
// endpoint only when it owns the same Action key or exact HTTP binding.
func BuildAuthorizationRegistry(input AuthorizationRegistryInput) (*actioncontract.Registry, error) {
	applicationKey := strings.TrimSpace(input.ApplicationKey)
	if applicationKey == "" && (len(input.Snapshot.Objects) != 0 || len(input.Snapshot.Actions) != 0 || len(input.Snapshot.Workflows) != 0) {
		return nil, fmt.Errorf("Runtime application key is required for metadata Actions")
	}
	applicationOwner := "application:" + applicationKey
	registry := actioncontract.NewRegistry()
	for _, object := range input.Snapshot.Objects {
		definitions, err := actionprojection.DefaultActionsForObject(object, applicationOwner)
		if err != nil {
			return nil, err
		}
		if err := registry.Register(definitions...); err != nil {
			return nil, fmt.Errorf("register default object Actions: %w", err)
		}
	}
	for _, authored := range input.Snapshot.Actions {
		definition, err := actionprojection.AuthorizationActionDefinition(authored, actionprojection.AuthorizationContractContext{
			Owner: applicationOwner, CapabilityKey: strings.TrimSpace(authored.ObjectKey), CapabilityLabel: strings.TrimSpace(authored.ObjectKey),
			PermissionCategory: "Business Actions", Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic},
		})
		if err != nil {
			return nil, err
		}
		if err := registry.Register(definition); err != nil {
			return nil, fmt.Errorf("register authored Action %q: %w", authored.Key, err)
		}
	}
	for _, workflow := range input.Snapshot.Workflows {
		if !workflow.Enabled {
			continue
		}
		definition, err := actionprojection.AuthorizationActionForWorkflow(workflow, applicationOwner)
		if err != nil {
			return nil, err
		}
		if err := registry.Register(definition); err != nil {
			return nil, fmt.Errorf("register Workflow Action %q: %w", workflow.Key, err)
		}
	}
	moduleActionKeys := make(map[string]bool)
	moduleHTTPBindings := make(map[string]bool)
	nonHTTPBindings := cloneNonHTTPBindings(input.NonHTTPBindings)
	for _, definition := range input.ContributedActions {
		definition = attachNonHTTPBindings(definition, nonHTTPBindings)
		if err := registry.Register(definition); err != nil {
			return nil, fmt.Errorf("register contributed Action %q: %w", definition.Key, err)
		}
		moduleActionKeys[definition.Key] = true
		if definition.HTTP != nil {
			moduleHTTPBindings[strings.TrimSpace(definition.HTTP.Method)+" "+strings.TrimSpace(definition.HTTP.RouteTemplate)] = true
		}
	}
	endpointKeys := make([]string, 0, len(input.EndpointContracts))
	for key := range input.EndpointContracts {
		endpointKeys = append(endpointKeys, key)
	}
	sort.Strings(endpointKeys)
	for _, key := range endpointKeys {
		definition, err := endpointmodel.AuthorizationActionDefinition(input.EndpointContracts[key])
		if err != nil {
			return nil, err
		}
		pattern := ""
		if definition.HTTP != nil {
			pattern = definition.HTTP.Method + " " + definition.HTTP.RouteTemplate
		}
		if moduleActionKeys[definition.Key] || moduleHTTPBindings[pattern] {
			continue
		}
		definition = attachNonHTTPBindings(definition, nonHTTPBindings)
		if err := registry.Register(definition); err != nil {
			return nil, fmt.Errorf("register Runtime endpoint Action %q: %w", definition.Key, err)
		}
	}
	if len(nonHTTPBindings) != 0 {
		keys := make([]string, 0, len(nonHTTPBindings))
		for key := range nonHTTPBindings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("operation bindings reference unknown Actions: %s", strings.Join(keys, ", "))
	}
	if err := registry.Freeze(); err != nil {
		return nil, fmt.Errorf("freeze Runtime authorization Action registry: %w", err)
	}
	return registry, nil
}

func cloneNonHTTPBindings(source map[string][]actioncontract.NonHTTPBinding) map[string][]actioncontract.NonHTTPBinding {
	result := make(map[string][]actioncontract.NonHTTPBinding, len(source))
	for key, bindings := range source {
		result[strings.TrimSpace(key)] = append([]actioncontract.NonHTTPBinding(nil), bindings...)
	}
	return result
}

func attachNonHTTPBindings(definition actioncontract.ActionDefinition, bindings map[string][]actioncontract.NonHTTPBinding) actioncontract.ActionDefinition {
	key := strings.TrimSpace(definition.Key)
	additional, exists := bindings[key]
	if !exists {
		return definition
	}
	delete(bindings, key)
	definition.NonHTTP = append(append([]actioncontract.NonHTTPBinding(nil), definition.NonHTTP...), additional...)
	return definition
}
