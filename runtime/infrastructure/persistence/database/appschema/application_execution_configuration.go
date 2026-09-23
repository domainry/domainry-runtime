package appschema

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r ApplicationSchemaStore) ExecutionConfiguration(ctx context.Context, scope principalmodel.SystemScope) (appschemamodel.ApplicationExecutionConfiguration, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return appschemamodel.ApplicationExecutionConfiguration{}, err
	}
	statement, args, err := query.NewSelectBuilder(r.store.SQLRenderer, "_project_model_state").
		Columns("model_hash", "catalog_hash", "time_zone").Where(query.Equal("id", "current")).Build()
	if err != nil {
		return appschemamodel.ApplicationExecutionConfiguration{}, err
	}
	executor := database.ActionExecutionExecutor(r.database())
	if transaction := database.ActionExecutionTransaction(ctx); transaction != nil {
		executor = transaction
	}
	var modelHash, catalogHash, zone string
	if err := executor.QueryRowContext(ctx, statement, args...).Scan(&modelHash, &catalogHash, &zone); err != nil {
		return appschemamodel.ApplicationExecutionConfiguration{}, fmt.Errorf("load Action application configuration: %w", err)
	}
	if strings.TrimSpace(catalogHash) == "" {
		return appschemamodel.ApplicationExecutionConfiguration{}, fmt.Errorf("Action application schema revision is missing")
	}
	if err := runtimeext.ValidateApplicationTimeZone(zone); err != nil {
		return appschemamodel.ApplicationExecutionConfiguration{}, err
	}
	return appschemamodel.ApplicationExecutionConfiguration{SchemaRevision: strings.TrimSpace(modelHash) + ":" + strings.TrimSpace(catalogHash), TimeZone: zone}, nil
}
