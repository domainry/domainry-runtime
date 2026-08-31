package transport

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-foundation/ratelimit"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

// AssembleHTTPServer exposes the Bootstrap composition root for integration
// tests and embedders that already own an assembled RuntimeServices graph.
func AssembleHTTPServer(ctx context.Context, records *composition.RuntimeServices, identity identitysdk.Binding, uploadDir string, corsAllowedOrigins []string, allowDevAuthHeaders bool) *runtimehttp.HTTPRouter {
	if identity == nil {
		panic("transport.AssembleHTTPServer requires an Identity SDK Binding")
	}
	return AssembleRuntimeHTTPServer(ctx, HTTPServerDependencies{
		Records: records, IdentityBinding: identity,
		RateLimiter: ratelimit.NewMemoryLimiter(ratelimit.DefaultMemoryCapacity),
		Config: config.Config{
			UploadDir: uploadDir, CORSAllowedOrigins: append([]string(nil), corsAllowedOrigins...),
			RuntimeAllowDevIdentityHeaders: allowDevAuthHeaders,
			IdentityWorkspaceID:            principalmodel.InstallationWorkspaceID, IdentityAudience: "domainry-runtime",
		},
	})
}
