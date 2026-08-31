package surfacemodel

import "testing"

func TestRuntimeSurfaceContractV2(t *testing.T) {
	t.Parallel()

	if ContractVersion != "runtime-surface-contract-v2" {
		t.Fatalf("contract version=%q", ContractVersion)
	}
	tests := []struct {
		surface   ProductSurface
		shell     ShellClass
		ownership ShellOwnership
		audience  ActorAudience
		exposure  ExposureClass
		routeRoot string
	}{
		{ProductSurfaceBusinessWorkspace, ShellClassSourceOwnedBusiness, ShellOwnershipSourceOwned, ActorAudienceBusinessActor, ExposureClassPublic, "/business"},
		{ProductSurfaceAdminConsole, ShellClassPlatformAdmin, ShellOwnershipPlatformAdmin, ActorAudiencePlatformAdmin, ExposureClassPlatformAdmin, "/admin"},
		{ProductSurfaceConsumerPortal, ShellClassSourceOwnedPortal, ShellOwnershipSourceOwned, ActorAudienceConsumerProfile, ExposureClassPublic, "/portal"},
	}
	if got := len(ProductSurfaces()); got != len(tests) {
		t.Fatalf("surface count=%d want=%d", got, len(tests))
	}
	for _, test := range tests {
		test := test
		t.Run(string(test.surface), func(t *testing.T) {
			t.Parallel()
			if !test.surface.Valid() {
				t.Fatal("surface must be valid")
			}
			if shell, ok := test.surface.Shell(); !ok || shell != test.shell {
				t.Fatalf("shell=(%q,%t) want=%q", shell, ok, test.shell)
			}
			if ownership, ok := test.surface.ShellOwnership(); !ok || ownership != test.ownership {
				t.Fatalf("ownership=(%q,%t) want=%q", ownership, ok, test.ownership)
			}
			if audience, ok := test.surface.RequiredAudience(); !ok || audience != test.audience {
				t.Fatalf("audience=(%q,%t) want=%q", audience, ok, test.audience)
			}
			if exposure, ok := test.surface.Exposure(); !ok || exposure != test.exposure {
				t.Fatalf("exposure=(%q,%t) want=%q", exposure, ok, test.exposure)
			}
			if routeRoot, ok := test.surface.DevelopmentRouteRoot(); !ok || routeRoot != test.routeRoot {
				t.Fatalf("route root=(%q,%t) want=%q", routeRoot, ok, test.routeRoot)
			}
		})
	}
}

func TestPublicationHandoffOutcomeIsAvailableToItsConsumerOwner(t *testing.T) {
	const endpoint = "GET /business/publication-handoffs/{messageID}"
	surfaces := RoutePolicies[endpoint]
	want := map[ProductSurface]bool{
		ProductSurfaceBusinessWorkspace: true,
		ProductSurfaceConsumerPortal:    true,
	}
	for _, surface := range surfaces {
		delete(want, surface)
	}
	if len(want) != 0 {
		t.Fatalf("publication handoff route is missing owner surfaces: %v", want)
	}
}

func TestRuntimeSurfaceContractRejectsUnknownValues(t *testing.T) {
	t.Parallel()
	if surface, ok := ParseProductSurface(" admin_console "); !ok || surface != ProductSurfaceAdminConsole {
		t.Fatalf("parsed=(%q,%t)", surface, ok)
	}
	if surface, ok := ParseProductSurface("console"); ok || surface.Valid() {
		t.Fatalf("legacy surface must fail closed: (%q,%t)", surface, ok)
	}
	if surface, ok := ParseProductSurface(""); ok || surface.Valid() {
		t.Fatalf("empty surface must fail closed: (%q,%t)", surface, ok)
	}
}
