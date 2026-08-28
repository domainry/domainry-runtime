package validation

import deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

import (
	"path"
	"sort"
	"strings"
)

func (validator *frontendCapabilityUsageValidator) validateEntryRoute(fieldPath string, entry deploymentmodel.FrontendCapabilitySupportEntry) {
	route := strings.TrimSpace(entry.Route)
	if !strings.HasPrefix(route, "/") || strings.ContainsAny(route, "?#") || strings.Contains(route, "//") || (route != "/" && strings.HasSuffix(route, "/")) || path.Clean(route) != route {
		validator.issue("error", entry.SupportKey, fieldPath+".route", "backend.frontend.route_invalid", "", map[string]string{"actual": route})
	}
}

func (validator *frontendCapabilityUsageValidator) validateEntryEvidence(fieldPath string, entry deploymentmodel.FrontendCapabilitySupportEntry) {
	module := strings.TrimSpace(entry.FeatureModule)
	if module == "" || strings.HasPrefix(module, "/") || strings.Contains(module, "..") {
		validator.issue("error", entry.SupportKey, fieldPath+".feature_module", "backend.frontend.feature_module_invalid", "", map[string]string{"actual": module})
	}
	if len(entry.AcceptanceTests) == 0 {
		validator.issue("error", entry.SupportKey, fieldPath+".acceptance_tests", "backend.frontend.evidence_required", "", nil)
	}
	seen := map[string]bool{}
	for index, raw := range entry.AcceptanceTests {
		testPath := strings.TrimSpace(raw)
		path := fieldPath + ".acceptance_tests[" + stringIndex(index) + "]"
		if testPath == "" || strings.HasPrefix(testPath, "/") || strings.Contains(testPath, "..") || seen[testPath] {
			validator.issue("error", entry.SupportKey, path, "backend.frontend.acceptance_test_invalid", "", map[string]string{"actual": testPath})
		}
		seen[testPath] = true
	}
}

func (validator *frontendCapabilityUsageValidator) validateEntryCapabilities(fieldPath string, entry *deploymentmodel.FrontendCapabilitySupportEntry) {
	seen := map[string]bool{}
	for index, raw := range entry.CapabilityKeys {
		key := strings.TrimSpace(raw)
		path := fieldPath + ".capability_keys[" + stringIndex(index) + "]"
		if key == "" || seen[key] {
			validator.issue("error", entry.SupportKey, path, "backend.frontend.capability_duplicate", key, map[string]string{"actual": key})
		} else if _, exists := validator.capabilities[key]; !exists || validator.expectedSupport[key] != entry.SupportKey {
			validator.issue("error", entry.SupportKey, path, "backend.frontend.capability_binding_invalid", key, map[string]string{"capability": key, "support_key": entry.SupportKey})
		}
		entry.CapabilityKeys[index] = key
		seen[key] = true
	}
}

func (validator *frontendCapabilityUsageValidator) validateEntryPermissions(fieldPath string, entry *deploymentmodel.FrontendCapabilitySupportEntry) {
	declared := map[string]bool{}
	for index, raw := range entry.RequiredPermissions {
		permission := strings.TrimSpace(raw)
		path := fieldPath + ".required_permissions[" + stringIndex(index) + "]"
		if permission == "" || declared[permission] {
			validator.issue("error", entry.SupportKey, path, "backend.frontend.permission_invalid", "", map[string]string{"actual": permission})
		}
		entry.RequiredPermissions[index] = permission
		declared[permission] = true
	}
	expected := map[string]string{}
	for _, capabilityKey := range entry.CapabilityKeys {
		for _, permission := range validator.capabilities[capabilityKey].Permissions {
			expected[permission] = capabilityKey
		}
	}
	permissions := make([]string, 0, len(expected))
	for permission := range expected {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	for _, permission := range permissions {
		if !declared[permission] {
			validator.issue("error", entry.SupportKey, fieldPath+".required_permissions", "backend.frontend.permission_missing", expected[permission], map[string]string{"permission": permission, "capability": expected[permission]})
		}
	}
}

func (validator *frontendCapabilityUsageValidator) validateEntryBusinessBindings(fieldPath string, entry *deploymentmodel.FrontendCapabilitySupportEntry) {
	bindings := []struct {
		name   string
		values *[]string
		field  bool
	}{
		{name: "actor_roles", values: &entry.ActorRoles},
		{name: "business_objects", values: &entry.BusinessObjects},
		{name: "view_keys", values: &entry.ViewKeys},
		{name: "implemented_actions", values: &entry.ImplementedActions},
		{name: "report_keys", values: &entry.ReportKeys},
		{name: "field_keys", values: &entry.FieldKeys, field: true},
		{name: "acceptance_claims", values: &entry.AcceptanceClaims},
	}
	for _, binding := range bindings {
		seen := map[string]bool{}
		for index, raw := range *binding.values {
			value := strings.TrimSpace(raw)
			path := fieldPath + "." + binding.name + "[" + stringIndex(index) + "]"
			invalid := value == "" || seen[value] || strings.ContainsAny(value, " \t\r\n")
			if binding.field {
				parts := strings.Split(value, ".")
				invalid = invalid || len(parts) != 2 || parts[0] == "" || parts[1] == ""
			}
			if invalid {
				validator.issue("error", entry.SupportKey, path, "backend.frontend.business_binding_invalid", "", map[string]string{"kind": binding.name, "actual": value})
			}
			(*binding.values)[index] = value
			seen[value] = true
		}
	}
}

func (validator *frontendCapabilityUsageValidator) appendMissingSupportWarnings() {
	capabilityKeys := make([]string, 0, len(validator.expectedSupport))
	for capability := range validator.expectedSupport {
		capabilityKeys = append(capabilityKeys, capability)
	}
	sort.Strings(capabilityKeys)
	for _, capability := range capabilityKeys {
		supportKey := validator.expectedSupport[capability]
		if !validator.registeredSupport[supportKey] {
			validator.issue("warning", supportKey, "entries", "backend.frontend.support_missing", capability, map[string]string{"support_key": supportKey, "capability": capability})
		}
	}
}
