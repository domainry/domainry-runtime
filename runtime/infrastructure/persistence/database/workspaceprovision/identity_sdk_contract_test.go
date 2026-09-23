package workspaceprovision

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// Keep the shared provisioning probe aligned with the current bootstrap SDK
// contract so the storage tests exercise the real Binding boundary.
func (*identityBootstrapProbe) BindBootstrapProjectNavigationCatalog(context.Context, identitysdk.ProjectNavigationCatalog) error {
	return nil
}
