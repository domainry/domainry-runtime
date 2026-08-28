package policy

import principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

import (
	"fmt"
	"strings"
)

func isAllScope(scope string) bool {
	return strings.TrimSpace(scope) == "all_records"
}

func isOwnedScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	return scope == "owned_records"
}

func isSubordinatesScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	return scope == "subordinates"
}

func isDepartmentScope(scope string) bool {
	return strings.TrimSpace(scope) == "department"
}

func isDepartmentAndChildrenScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	return scope == "department_and_children"
}

func isTeamScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	return scope == "team"
}

func RecordOwnerFieldKey(object definitionmodel.ObjectSchema) string {
	if explicit := RecordScopeOwnerFieldKey(object); explicit != "" {
		return explicit
	}
	if strings.TrimSpace(fmt.Sprint(object.UX["kind"])) == "identity_profile_extension" {
		if config, ok := object.UX["config"].(map[string]any); ok {
			key := strings.TrimSpace(fmt.Sprint(config["identity_relation_field"]))
			for _, field := range object.Fields {
				if field.Key == key && field.Type == "relation" {
					return key
				}
			}
		}
	}
	for _, preferred := range []string{"owner", "assignee", "requester", "created_by", "createdBy"} {
		for _, field := range object.Fields {
			if field.Key == preferred && field.Type == "user" {
				return field.Key
			}
		}
	}
	for _, field := range object.Fields {
		if field.Type == "user" {
			return field.Key
		}
	}
	return ""
}

// RecordScopeOwnerFieldKey returns the one field explicitly designated as the
// owner whose active Workforce assignment controls department scope. It never
// guesses: owner/assignee heuristics remain available for owned_records, but
// department-derived facts require an authored contract.
func RecordScopeOwnerFieldKey(object definitionmodel.ObjectSchema) string {
	for _, field := range object.Fields {
		enabled, valid := boolAny(field.Config["scope_owner"])
		if valid && enabled {
			return field.Key
		}
	}
	return ""
}

func RecordOwnerDepartmentPathFieldKey(object definitionmodel.ObjectSchema) string {
	for _, field := range object.Fields {
		if enabled, valid := boolAny(field.Config["scope_organization_path"]); valid && enabled {
			return field.Key
		}
	}
	for _, preferred := range []string{"owner_department_path", "ownerDepartmentPath"} {
		for _, field := range object.Fields {
			if field.Key == preferred {
				return field.Key
			}
		}
	}
	return ""
}

func RecordOwnerDepartmentIDFieldKey(object definitionmodel.ObjectSchema) string {
	for _, field := range object.Fields {
		if enabled, valid := boolAny(field.Config["scope_organization_id"]); valid && enabled {
			return field.Key
		}
	}
	for _, preferred := range []string{"owner_department_id", "ownerDepartmentId"} {
		for _, field := range object.Fields {
			if field.Key == preferred {
				return field.Key
			}
		}
	}
	return ""
}

func RecordTeamFieldKey(object definitionmodel.ObjectSchema) string {
	return firstExistingFieldKey(object, []string{"team", "team_id", "owner_team", "owner_team_id", "assigned_team"})
}

func RecordStoreFieldKey(object definitionmodel.ObjectSchema) string {
	return firstExistingFieldKey(object, []string{"store", "store_id", "store_profile", "store_profile_id", "owner_store", "owner_store_id"})
}

func RecordTerritoryFieldKey(object definitionmodel.ObjectSchema) string {
	return firstExistingFieldKey(object, []string{"territory", "territory_id", "territory_owner", "owner_territory", "owner_territory_id"})
}

func RecordWarehouseFieldKey(object definitionmodel.ObjectSchema) string {
	return firstExistingFieldKey(object, []string{"warehouse", "warehouse_id", "receiving_warehouse", "to_warehouse", "owner_warehouse", "owner_warehouse_id"})
}

func firstExistingFieldKey(object definitionmodel.ObjectSchema, candidates []string) string {
	for _, preferred := range candidates {
		for _, field := range object.Fields {
			if field.Key == preferred {
				return field.Key
			}
		}
	}
	return ""
}

func recordValueInPrincipalSet(object definitionmodel.ObjectSchema, data map[string]any, fieldKey string, values []string) bool {
	if fieldKey == "" || len(values) == 0 {
		return false
	}
	actual := strings.TrimSpace(fmt.Sprint(data[fieldKey]))
	return containsString(values, actual)
}

func teamScopeMatches(principal principalmodel.Principal, object definitionmodel.ObjectSchema, data map[string]any) bool {
	if recordValueInPrincipalSet(object, data, RecordTeamFieldKey(object), principal.OrganizationScopes.TeamIDs) {
		return true
	}
	pathField := RecordOwnerDepartmentPathFieldKey(object)
	if pathField == "" || strings.TrimSpace(principal.DepartmentPath) == "" {
		return false
	}
	recordPath := strings.TrimSpace(fmt.Sprint(data[pathField]))
	basePath := strings.TrimRight(principal.DepartmentPath, "/")
	return recordPath == basePath || strings.HasPrefix(recordPath, basePath+"/")
}

func principalScopeValues(principal principalmodel.Principal, scope string) []string {
	switch strings.TrimSpace(scope) {
	case "teams", "team_ids":
		return principal.OrganizationScopes.TeamIDs
	case "stores", "store_ids":
		return principal.OrganizationScopes.StoreIDs
	case "territories", "territory_ids":
		return principal.OrganizationScopes.TerritoryIDs
	case "warehouses", "warehouse_ids":
		return principal.OrganizationScopes.WarehouseIDs
	default:
		return nil
	}
}
