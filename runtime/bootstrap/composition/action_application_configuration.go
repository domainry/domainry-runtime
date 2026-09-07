package composition

import (
	"context"
	"fmt"
	"strings"

	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func resolveActionApplicationConfiguration(records *runtimeAssembly) func(context.Context, principalmodel.Principal) (actionapplication.ApplicationExecutionConfiguration, error) {
	return func(ctx context.Context, _ principalmodel.Principal) (actionapplication.ApplicationExecutionConfiguration, error) {
		if records.applicationSchemaRepo == nil {
			records.mu.RLock()
			revision, zone := records.actionMetadataRevision, strings.TrimSpace(records.timeZone)
			records.mu.RUnlock()
			// This is the manifest-owned default, for assemblies without a
			// persisted catalog; it is unrelated to the host's local clock.
			if zone == "" {
				zone = "UTC"
			}
			return actionapplication.ApplicationExecutionConfiguration{SchemaRevision: revision, TimeZone: zone}, nil
		}
		reader, ok := records.applicationSchemaRepo.(appschemarepository.ApplicationExecutionConfigurationReader)
		if !ok {
			return actionapplication.ApplicationExecutionConfiguration{}, fmt.Errorf("application catalog does not provide Action execution configuration")
		}
		value, err := reader.ExecutionConfiguration(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "resolve Action application configuration"))
		if err != nil {
			return actionapplication.ApplicationExecutionConfiguration{}, err
		}
		return actionapplication.ApplicationExecutionConfiguration{SchemaRevision: value.SchemaRevision, TimeZone: value.TimeZone}, nil
	}
}
