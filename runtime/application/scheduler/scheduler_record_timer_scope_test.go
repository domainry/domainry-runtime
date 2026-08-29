package scheduler

import principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

func schedulerRuntimeScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test record timer owner operation")
}
