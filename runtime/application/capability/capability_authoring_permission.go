package capability

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

import "sort"

type authoringCapabilityLocation struct {
	domainIndex     int
	capabilityIndex int
}

func materializeAuthoringCapabilityPermissions(contract *capabilitycontract.CapabilityRuntimeAuthoringContract) {
	if contract == nil {
		return
	}
	locations := make(map[string]authoringCapabilityLocation)
	for domainIndex := range contract.Domains {
		for capabilityIndex := range contract.Domains[domainIndex].Capabilities {
			capability := contract.Domains[domainIndex].Capabilities[capabilityIndex]
			locations[capability.Key] = authoringCapabilityLocation{domainIndex: domainIndex, capabilityIndex: capabilityIndex}
		}
	}
	resolved := make(map[string][]string, len(locations))
	for key := range locations {
		resolveAuthoringCapabilityPermissions(contract, key, locations, resolved, map[string]bool{})
	}
}

func resolveAuthoringCapabilityPermissions(contract *capabilitycontract.CapabilityRuntimeAuthoringContract, key string, locations map[string]authoringCapabilityLocation, resolved map[string][]string, visiting map[string]bool) []string {
	if permissions, exists := resolved[key]; exists {
		return permissions
	}
	location, exists := locations[key]
	if !exists || visiting[key] {
		return nil
	}
	visiting[key] = true
	capability := &contract.Domains[location.domainIndex].Capabilities[location.capabilityIndex]
	permissionSet := make(map[string]bool, len(capability.Permissions))
	for _, permission := range capability.Permissions {
		permissionSet[permission] = true
	}
	// Requires describes capability/data dependencies, not authorization
	// inheritance. Each operation declares the permission required to execute
	// that operation; inheriting an authoring permission from a dependency would
	// incorrectly turn read-only Runtime endpoints into workspace-admin APIs.
	// Legacy leaf capabilities without an explicit policy still inherit their
	// parent's policy until they are migrated to a direct declaration.
	if len(permissionSet) == 0 {
		for _, requiredKey := range capability.Requires {
			for _, permission := range resolveAuthoringCapabilityPermissions(contract, requiredKey, locations, resolved, visiting) {
				permissionSet[permission] = true
			}
		}
	}
	delete(visiting, key)
	permissions := make([]string, 0, len(permissionSet))
	for permission := range permissionSet {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	capability.Permissions = permissions
	resolved[key] = permissions
	return permissions
}
