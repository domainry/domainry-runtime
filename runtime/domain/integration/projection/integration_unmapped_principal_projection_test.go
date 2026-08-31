package projection

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestIntegrationUnmappedReadOnlyPrincipalIsStableAndLeastPrivilege(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"z_order": {}, " customer ": {}, "": {}, "   ": {},
	}
	principal := IntegrationUnmappedReadOnlyPrincipal(" workspace-a ", " user@example.com / east ", objects)
	if principal.UserID != "integration:unmapped:user_example.com___east" || principal.WorkspaceID != " workspace-a " || principal.Known {
		t.Fatalf("principal identity = %#v", principal)
	}
	if principal.AccessBundle != nil || principal.HasPermission("customer.read") || principal.HasPermission("z_order.read") {
		t.Fatalf("unmapped principal received fabricated grants: %#v", principal)
	}
}

func TestIntegrationUnmappedReadOnlyPrincipalDefaultsEmptyIdentity(t *testing.T) {
	principal := IntegrationUnmappedReadOnlyPrincipal(" ", "\t\n", nil)
	if principal.WorkspaceID != "" || principal.UserID != "integration:unmapped:unknown" {
		t.Fatalf("principal = %#v", principal)
	}
	if principal.Known || principal.AccessBundle != nil {
		t.Fatalf("empty external identity must remain unmapped: %#v", principal)
	}
}

func TestIntegrationSanitizePrincipalKeyPreservesUnicodeAndSafePunctuation(t *testing.T) {
	if got := integrationSanitizePrincipalKey(" 张三-A_1.2+ops "); got != "张三-A_1.2_ops" {
		t.Fatalf("sanitized key = %q", got)
	}
}
