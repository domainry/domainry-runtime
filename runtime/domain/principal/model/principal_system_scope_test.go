package principalmodel

import "testing"

func TestSystemPrincipalRequiresExplicitKindAndPurpose(t *testing.T) {
	valid := NewSystemPrincipal(" runtime-provision ", NewSystemScope(SystemScopeInstallation, " manifest install "), "runtime.install")
	if !valid.Known || valid.UserID != "runtime-provision" || valid.SystemScope.Purpose != "manifest install" {
		t.Fatalf("unexpected explicit system principal: %#v", valid)
	}
	for _, scope := range []SystemScope{
		NewSystemScope("unknown", "purpose"),
		NewSystemScope(SystemScopeBootstrap, ""),
	} {
		if principal := NewSystemPrincipal("system", scope); principal.Known || principal.SystemScope.Valid() {
			t.Fatalf("invalid system scope produced authenticated principal: %#v", principal)
		}
	}
}

func TestSystemCapabilitiesAreExactAndWildcardShapesNeverAuthorize(t *testing.T) {
	principal := NewSystemPrincipal(
		"worker",
		NewSystemScope(SystemScopeRuntimeGlobal, "execute one work item"),
		"customer.read", "customer.*", "*",
	)
	if !principal.HasPermission("customer.read") {
		t.Fatal("exact system capability was rejected")
	}
	if principal.HasPermission("customer.update") || principal.HasPermission("order.read") {
		t.Fatal("wildcard-shaped system capability authorized an unrelated operation")
	}

	narrowed := principal.WithExactSystemCapabilities(" customer.update ", "customer.update", "order.*", "*")
	if !narrowed.HasPermission("customer.update") {
		t.Fatal("concrete work-item capability was not added")
	}
	if narrowed.HasPermission("order.read") {
		t.Fatal("work-item narrowing accepted a wildcard-shaped capability")
	}
	count := 0
	for _, capability := range narrowed.SystemCapabilities {
		if capability == "customer.update" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("exact system capability was not de-duplicated: %#v", narrowed.SystemCapabilities)
	}
}
