package transport

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/ratelimit"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	blobstore "github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

// AssembleHTTPServer exposes the Bootstrap composition root for integration
// tests and embedders that already own an assembled RuntimeServices graph.
func AssembleHTTPServer(ctx context.Context, records *composition.RuntimeServices, identity identitysdk.Binding, uploadDir string, corsAllowedOrigins []string, allowDevAuthHeaders bool) *runtimehttp.HTTPRouter {
	if identity == nil {
		panic("transport.AssembleHTTPServer requires an Identity SDK Binding")
	}
	root := strings.TrimSpace(uploadDir)
	if root == "" {
		root = "../data/uploads"
	}
	blobs, err := blobstore.NewLocalStore(root)
	if err != nil {
		panic("transport.AssembleHTTPServer initialize local blob store: " + err.Error())
	}
	return AssembleRuntimeHTTPServer(ctx, HTTPServerDependencies{
		Records: records, IdentityBinding: identity,
		BlobStore:   blobs,
		RateLimiter: ratelimit.NewMemoryLimiter(ratelimit.DefaultMemoryCapacity),
		Config: config.Config{
			UploadDir: uploadDir, CORSAllowedOrigins: append([]string(nil), corsAllowedOrigins...),
			RuntimeAllowDevIdentityHeaders: allowDevAuthHeaders,
			IdentityWorkspaceID:            principalmodel.InstallationWorkspaceID, IdentityAudience: "domainry-runtime",
		},
	})
}
