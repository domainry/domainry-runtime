package runtimehost

import (
	"context"
	"fmt"
	"net/http"

	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type identityOperationsPersistenceBinding interface {
	BindOperationsPersistence() error
}

func openProjectIdentity(ctx context.Context, cfg config.Config, factory identitysdk.Factory, databases ...identitysdk.DatabaseHandle) (identitysdk.Binding, []identityhttpapi.Adapter, error) {
	if factory == nil {
		return nil, nil, fmt.Errorf("project composition did not supply an Identity SDK Factory")
	}
	application := identitysdk.ApplicationRef{
		WorkspaceID:    identitysdk.WorkspaceID(cfg.IdentityWorkspaceID),
		ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience),
	}
	var binding identitysdk.Binding
	var err error
	if externalFactory, ok := factory.(identitysdk.ExternalDatabaseFactory); ok && len(databases) > 0 {
		binding, err = externalFactory.OpenExternalWithDatabase(ctx, application, databases[0])
	} else if databaseFactory, ok := factory.(identitysdk.DatabaseFactory); ok && len(databases) > 0 {
		binding, err = databaseFactory.OpenWithDatabase(ctx, application, databases[0])
	} else {
		binding, err = factory.Open(ctx, application)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open project Identity integration: %w", err)
	}
	if binding == nil {
		return nil, nil, fmt.Errorf("Identity SDK Factory returned no Binding")
	}
	var adapters []identityhttpapi.Adapter
	if provider, ok := binding.(identityhttpapi.Provider); ok {
		adapters = provider.HTTPAdapters()
	}
	switch binding.Descriptor().Mode {
	case identitysdk.DeploymentModeExternal:
		if source, ok := binding.(identitysdk.PrincipalAuthenticationBinding); !ok || source.PrincipalAuthenticator() == nil {
			_ = binding.Close(context.WithoutCancel(ctx))
			return nil, nil, fmt.Errorf("external Identity binding returned no principal authenticator")
		}
	case identitysdk.DeploymentModeModule:
		if len(adapters) == 0 {
			_ = binding.Close(context.WithoutCancel(ctx))
			return nil, nil, fmt.Errorf("Identity module returned no HTTP adapters")
		}
	case identitysdk.DeploymentModeSaaS:
		if len(adapters) != 0 {
			_ = binding.Close(context.WithoutCancel(ctx))
			return nil, nil, fmt.Errorf("Identity SaaS binding returned in-process HTTP adapters")
		}
	default:
		_ = binding.Close(context.WithoutCancel(ctx))
		return nil, nil, fmt.Errorf("unsupported Identity deployment mode %q", binding.Descriptor().Mode)
	}
	return binding, append([]identityhttpapi.Adapter(nil), adapters...), nil
}

// bindEmbeddedIdentityOperations enables Identity receipt and write-fence
// persistence only after Runtime has prepared the project database. SaaS and
// external bindings own a different database and must bind their persistence
// inside the owner service instead.
func bindEmbeddedIdentityOperations(binding identitysdk.Binding) error {
	if binding == nil {
		return fmt.Errorf("Identity binding is required")
	}
	if binding.Descriptor().Mode != identitysdk.DeploymentModeModule {
		return nil
	}
	return bindIdentityOperationsPersistence(binding, "embedded Identity")
}

func bindIdentityOperationsPersistence(binding any, scope string) error {
	binder, ok := binding.(identityOperationsPersistenceBinding)
	if !ok {
		return fmt.Errorf("%s binding does not expose shared Operations persistence", scope)
	}
	if err := binder.BindOperationsPersistence(); err != nil {
		return fmt.Errorf("bind %s shared Operations persistence: %w", scope, err)
	}
	return nil
}

type identityAdapterRouter = moduleAdapterRouter

func newIdentityAdapterRouter(group runtimehttp.ListenerRouteGroup, fallback http.Handler) *identityAdapterRouter {
	return newModuleAdapterRouter(group, fallback)
}

func mountIdentityHTTPAdapters(group runtimehttp.ListenerRouteGroup, adapters []identityhttpapi.Adapter, fallback http.Handler) (http.Handler, error) {
	return mountModuleHTTPAdapters(group, adapters, fallback, func(_ modulehttp.Route, handler http.Handler, _ modulehttp.AuditRecorder) (http.Handler, error) {
		return handler, nil
	})
}

func identityRouteVisible(group runtimehttp.ListenerRouteGroup, exposures []identityhttpapi.Exposure) bool {
	return moduleRouteVisible(group, []modulehttp.Exposure(exposures))
}
