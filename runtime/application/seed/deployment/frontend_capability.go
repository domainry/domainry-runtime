package deploymentseed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

// InstallFrontendCapabilityManifest installs Builder-produced deployment
// evidence from a separate project artifact. It never enters the Runtime
// domain manifest or its content hash.
type FrontendCapabilityRegistrar interface {
	RegisterManifest(context.Context, deploymentmodel.FrontendCapabilityManifest, principalmodel.Principal) (deploymentmodel.FrontendCapabilitySnapshot, error)
}

func InstallFrontendCapabilityManifest(ctx context.Context, service FrontendCapabilityRegistrar, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read frontend capability manifest %s: %w", path, err)
	}
	var manifest deploymentmodel.FrontendCapabilityManifest
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode frontend capability manifest %s: %w", path, err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "runtime-provision", WorkspaceID: "__default__"}, SystemScope: principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "frontend_capability_seed"),
		SystemCapabilities: []string{"workspace.admin"},
	}
	ctx = requestcontext.WithWorkspaceID(ctx, principal.WorkspaceID)
	if _, err := service.RegisterManifest(ctx, manifest, principal); err != nil {
		return fmt.Errorf("install frontend capability manifest %s: %w", path, err)
	}
	return nil
}
