package capability

import (
	"strings"
	"testing"
)

func TestRuntimeAuthoringCapabilitiesPublishExecutionSemantics(t *testing.T) {
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, definition := range domain.Capabilities {
			execution := definition.Execution
			if execution == nil || strings.TrimSpace(execution.Transaction) == "" || strings.TrimSpace(execution.Idempotency) == "" || strings.TrimSpace(execution.SideEffectLevel) == "" || strings.TrimSpace(execution.PermissionModel) == "" {
				t.Errorf("%s has incomplete execution semantics: %#v", definition.Key, execution)
				continue
			}
			if execution.SideEffectLevel != "none" && len(execution.ReadSet) == 0 && len(execution.WriteSet) == 0 {
				t.Errorf("%s declares neither read_set nor write_set", definition.Key)
			}
			if strings.HasPrefix(execution.SideEffectLevel, "external") && strings.TrimSpace(execution.Compensation) == "" {
				t.Errorf("%s has external side effects without compensation semantics", definition.Key)
			}
			if len(execution.WriteSet) > 0 && execution.SideEffectLevel != "none" && len(execution.SideEffects) == 0 {
				t.Errorf("%s writes state without declaring audit/event/outbox side effects", definition.Key)
			}
		}
	}
}
