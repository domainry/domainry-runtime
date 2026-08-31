package appschema

import (
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func requireMetadataInstallationScope(scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return err
	}
	if scope.Kind != principalmodel.SystemScopeInstallation {
		return fmt.Errorf("metadata installation scope is required")
	}
	return nil
}
