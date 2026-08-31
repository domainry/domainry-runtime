package recordtimer

import principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

func recordTimerRuntimeScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test record timer owner operation")
}
