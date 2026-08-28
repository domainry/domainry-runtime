package surfacemodel

import (
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

const ContractVersion = runtimeext.SurfaceContractVersion

type ProductSurface string

const (
	ProductSurfaceBusinessWorkspace ProductSurface = runtimeext.SurfaceBusinessWorkspace
	ProductSurfaceAdminConsole      ProductSurface = runtimeext.SurfaceAdminConsole
	ProductSurfaceConsumerPortal    ProductSurface = runtimeext.SurfaceConsumerPortal
)

type ShellClass string

const (
	ShellClassSourceOwnedBusiness ShellClass = runtimeext.SurfaceBusinessShell
	ShellClassPlatformAdmin       ShellClass = runtimeext.SurfaceAdminShell
	ShellClassSourceOwnedPortal   ShellClass = runtimeext.SurfacePortalShell
)

type ShellOwnership string

const (
	ShellOwnershipSourceOwned   ShellOwnership = "source_owned"
	ShellOwnershipPlatformAdmin ShellOwnership = "platform_admin"
)

type ActorAudience string

const (
	ActorAudienceBusinessActor   ActorAudience = runtimeext.SurfaceBusinessActor
	ActorAudienceConsumerProfile ActorAudience = runtimeext.SurfaceConsumerProfile
	ActorAudiencePlatformAdmin   ActorAudience = runtimeext.SurfacePlatformAdmin
)

type ExposureClass string

const (
	ExposureClassPublic        ExposureClass = runtimeext.SurfaceExposurePublic
	ExposureClassPlatformAdmin ExposureClass = runtimeext.SurfaceExposurePlatformAdmin
)

var productSurfaceContracts = map[ProductSurface]struct {
	Shell                ShellClass
	Ownership            ShellOwnership
	Audience             ActorAudience
	Exposure             ExposureClass
	DevelopmentRouteRoot string
}{
	ProductSurfaceBusinessWorkspace: {
		Shell: ShellClassSourceOwnedBusiness, Ownership: ShellOwnershipSourceOwned,
		Audience: ActorAudienceBusinessActor, Exposure: ExposureClassPublic,
		DevelopmentRouteRoot: runtimeext.SurfaceDevelopmentBusinessRouteRoot,
	},
	ProductSurfaceAdminConsole: {
		Shell: ShellClassPlatformAdmin, Ownership: ShellOwnershipPlatformAdmin,
		Audience: ActorAudiencePlatformAdmin, Exposure: ExposureClassPlatformAdmin,
		DevelopmentRouteRoot: runtimeext.SurfaceDevelopmentAdminRouteRoot,
	},
	ProductSurfaceConsumerPortal: {
		Shell: ShellClassSourceOwnedPortal, Ownership: ShellOwnershipSourceOwned,
		Audience: ActorAudienceConsumerProfile, Exposure: ExposureClassPublic,
		DevelopmentRouteRoot: runtimeext.SurfaceDevelopmentPortalRouteRoot,
	},
}

func ParseProductSurface(value string) (ProductSurface, bool) {
	surface := ProductSurface(strings.TrimSpace(value))
	_, ok := productSurfaceContracts[surface]
	return surface, ok
}

func (surface ProductSurface) Valid() bool {
	_, ok := productSurfaceContracts[surface]
	return ok
}

func (surface ProductSurface) Shell() (ShellClass, bool) {
	contract, ok := productSurfaceContracts[surface]
	return contract.Shell, ok
}

func (surface ProductSurface) ShellOwnership() (ShellOwnership, bool) {
	contract, ok := productSurfaceContracts[surface]
	return contract.Ownership, ok
}

func (surface ProductSurface) RequiredAudience() (ActorAudience, bool) {
	contract, ok := productSurfaceContracts[surface]
	return contract.Audience, ok
}

func (surface ProductSurface) Exposure() (ExposureClass, bool) {
	contract, ok := productSurfaceContracts[surface]
	return contract.Exposure, ok
}

func (surface ProductSurface) DevelopmentRouteRoot() (string, bool) {
	contract, ok := productSurfaceContracts[surface]
	return contract.DevelopmentRouteRoot, ok
}

func ProductSurfaces() []ProductSurface {
	return []ProductSurface{
		ProductSurfaceBusinessWorkspace,
		ProductSurfaceAdminConsole,
		ProductSurfaceConsumerPortal,
	}
}
