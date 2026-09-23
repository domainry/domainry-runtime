package projectmodel

import "fmt"

const InstallationAdministratorRoleKey = "tenant_admin"

// ValidatePlatformRoleExtension protects the project grant extension of the
// Runtime-owned installation administrator role.
func ValidatePlatformRoleExtension(role Role) error {
	if !role.PlatformRoleExtension {
		return fmt.Errorf("role %q is platform-owned and cannot be declared by a project", role.Key)
	}
	if role.Key != InstallationAdministratorRoleKey {
		return fmt.Errorf("unknown platform role extension %q", role.Key)
	}
	if len(role.Permissions) == 0 {
		return fmt.Errorf("platform role extension %q must declare explicit business grants", role.Key)
	}
	if role.Name != "Installation administrator" || role.Audience != "user" || role.AssignmentMode != "system_managed" || role.RiskLevel != "privileged" ||
		role.ProvisionToWorkspaces || role.RequiredBindingKey != "" || len(role.I18n) != 0 || len(role.ConflictRoleKeys) != 0 || len(role.GrantableRoleKeys) != 0 ||
		len(role.PermissionSetKeys) != 0 || len(role.PermissionSetGroups) != 0 || len(role.GuardrailKeys) != 0 || len(role.Guardrails) != 0 {
		return fmt.Errorf("platform role extension %q cannot change platform-owned identity or assignment policy", role.Key)
	}
	return nil
}
